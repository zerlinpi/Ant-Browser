package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

// TestMigrationsAgainstPostgres is intentionally opt-in so normal unit tests
// remain hermetic. CI and release validation should provide a disposable,
// empty database through ANT_TEST_DATABASE_URL.
func TestMigrationsAgainstPostgres(t *testing.T) {
	databaseURL := os.Getenv("ANT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ANT_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	migrationsPath, err := filepath.Abs(filepath.Join("..", "..", "..", "database", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, databaseURL, migrationsPath); err != nil {
		t.Fatal(err)
	}
	var applied int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied < 15 {
		t.Fatalf("only %d migrations were applied", applied)
	}
	contract, err := os.ReadFile(filepath.Join(migrationsPath, "..", "tests", "account_proxy_contract.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(contract)); err != nil {
		t.Fatalf("account/proxy SQL contract: %v", err)
	}
	workflowContract, err := os.ReadFile(filepath.Join(migrationsPath, "..", "tests", "workflow_task_contract.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(workflowContract)); err != nil {
		t.Fatalf("workflow SQL contract: %v", err)
	}
	securityContract, err := os.ReadFile(filepath.Join(migrationsPath, "..", "tests", "security_rls_contract.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(securityContract)); err != nil {
		t.Fatalf("security/RLS SQL contract: %v", err)
	}
	for _, name := range []string{"billing_resource_quota_contract.sql", "invitation_rls_contract.sql", "instance_reference_contract.sql"} {
		contract, err := os.ReadFile(filepath.Join(migrationsPath, "..", "tests", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(contract)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	testTaskAttemptFencing(t, ctx, pool, databaseURL)
	testRLSTenantIsolation(t, ctx, pool)
}

// testRLSTenantIsolation exercises the runtime role (rather than the
// migration/table-owner role). It deliberately reuses the pool between
// transactions to catch session-setting leakage.
func testRLSTenantIsolation(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	createWorkspace := func(label string) string {
		var workspaceID, ownerID string
		if err := pool.QueryRow(ctx, `
			WITH a AS (
				INSERT INTO users(email,password_hash) VALUES(gen_random_uuid()::text || '@rls-' || $1::text || '.test','test') RETURNING id
			), ao AS (
				INSERT INTO organizations(name,slug,owner_user_id) SELECT 'RLS ' || $1::text,gen_random_uuid()::text,id FROM a RETURNING id,owner_user_id
			) INSERT INTO workspaces(organization_id,name,slug,created_by) SELECT id,'RLS ' || $1::text,gen_random_uuid()::text,owner_user_id FROM ao RETURNING id::text, created_by::text
		`, label).Scan(&workspaceID, &ownerID); err != nil {
			t.Fatal(err)
		}
		// The membership needs its own statement: the organization's AFTER
		// INSERT trigger provisions Free-plan entitlements only when the
		// statement above ends, and the team-member quota trigger (023)
		// rejects memberships of organizations without them.
		if _, err := pool.Exec(ctx, `
			INSERT INTO workspace_members(workspace_id,user_id,role_id)
			SELECT $1::uuid, $2::uuid, r.id FROM roles r WHERE r.code='owner'
		`, workspaceID, ownerID); err != nil {
			t.Fatal(err)
		}
		return workspaceID
	}
	workspaceA, workspaceB := createWorkspace("a"), createWorkspace("b")
	for _, workspaceID := range []string{workspaceA, workspaceB} {
		if _, err := pool.Exec(ctx, `INSERT INTO devices(workspace_id,device_key,name,platform,agent_version,capabilities,status) VALUES($1::uuid,gen_random_uuid()::text,'rls-device','linux','test','{}','offline')`, workspaceID); err != nil {
			t.Fatal(err)
		}
	}
	check := func(scope string, want int) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SET LOCAL ROLE ant_control_plane`); err != nil {
			t.Fatal(err)
		}
		if scope != "" {
			if _, err := tx.Exec(ctx, `SELECT set_config('app.current_workspace_id',$1,true)`, scope); err != nil {
				t.Fatal(err)
			}
		}
		var got int
		if err := tx.QueryRow(ctx, `SELECT count(*)::int FROM devices`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("scope %q: got %d devices, want %d", scope, got, want)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	check(workspaceA, 1)
	check(workspaceB, 1)
	check("", 0)
}

func testTaskAttemptFencing(t *testing.T, ctx context.Context, pool *pgxpool.Pool, databaseURL string) {
	t.Helper()
	var workspaceID string
	err := pool.QueryRow(ctx, `WITH actor AS (
	 INSERT INTO users(email,password_hash) VALUES(gen_random_uuid()::text || '@lease.test','test') RETURNING id
	), org AS (
	 INSERT INTO organizations(name,slug,owner_user_id) SELECT 'Lease',gen_random_uuid()::text,id FROM actor RETURNING id,owner_user_id
	) INSERT INTO workspaces(organization_id,name,slug,created_by)
	 SELECT id,'Lease',gen_random_uuid()::text,owner_user_id FROM org RETURNING id::text`).Scan(&workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	store, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, operation := range []string{"start", "complete", "fail"} {
		now := time.Now().UTC()
		_, err := store.CreateTask(ctx, taskservice.Task{ID: uuid.NewString(), WorkspaceID: workspaceID, TaskType: "system.healthcheck", IdempotencyKey: uuid.NewString(), Status: "queued", RetryLimit: 1, AvailableAt: now, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		first, err := store.ClaimNextTask(ctx, "same-worker", []string{"system.healthcheck"}, time.Second, now)
		if err != nil {
			t.Fatal(err)
		}
		apply := func(lease taskservice.Lease, at time.Time) error {
			switch operation {
			case "start":
				return store.StartTask(ctx, lease, at)
			case "complete":
				return store.CompleteTask(ctx, lease, nil, at)
			default:
				return store.FailTask(ctx, lease, "failure", "", false, at, at)
			}
		}
		if err := apply(first, now.Add(time.Second)); !errors.Is(err, taskservice.ErrStateConflict) {
			t.Fatalf("%s accepted expired lease: %v", operation, err)
		}
		second, err := store.ClaimNextTask(ctx, "same-worker", []string{"system.healthcheck"}, time.Minute, now.Add(2*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		at := now.Add(3 * time.Second)
		if err := apply(first, at); !errors.Is(err, taskservice.ErrStateConflict) {
			t.Fatalf("%s accepted old attempt: %v", operation, err)
		}
		forged := second
		forged.RunID = uuid.NewString()
		if err := apply(forged, at); !errors.Is(err, taskservice.ErrStateConflict) {
			t.Fatalf("%s accepted wrong run: %v", operation, err)
		}
		if err := apply(second, at); err != nil {
			t.Fatalf("%s rejected current attempt: %v", operation, err)
		}
		if operation == "start" {
			if err := store.CompleteTask(ctx, second, nil, at); err != nil {
				t.Fatal(err)
			}
		}
	}
}

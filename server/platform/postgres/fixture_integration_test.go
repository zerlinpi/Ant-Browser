package postgres_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

// integrationEnv is a migrated database with one fresh owner, organization
// and workspace, reachable through the table owner and both runtime roles.
type integrationEnv struct {
	Ctx context.Context
	// Admin and Pool connect as the table owner (migration role); use them
	// only to seed or inspect rows the runtime roles cannot see.
	Admin *postgres.Store
	Pool  *pgxpool.Pool
	// Store connects as ant_control_plane and Worker as ant_worker, both
	// subject to FORCE ROW LEVEL SECURITY.
	Store     *postgres.Store
	Worker    *postgres.Store
	WorkerURL string

	Owner        authservice.User
	Organization workspaceservice.Organization
	Workspace    workspaceservice.Workspace
	// Tenant is Ctx scoped to Workspace and Owner, as the gateway scopes
	// user requests.
	Tenant context.Context
}

// newIntegrationEnv skips the test unless ANT_TEST_DATABASE_URL names a
// disposable database. It applies every migration and gives the runtime
// roles known passwords, so never point it at a shared database.
func newIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()
	databaseURL := os.Getenv("ANT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ANT_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	migrationsPath, err := filepath.Abs(filepath.Join("..", "..", "..", "database", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, databaseURL, migrationsPath); err != nil {
		t.Fatal(err)
	}
	env := &integrationEnv{Ctx: ctx}
	if env.Admin, err = postgres.Open(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Admin.Close() })
	if env.Pool, err = pgxpool.New(ctx, databaseURL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(env.Pool.Close)

	roleURL := func(role, password string) string {
		t.Helper()
		if _, err := env.Pool.Exec(ctx, `ALTER ROLE `+role+` WITH LOGIN PASSWORD '`+password+`'`); err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		parsed.User = url.UserPassword(role, password)
		return parsed.String()
	}
	if env.Store, err = postgres.Open(ctx, roleURL("ant_control_plane", "integration-control-plane")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Store.Close() })
	env.WorkerURL = roleURL("ant_worker", "integration-worker")
	if env.Worker, err = postgres.Open(ctx, env.WorkerURL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Worker.Close() })

	now := time.Now().UTC().Truncate(time.Microsecond)
	env.Owner = authservice.User{
		ID: uuid.NewString(), Email: uuid.NewString() + "@integration.test", PasswordHash: "test",
		DisplayName: "Owner", Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := env.Admin.CreateUser(ctx, env.Owner); err != nil {
		t.Fatal(err)
	}
	suffix := uuid.NewString()[:8]
	env.Organization = workspaceservice.Organization{ID: uuid.NewString(), Name: "Integration", Slug: "org-" + suffix, Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	env.Workspace = workspaceservice.Workspace{ID: uuid.NewString(), OrganizationID: env.Organization.ID, Name: "Integration", Slug: "ws-" + suffix, Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := env.Admin.CreateOrganizationWorkspace(ctx, env.Organization, env.Workspace, workspaceservice.Membership{
		ID: uuid.NewString(), WorkspaceID: env.Workspace.ID, UserID: env.Owner.ID, Role: memberservice.RoleOwner, Status: "active", JoinedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	env.Tenant = postgres.WithTenantScope(ctx, postgres.TenantScope{WorkspaceID: env.Workspace.ID, UserID: env.Owner.ID})
	return env
}

// NewUser creates another active user as the table owner.
func (env *integrationEnv) NewUser(t *testing.T, passwordHash string) authservice.User {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	user := authservice.User{
		ID: uuid.NewString(), Email: uuid.NewString() + "@integration.test", PasswordHash: passwordHash,
		DisplayName: "User", Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := env.Admin.CreateUser(env.Ctx, user); err != nil {
		t.Fatal(err)
	}
	return user
}

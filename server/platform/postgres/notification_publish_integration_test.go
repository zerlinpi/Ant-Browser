package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
)

// Migration 032: CreateNotification goes through publish_notification for
// both runtime roles. ant_worker can publish but has no privilege on the
// notification tables, and the tenant policies still apply inside the
// function.
func TestNotificationPublishAgainstPostgres(t *testing.T) {
	env := newIntegrationEnv(t)
	scoped := postgres.WithTenantScope(env.Ctx, postgres.TenantScope{WorkspaceID: env.Workspace.ID})
	deliveries := func(t *testing.T, notificationID string) map[string]string {
		t.Helper()
		rows, err := env.Pool.Query(env.Ctx, `SELECT channel, status FROM notification_deliveries WHERE workspace_id = $1::uuid AND notification_id = $2::uuid`, env.Workspace.ID, notificationID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		channels := map[string]string{}
		for rows.Next() {
			var channel, status string
			if err := rows.Scan(&channel, &status); err != nil {
				t.Fatal(err)
			}
			channels[channel] = status
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return channels
	}

	for _, runtime := range []struct {
		role  string
		store *postgres.Store
	}{{"ant_control_plane", env.Store}, {"ant_worker", env.Worker}} {
		t.Run(runtime.role, func(t *testing.T) {
			service := notificationservice.New(runtime.store, nil)
			input := notificationservice.CreateInput{
				WorkspaceID: env.Workspace.ID, RecipientUserID: env.Owner.ID, EventType: "task.failed",
				Title: "Task execution failed", Body: "Review the task center.",
				Payload:        map[string]interface{}{"taskId": uuid.NewString(), "attempt": 2},
				IdempotencyKey: "integration:" + uuid.NewString(),
			}
			first, err := service.Publish(scoped, input)
			if err != nil {
				t.Fatal(err)
			}
			if first.WorkspaceID != env.Workspace.ID || first.RecipientUserID != env.Owner.ID || first.Payload["attempt"] != float64(2) || first.ReadAt != nil {
				t.Fatalf("published=%+v", first)
			}
			if got := deliveries(t, first.ID); len(got) != 2 || got["in_app"] != "sent" || got["websocket"] != "pending" {
				t.Fatalf("deliveries=%v", got)
			}
			replay, err := service.Publish(scoped, input)
			if err != nil || replay.ID != first.ID || !replay.CreatedAt.Equal(first.CreatedAt) {
				t.Fatalf("replay=%+v err=%v, want notification %s", replay, err, first.ID)
			}
			if got := deliveries(t, first.ID); len(got) != 2 {
				t.Fatalf("a replay added deliveries: %v", got)
			}
			changed := input
			changed.Body = "A different body"
			if _, err := service.Publish(scoped, changed); !errors.Is(err, notificationservice.ErrConflict) {
				t.Fatalf("conflicting replay error=%v", err)
			}
			// The recipient must be a member of the workspace.
			stranger := input
			stranger.RecipientUserID, stranger.IdempotencyKey = uuid.NewString(), "integration:"+uuid.NewString()
			if _, err := service.Publish(scoped, stranger); !errors.Is(err, notificationservice.ErrNotFound) {
				t.Fatalf("publish to a non-member error=%v", err)
			}
			// A transaction scoped to another workspace cannot write this one's rows.
			elsewhere := postgres.WithTenantScope(env.Ctx, postgres.TenantScope{WorkspaceID: uuid.NewString()})
			other := input
			other.IdempotencyKey = "integration:" + uuid.NewString()
			var pgError *pgconn.PgError
			if _, err := service.Publish(elsewhere, other); !errors.As(err, &pgError) || pgError.Code != "42501" {
				t.Fatalf("publish outside the tenant scope error=%v", err)
			}
		})
	}

	t.Run("preferences choose the channels", func(t *testing.T) {
		if _, err := env.Store.UpsertNotificationPreferences(env.Tenant, env.Workspace.ID, env.Owner.ID, []notificationservice.NotificationPreference{
			{Channel: "websocket", EventType: "*", Enabled: false},
			{Channel: "email", EventType: "proxy.health_failed", Enabled: true},
		}, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		published, err := notificationservice.New(env.Worker, nil).Publish(scoped, notificationservice.CreateInput{
			WorkspaceID: env.Workspace.ID, RecipientUserID: env.Owner.ID, EventType: "proxy.health_failed",
			Title: "Proxy health check failed", Body: "Review the proxy center.", IdempotencyKey: "integration:" + uuid.NewString(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := deliveries(t, published.ID); len(got) != 2 || got["in_app"] != "sent" || got["email"] != "pending" {
			t.Fatalf("deliveries=%v", got)
		}
	})

	t.Run("worker has no table access", func(t *testing.T) {
		workerPool, err := pgxpool.New(env.Ctx, env.WorkerURL)
		if err != nil {
			t.Fatal(err)
		}
		defer workerPool.Close()
		for _, statement := range []string{
			`SELECT count(*) FROM notifications`,
			`SELECT count(*) FROM notification_deliveries`,
			`SELECT count(*) FROM notification_preferences`,
			`UPDATE notifications SET read_at = now()`,
		} {
			var pgError *pgconn.PgError
			if _, err := workerPool.Exec(env.Ctx, statement); !errors.As(err, &pgError) || pgError.Code != "42501" {
				t.Errorf("%s as ant_worker: error=%v, want permission denied", statement, err)
			}
		}
	})
}

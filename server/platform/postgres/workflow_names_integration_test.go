package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

// Migration 031: workflow names are unique per workspace ignoring letter
// case, archived workflows included, and a violation of
// workflows_lower_name_uq maps to ErrNameConflict. Runs as ant_control_plane
// with the real workspace authorizer.
func TestWorkflowNamesIgnoreCaseAgainstPostgres(t *testing.T) {
	env := newIntegrationEnv(t)
	var legacyConstraint, index int
	if err := env.Pool.QueryRow(env.Ctx, `SELECT
		(SELECT count(*) FROM pg_constraint WHERE conname = 'workflows_workspace_id_name_key'),
		(SELECT count(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = 'workflows_lower_name_uq')`).Scan(&legacyConstraint, &index); err != nil {
		t.Fatal(err)
	}
	if legacyConstraint != 0 || index != 1 {
		t.Fatalf("legacy constraint count=%d, lower(name) index count=%d", legacyConstraint, index)
	}

	workflows := automationservice.New(env.Store, workspaceservice.New(env.Store))
	definition := automationservice.Definition{Engine: "playwright", Steps: []automationservice.Step{
		{ID: "open", Action: "navigate", Parameters: map[string]interface{}{"url": "https://example.com"}},
	}}
	create := func(ctx context.Context, workspaceID, name string) (automationservice.Workflow, error) {
		workflow, _, err := workflows.Create(ctx, env.Owner.ID, workspaceID, automationservice.CreateInput{Name: name, Definition: definition})
		return workflow, err
	}

	first, err := create(env.Tenant, env.Workspace.ID, "Nightly Sync")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"nightly sync", "NIGHTLY SYNC"} {
		if _, err := create(env.Tenant, env.Workspace.ID, name); !errors.Is(err, automationservice.ErrNameConflict) {
			t.Fatalf("%q: error=%v, want ErrNameConflict", name, err)
		}
	}
	if _, err := workflows.Archive(env.Tenant, env.Owner.ID, env.Workspace.ID, first.ID, automationservice.StateInput{ExpectedVersion: first.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err := create(env.Tenant, env.Workspace.ID, "Nightly sync"); !errors.Is(err, automationservice.ErrNameConflict) {
		t.Fatalf("archived workflow name reused: error=%v", err)
	}
	if _, err := create(env.Tenant, env.Workspace.ID, "Nightly Sync 2"); err != nil {
		t.Fatal(err)
	}

	// The same name in another workspace is independent.
	now := time.Now().UTC().Truncate(time.Microsecond)
	suffix := uuid.NewString()[:8]
	organization := workspaceservice.Organization{ID: uuid.NewString(), Name: "Other", Slug: "org-" + suffix, Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	other := workspaceservice.Workspace{ID: uuid.NewString(), OrganizationID: organization.ID, Name: "Other", Slug: "ws-" + suffix, Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := env.Admin.CreateOrganizationWorkspace(env.Ctx, organization, other, workspaceservice.Membership{
		ID: uuid.NewString(), WorkspaceID: other.ID, UserID: env.Owner.ID, Role: memberservice.RoleOwner, Status: "active", JoinedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	otherTenant := postgres.WithTenantScope(env.Ctx, postgres.TenantScope{WorkspaceID: other.ID, UserID: env.Owner.ID})
	if _, err := create(otherTenant, other.ID, "NIGHTLY SYNC"); err != nil {
		t.Fatalf("name taken across workspaces: %v", err)
	}
}

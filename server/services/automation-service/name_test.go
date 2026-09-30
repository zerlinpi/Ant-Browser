package automationservice_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var errNotAllowed = errors.New("not allowed")

type denyAuthorizer struct{}

func (denyAuthorizer) Require(context.Context, string, string, memberservice.Permission) error {
	return errNotAllowed
}

// The in-memory store mirrors workflows_lower_name_uq (migration 031): names
// are unique per workspace ignoring letter case, archived workflows included.
func TestWorkflowNamesAreUniqueIgnoringCaseIncludingArchived(t *testing.T) {
	t.Parallel()
	store := memory.New()
	service := automationservice.New(store, allowAuthorizer{})
	ctx := context.Background()
	definition := automationservice.Definition{Engine: "playwright", Steps: []automationservice.Step{
		{ID: "open", Action: "navigate", Parameters: map[string]interface{}{"url": "https://example.test"}},
	}}
	create := func(workspaceID, name string) (automationservice.Workflow, error) {
		workflow, _, err := service.Create(ctx, "actor", workspaceID, automationservice.CreateInput{Name: name, Definition: definition})
		return workflow, err
	}

	first, err := create("workspace-a", "Nightly Sync")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Nightly Sync", "nightly sync", "NIGHTLY SYNC", "  nIGHTLY sYNC  "} {
		if _, err := create("workspace-a", name); !errors.Is(err, automationservice.ErrNameConflict) {
			t.Fatalf("%q: error=%v, want ErrNameConflict", name, err)
		}
	}
	if _, err := service.Archive(ctx, "actor", "workspace-a", first.ID, automationservice.StateInput{ExpectedVersion: first.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err := create("workspace-a", "NIGHTLY sync"); !errors.Is(err, automationservice.ErrNameConflict) {
		t.Fatalf("archived workflow name reused: error=%v", err)
	}
	if _, err := create("workspace-b", "nightly sync"); err != nil {
		t.Fatalf("name taken in another workspace: %v", err)
	}
	if _, err := create("workspace-a", "Nightly Sync 2"); err != nil {
		t.Fatal(err)
	}

	// Authorization comes first, so a caller without workflow.manage learns
	// nothing about existing names.
	denied := automationservice.New(store, denyAuthorizer{})
	if _, _, err := denied.Create(ctx, "stranger", "workspace-a", automationservice.CreateInput{Name: "nightly sync", Definition: definition}); !errors.Is(err, errNotAllowed) {
		t.Fatalf("unauthorized create error=%v", err)
	}
}

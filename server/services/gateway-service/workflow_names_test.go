package gatewayservice_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
)

// Workflow names are unique per workspace ignoring letter case, archived
// workflows included (migration 031), and conflicts are 409.
func TestWorkflowNamesAreUniqueIgnoringCaseIncludingArchived(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	token, base := newConflictWorkspace(t, handler, "workflow-names@example.test")
	definition := map[string]interface{}{
		"engine": "playwright",
		"steps":  []map[string]interface{}{{"id": "open", "action": "navigate", "parameters": map[string]interface{}{"url": "https://example.test"}}},
	}
	create := func(accessToken, workspaceBase, name string) *httptest.ResponseRecorder {
		return perform(t, handler, http.MethodPost, workspaceBase+"/workflows", accessToken, "", map[string]interface{}{"name": name, "definition": definition})
	}
	first := create(token, base, "Nightly Sync")
	assertStatus(t, first, http.StatusCreated)
	created := decodeData[struct {
		Workflow automationservice.Workflow `json:"workflow"`
	}](t, first)
	assertErrorCode(t, create(token, base, "nightly sync"), http.StatusConflict, "workflow_name_conflict")
	assertErrorCode(t, create(token, base, "NIGHTLY SYNC"), http.StatusConflict, "workflow_name_conflict")

	archive := perform(t, handler, http.MethodPost, base+"/workflows/"+created.Workflow.ID+"/archive", token, "", map[string]interface{}{
		"expectedVersion": created.Workflow.Version,
	})
	assertStatus(t, archive, http.StatusOK)
	assertErrorCode(t, create(token, base, "Nightly sync"), http.StatusConflict, "workflow_name_conflict")

	// Another workspace may use the name; a non-member is refused before any
	// name comparison.
	otherToken, otherBase := newConflictWorkspace(t, handler, "workflow-names-other@example.test")
	assertStatus(t, create(otherToken, otherBase, "NIGHTLY SYNC"), http.StatusCreated)
	assertStatus(t, create(otherToken, base, "nightly sync"), http.StatusForbidden)
}

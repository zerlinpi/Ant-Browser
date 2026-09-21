package gatewayservice

import (
	"net/http"
	"strconv"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
)

func (g *Gateway) listWorkflows(w http.ResponseWriter, r *http.Request) {
	items, err := g.workflows.List(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var input automationservice.CreateInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	workflow, version, err := g.workflows.Create(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"data": map[string]interface{}{"workflow": workflow, "version": version},
	})
}

func (g *Gateway) getWorkflow(w http.ResponseWriter, r *http.Request) {
	workflow, err := g.workflows.Get(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("workflowID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": workflow})
}

func (g *Gateway) addWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	var input automationservice.AddVersionInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	workflow, version, err := g.workflows.AddVersion(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("workflowID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"data": map[string]interface{}{"workflow": workflow, "version": version},
	})
}

func (g *Gateway) getWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		httpx.WriteError(w, r, httpx.Problem{
			Status: http.StatusUnprocessableEntity, Code: "validation_failed",
			Message: "Workflow version must be a positive integer",
		})
		return
	}
	value, err := g.workflows.Version(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("workflowID"), version,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": value})
}

func (g *Gateway) publishWorkflow(w http.ResponseWriter, r *http.Request) {
	var input automationservice.StateInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	workflow, err := g.workflows.Publish(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("workflowID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": workflow})
}

func (g *Gateway) archiveWorkflow(w http.ResponseWriter, r *http.Request) {
	var input automationservice.StateInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	workflow, err := g.workflows.Archive(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("workflowID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": workflow})
}

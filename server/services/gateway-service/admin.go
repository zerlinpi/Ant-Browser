package gatewayservice

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	adminservice "github.com/zerlinpi/Ant-Browser/server/services/admin-service"
)

func (g *Gateway) adminListUsers(w http.ResponseWriter, r *http.Request) {
	options, err := adminListOptions(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	result, err := g.admin.ListUsers(adminRequestContext(r), mustPrincipal(r.Context()).UserID, options)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

func (g *Gateway) adminListOrganizations(w http.ResponseWriter, r *http.Request) {
	options, err := adminListOptions(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	result, err := g.admin.ListOrganizations(adminRequestContext(r), mustPrincipal(r.Context()).UserID, options)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

func (g *Gateway) adminListWorkspaces(w http.ResponseWriter, r *http.Request) {
	options, err := adminListOptions(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	result, err := g.admin.ListWorkspaces(adminRequestContext(r), mustPrincipal(r.Context()).UserID, options)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

func (g *Gateway) adminSetUserStatus(w http.ResponseWriter, r *http.Request) {
	targetID, err := adminUUIDPath(r, "userID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var input struct {
		Status string `json:"status"`
		Reason string `json:"reason,omitempty"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if len([]rune(strings.TrimSpace(input.Reason))) > 500 {
		httpx.WriteError(w, r, adminProblem("reason must be at most 500 characters"))
		return
	}
	result, err := g.admin.SetUserStatus(adminRequestContext(r), mustPrincipal(r.Context()).UserID, targetID, input.Status, input.Reason)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

func (g *Gateway) adminSetOrganizationStatus(w http.ResponseWriter, r *http.Request) {
	targetID, err := adminUUIDPath(r, "organizationID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var input struct {
		Status string `json:"status"`
		Reason string `json:"reason,omitempty"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if len([]rune(strings.TrimSpace(input.Reason))) > 500 {
		httpx.WriteError(w, r, adminProblem("reason must be at most 500 characters"))
		return
	}
	result, err := g.admin.SetOrganizationStatus(adminRequestContext(r), mustPrincipal(r.Context()).UserID, targetID, input.Status, input.Reason)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

func (g *Gateway) adminGrantPlatformAdmin(w http.ResponseWriter, r *http.Request) {
	targetID, err := adminUUIDPath(r, "userID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	var input struct {
		Role string `json:"role"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	result, err := g.admin.GrantPlatformAdmin(adminRequestContext(r), mustPrincipal(r.Context()).UserID, targetID, input.Role)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

func (g *Gateway) adminRevokePlatformAdmin(w http.ResponseWriter, r *http.Request) {
	targetID, err := adminUUIDPath(r, "userID")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := g.admin.RevokePlatformAdmin(adminRequestContext(r), mustPrincipal(r.Context()).UserID, targetID); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func adminRequestContext(r *http.Request) context.Context {
	return adminservice.WithRequestID(r.Context(), httpx.RequestID(r.Context()))
}

func adminListOptions(r *http.Request) (adminservice.ListOptions, error) {
	options := adminservice.ListOptions{Query: strings.TrimSpace(r.URL.Query().Get("query")), Status: strings.TrimSpace(r.URL.Query().Get("status"))}
	if len([]rune(options.Query)) > 200 {
		return options, adminProblem("query must be at most 200 characters")
	}
	if len(options.Status) > 32 {
		return options, adminProblem("status is invalid")
	}
	for name, destination := range map[string]*int{"limit": &options.Limit, "offset": &options.Offset} {
		raw := strings.TrimSpace(r.URL.Query().Get(name))
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || (name == "limit" && (value < 1 || value > 100)) {
			return options, adminProblem(name + " is outside the supported range")
		}
		*destination = value
	}
	return options, nil
}

func adminUUIDPath(r *http.Request, name string) (string, error) {
	value := strings.TrimSpace(r.PathValue(name))
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", adminProblem(name + " must be a UUID")
	}
	return parsed.String(), nil
}

func adminProblem(message string) httpx.Problem {
	return httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: message}
}

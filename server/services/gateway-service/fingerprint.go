package gatewayservice

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	fingerprintservice "github.com/zerlinpi/Ant-Browser/server/services/fingerprint-service"
)

func (g *Gateway) listFingerprintTemplates(w http.ResponseWriter, r *http.Request) {
	items, err := g.fingerprints.List(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) createFingerprintTemplate(w http.ResponseWriter, r *http.Request) {
	var input fingerprintservice.CreateInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	template, err := g.fingerprints.Create(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": template})
}

func (g *Gateway) getFingerprintTemplate(w http.ResponseWriter, r *http.Request) {
	template, err := g.fingerprints.Get(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("templateID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": template})
}

func (g *Gateway) updateFingerprintTemplate(w http.ResponseWriter, r *http.Request) {
	var input fingerprintservice.UpdateInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	template, err := g.fingerprints.Update(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("templateID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": template})
}

func (g *Gateway) deleteFingerprintTemplate(w http.ResponseWriter, r *http.Request) {
	version, err := versionPrecondition(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := g.fingerprints.Delete(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("templateID"), version,
	); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func versionPrecondition(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw != "" {
		raw = strings.Trim(raw, `"`)
	} else {
		raw = strings.TrimSpace(r.URL.Query().Get("version"))
	}
	version, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || version <= 0 {
		return 0, httpx.Problem{
			Status: http.StatusPreconditionRequired, Code: "version_precondition_required",
			Message: "If-Match with the current version is required",
		}
	}
	return version, nil
}

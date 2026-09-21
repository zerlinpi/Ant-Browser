package gatewayservice

import (
	"net/http"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	profilesyncservice "github.com/zerlinpi/Ant-Browser/server/services/profile-sync-service"
)

func (g *Gateway) listProfiles(w http.ResponseWriter, r *http.Request) {
	items, err := g.profiles.ListProfiles(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) createProfile(w http.ResponseWriter, r *http.Request) {
	var input profilesyncservice.CreateProfileInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	profile, err := g.profiles.CreateProfile(
		r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": profile})
}

func (g *Gateway) getProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := g.profiles.GetProfile(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": profile})
}

func (g *Gateway) acquireProfileLease(w http.ResponseWriter, r *http.Request) {
	var input profilesyncservice.AcquireLeaseInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	grant, err := g.profiles.AcquireLease(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": grant})
}

func (g *Gateway) renewProfileLease(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DeviceID   string `json:"deviceId"`
		Token      string `json:"token"`
		TTLSeconds int    `json:"ttlSeconds,omitempty"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	ttl := 10 * time.Minute
	if input.TTLSeconds != 0 {
		ttl = time.Duration(input.TTLSeconds) * time.Second
	}
	lease, err := g.profiles.RenewLease(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"),
		profilesyncservice.LeaseTokenInput{DeviceID: input.DeviceID, Token: input.Token}, ttl,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": lease})
}

func (g *Gateway) releaseProfileLease(w http.ResponseWriter, r *http.Request) {
	var input profilesyncservice.LeaseTokenInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := g.profiles.ReleaseLease(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"), input,
	); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) beginProfileRevision(w http.ResponseWriter, r *http.Request) {
	var input profilesyncservice.BeginRevisionInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	plan, err := g.profiles.BeginRevision(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	if plan.Conflict != nil {
		httpx.WriteJSON(w, http.StatusConflict, map[string]interface{}{
			"data": plan,
			"error": httpx.APIError{
				Code: "profile_revision_conflict", Message: "The cloud profile changed after the requested base revision",
				Details: map[string]string{"conflictId": plan.Conflict.ID}, TraceID: httpx.RequestID(r.Context()),
			},
		})
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": plan})
}

func (g *Gateway) prepareProfileObjectUpload(w http.ResponseWriter, r *http.Request) {
	var input profilesyncservice.LeaseTokenInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	grant, err := g.profiles.PrepareObjectUpload(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"), r.PathValue("revisionID"),
		r.PathValue("objectID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": grant})
}

func (g *Gateway) getProfileRevision(w http.ResponseWriter, r *http.Request) {
	plan, err := g.profiles.GetRevision(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"), r.PathValue("revisionID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": plan})
}

func (g *Gateway) prepareProfileObjectDownload(w http.ResponseWriter, r *http.Request) {
	grant, err := g.profiles.PrepareObjectDownload(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"), r.PathValue("revisionID"),
		r.PathValue("objectID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": grant})
}

func (g *Gateway) commitProfileRevision(w http.ResponseWriter, r *http.Request) {
	var input profilesyncservice.LeaseTokenInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	profile, revision, err := g.profiles.CommitRevision(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"), r.PathValue("revisionID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"data": map[string]interface{}{"profile": profile, "revision": revision},
	})
}

func (g *Gateway) restoreProfileRevision(w http.ResponseWriter, r *http.Request) {
	var input profilesyncservice.LeaseTokenInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	profile, revision, err := g.profiles.RestoreRevision(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"), r.PathValue("revisionID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"data": map[string]interface{}{"profile": profile, "revision": revision},
	})
}

func (g *Gateway) listProfileRevisions(w http.ResponseWriter, r *http.Request) {
	items, err := g.profiles.Revisions(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) listProfileConflicts(w http.ResponseWriter, r *http.Request) {
	items, err := g.profiles.Conflicts(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) resolveProfileConflict(w http.ResponseWriter, r *http.Request) {
	var input profilesyncservice.ResolveConflictInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	conflict, err := g.profiles.ResolveConflict(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("profileID"), r.PathValue("conflictID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": conflict})
}

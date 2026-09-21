package gatewayservice

import (
	"net/http"
	"strings"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
)

func (g *Gateway) listBillingPlans(w http.ResponseWriter, r *http.Request) {
	items, err := g.billing.ListPlans(r.Context())
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) listReleaseChannels(w http.ResponseWriter, r *http.Request) {
	items, err := g.billing.ListReleaseChannels(r.Context())
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) currentSubscription(w http.ResponseWriter, r *http.Request) {
	item, err := g.billing.CurrentSubscriptionFor(r.Context(), mustPrincipal(r.Context()).UserID, billingOrganizationID(r))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": item})
}

func (g *Gateway) listEntitlements(w http.ResponseWriter, r *http.Request) {
	items, err := g.billing.EntitlementsFor(r.Context(), mustPrincipal(r.Context()).UserID, billingOrganizationID(r))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) activateLicense(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkspaceID       string            `json:"workspaceId"`
		DeviceID          string            `json:"deviceId"`
		Token             string            `json:"token"`
		ExpiresAt         *time.Time        `json:"expiresAt"`
		OfflineGraceUntil *time.Time        `json:"offlineGraceUntil"`
		ReleaseChannelID  string            `json:"releaseChannelId"`
		Metadata          map[string]string `json:"metadata"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := g.billing.ActivateLicenseFor(
		r.Context(), mustPrincipal(r.Context()).UserID, billingOrganizationID(r),
		input.WorkspaceID, input.DeviceID, input.Token, input.ExpiresAt,
		input.OfflineGraceUntil, input.ReleaseChannelID, input.Metadata,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": item})
}

func (g *Gateway) validateLicense(w http.ResponseWriter, r *http.Request) {
	var input struct {
		WorkspaceID string `json:"workspaceId"`
		DeviceID    string `json:"deviceId"`
		Token       string `json:"token"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := g.billing.ValidateLicenseFor(
		r.Context(), mustPrincipal(r.Context()).UserID, billingOrganizationID(r),
		input.WorkspaceID, input.DeviceID, input.Token, time.Now().UTC(),
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": item})
}

func (g *Gateway) revokeLicense(w http.ResponseWriter, r *http.Request) {
	if err := g.billing.RevokeLicenseFor(
		r.Context(), mustPrincipal(r.Context()).UserID, billingOrganizationID(r),
		strings.TrimSpace(r.PathValue("activationID")), time.Now().UTC(),
	); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func billingOrganizationID(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("organizationID"))
}

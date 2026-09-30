package gatewayservice

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
)

// verifyMFA is the second login step for accounts with two-factor
// authentication. It is public: the challenge token from /auth/login is the
// credential, and the route is rate limited per client IP.
func (g *Gateway) verifyMFA(w http.ResponseWriter, r *http.Request) {
	var input authservice.MFAVerifyInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	metadata := g.requestMetadata(r)
	pair, err := g.auth.VerifyMFA(r.Context(), input, metadata)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	method := authservice.MFAMethodTOTP
	if strings.TrimSpace(input.RecoveryCode) != "" {
		method = authservice.MFAMethodRecoveryCode
	}
	g.publishLoginSecurityEvents(r.Context(), pair, metadata, method)
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": pair})
}

func (g *Gateway) getMFAStatus(w http.ResponseWriter, r *http.Request) {
	status, err := g.auth.MFAStatus(r.Context(), mustPrincipal(r.Context()).UserID)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": status})
}

func (g *Gateway) setupTOTP(w http.ResponseWriter, r *http.Request) {
	identity := mustPrincipal(r.Context())
	if !allowRequest(w, r, g.limits.mfaManageUser, identity.UserID) {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	setup, err := g.auth.SetupTOTP(r.Context(), identity.UserID, input.Password)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": setup})
}

func (g *Gateway) confirmTOTP(w http.ResponseWriter, r *http.Request) {
	identity := mustPrincipal(r.Context())
	if !allowRequest(w, r, g.limits.mfaManageUser, identity.UserID) {
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	codes, err := g.auth.ConfirmTOTP(r.Context(), identity.UserID, input.Code)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	g.publishMFASecurityEvent(r.Context(), identity, "Two-factor authentication enabled",
		"Sign-ins to your Ant Browser account now require a verification code.", "mfa_enabled")
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": codes})
}

func (g *Gateway) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	identity := mustPrincipal(r.Context())
	if !allowRequest(w, r, g.limits.mfaManageUser, identity.UserID) {
		return
	}
	var input authservice.MFACodeInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	codes, err := g.auth.RegenerateRecoveryCodes(r.Context(), identity.UserID, input)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	g.publishMFASecurityEvent(r.Context(), identity, "Recovery codes regenerated",
		"New two-factor recovery codes were created; the previous codes no longer work.", "mfa_recovery_codes_regenerated")
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": codes})
}

func (g *Gateway) disableMFA(w http.ResponseWriter, r *http.Request) {
	identity := mustPrincipal(r.Context())
	if !allowRequest(w, r, g.limits.mfaManageUser, identity.UserID) {
		return
	}
	var input authservice.DisableMFAInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := g.auth.DisableMFA(r.Context(), identity.UserID, input); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	g.publishMFASecurityEvent(r.Context(), identity, "Two-factor authentication disabled",
		"Sign-ins to your Ant Browser account no longer require a verification code.", "mfa_disabled")
	w.WriteHeader(http.StatusNoContent)
}

// publishMFASecurityEvent records a change to the account's second factor.
func (g *Gateway) publishMFASecurityEvent(ctx context.Context, identity principal, title, body, action string) {
	g.publishSecurityEvent(ctx, identity.UserID, title, body, map[string]interface{}{
		"action": action, "sessionId": identity.SessionID,
	}, "security-"+action+":"+uuid.NewString())
}

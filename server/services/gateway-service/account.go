package gatewayservice

import (
	"net/http"
	"strings"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
)

func (g *Gateway) listAccounts(w http.ResponseWriter, r *http.Request) {
	items, err := g.accounts.List(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) createAccount(w http.ResponseWriter, r *http.Request) {
	var input accountservice.CreateInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := g.accounts.Create(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), input)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": item})
}

func (g *Gateway) getAccount(w http.ResponseWriter, r *http.Request) {
	item, err := g.accounts.Get(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("accountID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": item})
}

type accountVersionInput struct {
	ExpectedVersion int64 `json:"expectedVersion"`
}

func (g *Gateway) updateAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		accountservice.UpdateInput
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := g.accounts.Update(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("accountID"), input.ExpectedVersion, input.UpdateInput)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": item})
}

func (g *Gateway) deleteAccount(w http.ResponseWriter, r *http.Request) {
	var input accountVersionInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := g.accounts.Delete(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("accountID"), input.ExpectedVersion); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) setAccountStatus(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Status          string `json:"status"`
		ExpectedVersion int64  `json:"expectedVersion"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := g.accounts.SetStatus(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("accountID"), input.Status, input.ExpectedVersion)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": item})
}

func (g *Gateway) setAccountRiskLevel(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RiskLevel       string `json:"riskLevel"`
		ExpectedVersion int64  `json:"expectedVersion"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := g.accounts.SetRiskLevel(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("accountID"), input.RiskLevel, input.ExpectedVersion)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": item})
}

// accountSecretResponse intentionally contains no envelope fields. In
// particular, ciphertext, encrypted DEKs, and key references never cross the
// HTTP boundary.
type accountSecretResponse struct {
	ID          string      `json:"id"`
	AccountID   string      `json:"accountId"`
	WorkspaceID string      `json:"workspaceId"`
	Kind        string      `json:"kind"`
	CreatedAt   interface{} `json:"createdAt"`
	UpdatedAt   interface{} `json:"updatedAt"`
	Version     int64       `json:"version"`
}

func safeSecret(secret accountservice.AccountSecret) accountSecretResponse {
	return accountSecretResponse{ID: secret.ID, AccountID: secret.AccountID, WorkspaceID: secret.WorkspaceID, Kind: secret.Kind, CreatedAt: secret.CreatedAt, UpdatedAt: secret.UpdatedAt, Version: secret.Version}
}

func (g *Gateway) listAccountSecrets(w http.ResponseWriter, r *http.Request) {
	items, err := g.accounts.ListSecrets(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("accountID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	result := make([]accountSecretResponse, 0, len(items))
	for _, item := range items {
		result = append(result, safeSecret(item))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

func (g *Gateway) storeAccountSecret(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	workspaceID, accountID := r.PathValue("workspaceID"), r.PathValue("accountID")
	// AAD is deterministic and scoped to the owning tenant, account, and kind.
	aad := []byte(strings.Join([]string{workspaceID, accountID, strings.TrimSpace(input.Kind)}, "/"))
	item, err := g.accounts.StoreSecret(r.Context(), mustPrincipal(r.Context()).UserID, workspaceID, accountID, input.Kind, []byte(input.Value), aad)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": safeSecret(item)})
}

func (g *Gateway) deleteAccountSecret(w http.ResponseWriter, r *http.Request) {
	if err := g.accounts.DeleteSecret(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("accountID"), r.PathValue("secretID")); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) listAccountBindings(w http.ResponseWriter, r *http.Request) {
	items, err := g.accounts.ListBindings(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("accountID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) bindAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		BindingType string `json:"bindingType"`
		TargetID    string `json:"targetId"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := g.accounts.Bind(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("accountID"), input.BindingType, input.TargetID)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": item})
}

func (g *Gateway) unbindAccount(w http.ResponseWriter, r *http.Request) {
	if err := g.accounts.Unbind(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("accountID"), r.PathValue("bindingID")); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) listAccountRiskEvents(w http.ResponseWriter, r *http.Request) {
	items, err := g.accounts.ListRiskEvents(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("accountID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) recordAccountRiskEvent(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Level       string `json:"level"`
		Code        string `json:"code"`
		Description string `json:"description"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := g.accounts.RecordRiskEvent(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("accountID"), input.Level, input.Code, input.Description)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	if g.notifications != nil {
		if _, notifyErr := g.notifications.Publish(r.Context(), notificationservice.CreateInput{
			WorkspaceID: item.WorkspaceID, RecipientUserID: mustPrincipal(r.Context()).UserID,
			EventType: "account.risk_event", Title: "Account risk event",
			Body: input.Code + ": " + input.Description,
			Payload: map[string]interface{}{"accountId": item.AccountID, "level": item.Level, "code": item.Code},
			IdempotencyKey: "risk-event:" + item.ID,
		}); notifyErr != nil {
			g.logger.WarnContext(r.Context(), "risk_notification_publish_failed", "risk_event_id", item.ID, "error", notifyErr)
		}
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": item})
}

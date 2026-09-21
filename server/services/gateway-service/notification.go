package gatewayservice

import (
	"net/http"
	"strconv"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
)

func (g *Gateway) listNotifications(w http.ResponseWriter, r *http.Request) {
	workspaceID := notificationWorkspaceID(r)
	if workspaceID == "" {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "workspaceId is required"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	page, err := g.notifications.List(r.Context(), mustPrincipal(r.Context()).UserID, workspaceID, notificationservice.ListInput{
		Limit: limit, Offset: offset, UnreadOnly: r.URL.Query().Get("unreadOnly") == "true",
	})
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": page})
}

func (g *Gateway) unreadNotificationCount(w http.ResponseWriter, r *http.Request) {
	workspaceID := notificationWorkspaceID(r)
	if workspaceID == "" {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "workspaceId is required"})
		return
	}
	count, err := g.notifications.UnreadCount(r.Context(), mustPrincipal(r.Context()).UserID, workspaceID)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": map[string]int{"unreadCount": count}})
}

func (g *Gateway) markNotificationRead(w http.ResponseWriter, r *http.Request) {
	workspaceID := notificationWorkspaceID(r)
	if workspaceID == "" {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "workspaceId is required"})
		return
	}
	item, err := g.notifications.MarkRead(r.Context(), mustPrincipal(r.Context()).UserID, workspaceID, r.PathValue("notificationID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": item})
}

func (g *Gateway) markAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	workspaceID := notificationWorkspaceID(r)
	if workspaceID == "" {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "workspaceId is required"})
		return
	}
	count, err := g.notifications.MarkAllRead(r.Context(), mustPrincipal(r.Context()).UserID, workspaceID)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": map[string]int{"markedCount": count}})
}

func (g *Gateway) getNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	workspaceID := notificationWorkspaceID(r)
	if workspaceID == "" {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "workspaceId is required"})
		return
	}
	items, err := g.notifications.GetPreferences(r.Context(), mustPrincipal(r.Context()).UserID, workspaceID)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) updateNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	workspaceID := notificationWorkspaceID(r)
	if workspaceID == "" {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "workspaceId is required"})
		return
	}
	var input struct {
		Preferences []notificationservice.NotificationPreference `json:"preferences"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	items, err := g.notifications.UpdatePreferences(r.Context(), mustPrincipal(r.Context()).UserID, workspaceID, input.Preferences)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func notificationWorkspaceID(r *http.Request) string {
	return r.PathValue("workspaceID")
}

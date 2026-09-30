package gatewayservice_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	"github.com/zerlinpi/Ant-Browser/server/platform/realtime"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
)

func TestLoginPublishesSecurityNotificationToUserWorkspaces(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := memory.New()
	bus := realtime.NewMemory()
	defer bus.Close()
	handler := newNotificationTestGateway(ctx, store, bus)

	registered := register(t, handler, "security-event@example.com")
	workspace := createTestWorkspace(t, handler, registered.AccessToken)
	loginResponse := perform(t, handler, http.MethodPost, "/api/v1/auth/login", "", "", map[string]string{
		"email": "security-event@example.com", "password": "SecurePassword123",
	})
	assertStatus(t, loginResponse, http.StatusOK)
	loggedIn := decodeData[authservice.TokenPair](t, loginResponse)

	listResponse := perform(t, handler, http.MethodGet, "/api/v1/workspaces/"+workspace.ID+"/notifications", loggedIn.AccessToken, "", nil)
	assertStatus(t, listResponse, http.StatusOK)
	page := decodeData[notificationservice.Page](t, listResponse)
	if len(page.Items) != 1 || page.Items[0].EventType != "security.event" || page.Items[0].RecipientUserID != registered.User.ID {
		t.Fatalf("unexpected notifications: %+v", page.Items)
	}
	if page.Items[0].Payload["sessionId"] != loggedIn.SessionID || page.UnreadCount != 1 {
		t.Fatalf("unexpected security notification payload/page: %+v", page)
	}
}

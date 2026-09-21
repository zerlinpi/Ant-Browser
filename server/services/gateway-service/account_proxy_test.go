package gatewayservice_test

import (
	"net/http"
	"strings"
	"testing"

	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

func TestAccountProxyTenantAndCredentialAPI(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	owner := register(t, handler, "asset-owner@example.test")
	created := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Assets"})
	assertStatus(t, created, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, created)
	base := "/api/v1/workspaces/" + workspace.ID
	created = perform(t, handler, http.MethodPost, base+"/accounts", owner.AccessToken, "", map[string]string{
		"platform": "amazon", "name": "Seller US", "identifier": "seller-us", "status": "pending",
	})
	assertStatus(t, created, http.StatusCreated)
	account := decodeData[accountservice.Account](t, created)
	secret := perform(t, handler, http.MethodPost, base+"/accounts/"+account.ID+"/secrets", owner.AccessToken, "", map[string]string{
		"kind": "password", "value": "private-seller-password",
	})
	assertStatus(t, secret, http.StatusCreated)
	list := perform(t, handler, http.MethodGet, base+"/accounts/"+account.ID+"/secrets", owner.AccessToken, "", nil)
	assertStatus(t, list, http.StatusOK)
	for _, body := range []string{secret.Body.String(), list.Body.String()} {
		for _, forbidden := range []string{"private-seller-password", "ciphertext", "encryptedDek", "nonce"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("secret response exposes %s", forbidden)
			}
		}
	}
	created = perform(t, handler, http.MethodPost, base+"/proxies", owner.AccessToken, "", map[string]interface{}{
		"name": "US authenticated", "protocol": "socks5", "host": "proxy.example.test", "port": 1080,
		"connectorType": "xray", "secret": map[string]string{"password": "private-proxy-password"},
	})
	assertStatus(t, created, http.StatusCreated)
	proxy := decodeData[proxyservice.Proxy](t, created)
	if proxy.Kernel != proxyservice.KernelXray || !proxy.HasCredentials {
		t.Fatalf("unexpected authenticated route: %+v", proxy)
	}
	if strings.Contains(created.Body.String(), "secretRef") || strings.Contains(created.Body.String(), "private-proxy-password") {
		t.Fatal("proxy response leaked credential data")
	}
	requested := perform(t, handler, http.MethodPost, base+"/proxies/"+proxy.ID+"/health-checks", owner.AccessToken, "", nil)
	assertStatus(t, requested, http.StatusAccepted)
	check := decodeData[proxyservice.HealthCheck](t, requested)
	queued := perform(t, handler, http.MethodGet, base+"/tasks/"+check.RequestID, owner.AccessToken, "", nil)
	assertStatus(t, queued, http.StatusOK)
	task := decodeData[taskservice.Task](t, queued)
	if task.TaskType != "proxy.health_check" || task.Status != "queued" || task.Payload["checkId"] != check.ID || task.Payload["connectorType"] != "xray" || task.Payload["kernel"] != "xray" {
		t.Fatalf("health task lost routing or identity: %+v", task)
	}
	if strings.Contains(queued.Body.String(), "private-proxy-password") || strings.Contains(queued.Body.String(), "secretRef") {
		t.Fatal("queue exposed proxy credentials")
	}
	assertStatus(t, perform(t, handler, http.MethodPost, base+"/tasks/"+check.RequestID+"/cancel", owner.AccessToken, "", nil), http.StatusOK)
	cancelled := perform(t, handler, http.MethodGet, base+"/proxy-health-checks/"+check.ID, owner.AccessToken, "", nil)
	assertStatus(t, cancelled, http.StatusOK)
	finished := decodeData[proxyservice.HealthCheck](t, cancelled)
	if finished.Status != "cancelled" || finished.CompletedAt == nil {
		t.Fatalf("health check not cancelled: %+v", finished)
	}
	stranger := register(t, handler, "asset-stranger@example.test")
	otherCreated := perform(t, handler, http.MethodPost, "/api/v1/workspaces", stranger.AccessToken, "", map[string]string{"name": "Other assets"})
	assertStatus(t, otherCreated, http.StatusCreated)
	other := decodeData[workspaceservice.Workspace](t, otherCreated)
	foreignAccount := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+other.ID+"/accounts", stranger.AccessToken, "", map[string]string{
		"platform": "amazon", "name": "Foreign seller", "identifier": "foreign-seller",
	})
	assertStatus(t, foreignAccount, http.StatusCreated)
	foreign := decodeData[accountservice.Account](t, foreignAccount)
	assignmentPath := base + "/proxies/" + proxy.ID + "/assignments"
	assertStatus(t, perform(t, handler, http.MethodPost, assignmentPath, owner.AccessToken, "", map[string]string{
		"targetType": "account", "targetId": foreign.ID,
	}), http.StatusNotFound)
	assertStatus(t, perform(t, handler, http.MethodPost, assignmentPath, owner.AccessToken, "", map[string]string{
		"targetType": "account", "targetId": account.ID,
	}), http.StatusCreated)
	assertStatus(t, perform(t, handler, http.MethodDelete, base+"/proxies/"+proxy.ID+"?version=1", owner.AccessToken, "", nil), http.StatusConflict)
	assertStatus(t, perform(t, handler, http.MethodDelete, base+"/proxy-assignments/account/"+account.ID+"?version=1", owner.AccessToken, "", nil), http.StatusNoContent)
	assertStatus(t, perform(t, handler, http.MethodPost, assignmentPath, owner.AccessToken, "", map[string]string{
		"targetType": "account", "targetId": account.ID,
	}), http.StatusCreated)
	for _, path := range []string{base + "/accounts", base + "/proxies", base + "/accounts/" + account.ID + "/secrets"} {
		assertStatus(t, perform(t, handler, http.MethodGet, path, stranger.AccessToken, "", nil), http.StatusForbidden)
	}
}

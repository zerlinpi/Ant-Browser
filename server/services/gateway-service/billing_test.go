package gatewayservice_test

import (
	"net/http"
	"testing"
	"time"

	billingservice "github.com/zerlinpi/Ant-Browser/server/services/billing-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

func TestBillingGatewayCatalogTenantAuthorizationAndLicenseLifecycle(t *testing.T) {
	handler := newTestGateway()
	plansResponse := perform(t, handler, http.MethodGet, "/api/v1/billing/plans", "", "", nil)
	assertStatus(t, plansResponse, http.StatusOK)
	plans := decodeData[[]billingservice.Plan](t, plansResponse)
	if len(plans) != 3 || plans[0].Code != billingservice.PlanFree || plans[2].Code != billingservice.PlanEnterprise {
		t.Fatalf("unexpected plan catalog: %+v", plans)
	}

	owner := register(t, handler, "billing-owner@example.com")
	outsider := register(t, handler, "billing-outsider@example.com")
	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Billing Workspace"})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)
	base := "/api/v1/organizations/" + workspace.OrganizationID + "/billing"

	subscriptionResponse := perform(t, handler, http.MethodGet, base+"/subscription", owner.AccessToken, "", nil)
	assertStatus(t, subscriptionResponse, http.StatusOK)
	subscription := decodeData[billingservice.Subscription](t, subscriptionResponse)
	if subscription.OrganizationID != workspace.OrganizationID || subscription.Status != "active" {
		t.Fatalf("free subscription was not provisioned: %+v", subscription)
	}
	entitlementsResponse := perform(t, handler, http.MethodGet, base+"/entitlements", owner.AccessToken, "", nil)
	assertStatus(t, entitlementsResponse, http.StatusOK)
	entitlements := decodeData[[]billingservice.Entitlement](t, entitlementsResponse)
	if len(entitlements) < 6 {
		t.Fatalf("free entitlements were not provisioned: %+v", entitlements)
	}
	forbidden := perform(t, handler, http.MethodGet, base+"/subscription", outsider.AccessToken, "", nil)
	assertStatus(t, forbidden, http.StatusForbidden)

	device := createTestDevice(t, handler, owner.AccessToken, workspace.ID)
	token := "0123456789abcdef0123456789abcdef"
	expiresAt := time.Now().UTC().Add(time.Hour)
	activateResponse := perform(t, handler, http.MethodPost, base+"/licenses/activate", owner.AccessToken, "", map[string]interface{}{
		"workspaceId": workspace.ID, "deviceId": device.Data.Device.ID,
		"token": token, "expiresAt": expiresAt,
		"metadata": map[string]string{"clientVersion": "1.0.0"},
	})
	assertStatus(t, activateResponse, http.StatusCreated)
	activation := decodeData[billingservice.LicenseActivation](t, activateResponse)
	if activation.WorkspaceID != workspace.ID || activation.DeviceID != device.Data.Device.ID || activation.Status != "active" {
		t.Fatalf("unexpected activation: %+v", activation)
	}
	validateResponse := perform(t, handler, http.MethodPost, base+"/licenses/validate", owner.AccessToken, "", map[string]string{
		"workspaceId": workspace.ID, "deviceId": device.Data.Device.ID, "token": token,
	})
	assertStatus(t, validateResponse, http.StatusOK)

	revokeResponse := perform(t, handler, http.MethodDelete, base+"/licenses/"+activation.ID, owner.AccessToken, "", nil)
	assertStatus(t, revokeResponse, http.StatusNoContent)
	revokedResponse := perform(t, handler, http.MethodPost, base+"/licenses/validate", owner.AccessToken, "", map[string]string{
		"workspaceId": workspace.ID, "deviceId": device.Data.Device.ID, "token": token,
	})
	assertStatus(t, revokedResponse, http.StatusForbidden)
}

func TestFreePlanEnforcesOrganizationInstanceAndSeatLimits(t *testing.T) {
	handler := newTestGateway()
	owner := register(t, handler, "quota-owner@example.com")
	member := register(t, handler, "quota-member@example.com")
	extra := register(t, handler, "quota-extra@example.com")
	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Quota Workspace"})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)
	instancePath := "/api/v1/workspaces/" + workspace.ID + "/browser-instances"
	for index := 0; index < 3; index++ {
		response := perform(t, handler, http.MethodPost, instancePath, owner.AccessToken, "", map[string]interface{}{
			"name": "Quota Instance " + string(rune('A'+index)), "platform": "chromium",
		})
		assertStatus(t, response, http.StatusCreated)
	}
	deniedInstance := perform(t, handler, http.MethodPost, instancePath, owner.AccessToken, "", map[string]interface{}{
		"name": "Quota Instance D", "platform": "chromium",
	})
	assertStatus(t, deniedInstance, http.StatusPaymentRequired)

	membersPath := "/api/v1/workspaces/" + workspace.ID + "/members"
	added := perform(t, handler, http.MethodPost, membersPath, owner.AccessToken, "", map[string]string{"userId": member.User.ID, "role": "operator"})
	assertStatus(t, added, http.StatusCreated)
	deniedMember := perform(t, handler, http.MethodPost, membersPath, owner.AccessToken, "", map[string]string{"userId": extra.User.ID, "role": "viewer"})
	assertStatus(t, deniedMember, http.StatusPaymentRequired)
}

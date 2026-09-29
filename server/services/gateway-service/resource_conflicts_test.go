package gatewayservice_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
	batchservice "github.com/zerlinpi/Ant-Browser/server/services/batch-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

func assertErrorCode(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	assertStatus(t, response, status)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeResponse(t, response, &body)
	if body.Error.Code != code {
		t.Fatalf("error code %q, want %q; body=%s", body.Error.Code, code, response.Body.String())
	}
}

func newConflictWorkspace(t *testing.T, handler http.Handler, email string) (string, string) {
	t.Helper()
	owner := register(t, handler, email)
	created := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Conflicts"})
	assertStatus(t, created, http.StatusCreated)
	return owner.AccessToken, "/api/v1/workspaces/" + decodeData[workspaceservice.Workspace](t, created).ID
}

func TestInstanceNamesAreUniqueAmongLiveInstances(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	token, base := newConflictWorkspace(t, handler, "instance-names@example.test")
	create := func(name string) *httptest.ResponseRecorder {
		return perform(t, handler, http.MethodPost, base+"/browser-instances", token, "", map[string]interface{}{"name": name, "platform": "chromium"})
	}
	first := create("Shop 1")
	assertStatus(t, first, http.StatusCreated)
	original := decodeData[browserinstanceservice.BrowserInstance](t, first)
	assertErrorCode(t, create("shop 1"), http.StatusConflict, "instance_name_conflict")

	other := create("Shop 2")
	assertStatus(t, other, http.StatusCreated)
	second := decodeData[browserinstanceservice.BrowserInstance](t, other)
	assertErrorCode(t, perform(t, handler, http.MethodPatch, base+"/browser-instances/"+second.ID, token, "", map[string]interface{}{
		"expectedVersion": second.Version, "name": "SHOP 1",
	}), http.StatusConflict, "instance_name_conflict")
	assertErrorCode(t, perform(t, handler, http.MethodPost, base+"/browser-instances/"+second.ID+"/clone", token, "", map[string]interface{}{
		"name": "Shop 1",
	}), http.StatusConflict, "instance_name_conflict")
	// Renaming to a new spelling of its own name is not a conflict.
	assertStatus(t, perform(t, handler, http.MethodPatch, base+"/browser-instances/"+second.ID, token, "", map[string]interface{}{
		"expectedVersion": second.Version, "name": "SHOP 2",
	}), http.StatusOK)

	batch := perform(t, handler, http.MethodPost, base+"/batch/browser-instances", token, "batch-names", map[string]interface{}{
		"items": []map[string]interface{}{
			{"itemId": "row-1", "input": map[string]interface{}{"name": "Shop 3", "platform": "chromium"}},
			{"itemId": "row-2", "input": map[string]interface{}{"name": "shop 3", "platform": "chromium"}},
		},
	})
	assertStatus(t, batch, http.StatusMultiStatus)
	result := decodeData[batchservice.BatchResult[browserinstanceservice.BrowserInstance]](t, batch)
	if result.Items[0].Value == nil || result.Items[1].Error == nil || result.Items[1].Error.Code != batchservice.ErrorCodeNameConflict {
		t.Fatalf("batch duplicate result = %+v", result.Items)
	}

	// Deleting frees the name.
	assertStatus(t, perform(t, handler, http.MethodDelete, base+"/browser-instances/"+original.ID, token, "", map[string]interface{}{
		"expectedVersion": original.Version,
	}), http.StatusNoContent)
	assertStatus(t, create("Shop 1"), http.StatusCreated)
}

func TestAccountIdentifiersAreUniquePerPlatformAmongLiveAccounts(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	token, base := newConflictWorkspace(t, handler, "account-identifiers@example.test")
	create := func(platform, identifier string) *httptest.ResponseRecorder {
		return perform(t, handler, http.MethodPost, base+"/accounts", token, "", map[string]string{
			"platform": platform, "name": "Seller", "identifier": identifier,
		})
	}
	first := create("amazon", "seller@example.test")
	assertStatus(t, first, http.StatusCreated)
	original := decodeData[accountservice.Account](t, first)
	assertErrorCode(t, create("amazon", "Seller@Example.test"), http.StatusConflict, "account_identifier_conflict")
	assertStatus(t, create("ebay", "seller@example.test"), http.StatusCreated)

	other := create("amazon", "second@example.test")
	assertStatus(t, other, http.StatusCreated)
	second := decodeData[accountservice.Account](t, other)
	assertErrorCode(t, perform(t, handler, http.MethodPatch, base+"/accounts/"+second.ID, token, "", map[string]interface{}{
		"expectedVersion": second.Version, "identifier": "SELLER@example.test",
	}), http.StatusConflict, "account_identifier_conflict")

	assertStatus(t, perform(t, handler, http.MethodDelete, base+"/accounts/"+original.ID, token, "", map[string]interface{}{
		"expectedVersion": original.Version,
	}), http.StatusNoContent)
	assertStatus(t, create("amazon", "seller@example.test"), http.StatusCreated)
}

func TestProxyAndProfileNameConflictsAreReported(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	token, base := newConflictWorkspace(t, handler, "proxy-names@example.test")
	proxy := func(name string) *httptest.ResponseRecorder {
		return perform(t, handler, http.MethodPost, base+"/proxies", token, "", map[string]interface{}{
			"name": name, "protocol": "direct", "connectorType": "xray",
		})
	}
	assertStatus(t, proxy("Residential"), http.StatusCreated)
	assertErrorCode(t, proxy("residential"), http.StatusConflict, "proxy_name_conflict")

	profile := func(name string) *httptest.ResponseRecorder {
		return perform(t, handler, http.MethodPost, base+"/profiles", token, "", map[string]string{"name": name})
	}
	assertStatus(t, profile("Storefront"), http.StatusCreated)
	assertErrorCode(t, profile("STOREFRONT"), http.StatusConflict, "profile_name_conflict")
}

func TestProxyAssignmentsCanBeReadWithProxyNames(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	token, base := newConflictWorkspace(t, handler, "assignment-reads@example.test")
	newProxy := func(name string) proxyservice.Proxy {
		response := perform(t, handler, http.MethodPost, base+"/proxies", token, "", map[string]interface{}{
			"name": name, "protocol": "direct", "connectorType": "xray",
		})
		assertStatus(t, response, http.StatusCreated)
		return decodeData[proxyservice.Proxy](t, response)
	}
	newInstance := func(name string) browserinstanceservice.BrowserInstance {
		response := perform(t, handler, http.MethodPost, base+"/browser-instances", token, "", map[string]interface{}{"name": name, "platform": "chromium"})
		assertStatus(t, response, http.StatusCreated)
		return decodeData[browserinstanceservice.BrowserInstance](t, response)
	}
	us, eu := newProxy("US residential"), newProxy("EU datacenter")
	first, second := newInstance("First"), newInstance("Second")
	accountResponse := perform(t, handler, http.MethodPost, base+"/accounts", token, "", map[string]string{"platform": "amazon", "name": "Seller", "identifier": "assignment-seller"})
	assertStatus(t, accountResponse, http.StatusCreated)
	account := decodeData[accountservice.Account](t, accountResponse)

	assign := func(proxyID, targetType, targetID string) *httptest.ResponseRecorder {
		return perform(t, handler, http.MethodPost, base+"/proxies/"+proxyID+"/assignments", token, "", map[string]string{"targetType": targetType, "targetId": targetID})
	}
	assigned := assign(us.ID, "browser_instance", first.ID)
	assertStatus(t, assigned, http.StatusCreated)
	if created := decodeData[proxyservice.Assignment](t, assigned); created.ProxyName != "US residential" {
		t.Fatalf("assign response has proxy name %q", created.ProxyName)
	}
	assertStatus(t, assign(eu.ID, "browser_instance", second.ID), http.StatusCreated)
	assertStatus(t, assign(eu.ID, "account", account.ID), http.StatusCreated)
	assertErrorCode(t, assign(eu.ID, "browser_instance", first.ID), http.StatusConflict, "proxy_assignment_conflict")

	read := perform(t, handler, http.MethodGet, base+"/proxy-assignments/browser_instance/"+first.ID, token, "", nil)
	assertStatus(t, read, http.StatusOK)
	current := decodeData[proxyservice.Assignment](t, read)
	if current.ProxyID != us.ID || current.ProxyName != "US residential" || current.TargetID != first.ID || current.Version < 1 {
		t.Fatalf("assignment read = %+v", current)
	}
	list := func(query string) []proxyservice.Assignment {
		response := perform(t, handler, http.MethodGet, base+"/proxy-assignments"+query, token, "", nil)
		assertStatus(t, response, http.StatusOK)
		return decodeData[[]proxyservice.Assignment](t, response)
	}
	if items := list(""); len(items) != 3 {
		t.Fatalf("all assignments = %+v", items)
	}
	if items := list("?targetType=browser_instance"); len(items) != 2 || items[0].ProxyName == "" || items[1].ProxyName == "" {
		t.Fatalf("instance assignments = %+v", items)
	}
	if items := list("?proxyId=" + eu.ID); len(items) != 2 || items[0].ProxyName != "EU datacenter" {
		t.Fatalf("EU assignments = %+v", items)
	}
	assertErrorCode(t, perform(t, handler, http.MethodGet, base+"/proxy-assignments?targetType=device", token, "", nil), http.StatusUnprocessableEntity, "validation_failed")
	assertErrorCode(t, perform(t, handler, http.MethodGet, base+"/proxy-assignments?proxyId=not-a-uuid", token, "", nil), http.StatusUnprocessableEntity, "validation_failed")

	// The version from the read is what unassigning needs.
	assertStatus(t, perform(t, handler, http.MethodDelete, base+"/proxy-assignments/browser_instance/"+first.ID+"?version=1", token, "", nil), http.StatusNoContent)
	assertErrorCode(t, perform(t, handler, http.MethodGet, base+"/proxy-assignments/browser_instance/"+first.ID, token, "", nil), http.StatusNotFound, "not_found")
	assertErrorCode(t, perform(t, handler, http.MethodGet, base+"/proxy-assignments/browser_instance/"+uuid.NewString(), token, "", nil), http.StatusNotFound, "not_found")

	stranger := register(t, handler, "assignment-stranger@example.test")
	assertStatus(t, perform(t, handler, http.MethodGet, base+"/proxy-assignments", stranger.AccessToken, "", nil), http.StatusForbidden)
}

func TestRiskEventLevelsMatchStoredSeverities(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	token, base := newConflictWorkspace(t, handler, "risk-levels@example.test")
	created := perform(t, handler, http.MethodPost, base+"/accounts", token, "", map[string]string{"platform": "tiktok", "name": "Creator", "identifier": "creator"})
	assertStatus(t, created, http.StatusCreated)
	account := decodeData[accountservice.Account](t, created)
	record := func(level string) *httptest.ResponseRecorder {
		return perform(t, handler, http.MethodPost, base+"/accounts/"+account.ID+"/risk-events", token, "", map[string]string{"level": level, "code": "login_challenge"})
	}
	for _, level := range []string{"info", "low", "MEDIUM", "high", "critical"} {
		assertStatus(t, record(level), http.StatusCreated)
	}
	for _, level := range []string{"unknown", "", "severe"} {
		assertErrorCode(t, record(level), http.StatusUnprocessableEntity, "validation_failed")
	}
	listed := perform(t, handler, http.MethodGet, base+"/accounts/"+account.ID+"/risk-events", token, "", nil)
	assertStatus(t, listed, http.StatusOK)
	events := decodeData[[]accountservice.RiskEvent](t, listed)
	if len(events) != 5 {
		t.Fatalf("risk events = %+v", events)
	}
	for _, event := range events {
		if event.Level == "MEDIUM" {
			t.Fatal("risk event level was not normalized")
		}
	}
}

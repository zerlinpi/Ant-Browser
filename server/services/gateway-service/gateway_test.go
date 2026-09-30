package gatewayservice_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	"github.com/zerlinpi/Ant-Browser/server/platform/realtime"
	"github.com/zerlinpi/Ant-Browser/server/platform/secureenvelope"
	"github.com/zerlinpi/Ant-Browser/server/platform/security"
	"github.com/zerlinpi/Ant-Browser/server/platform/taskwake"
	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
	adminservice "github.com/zerlinpi/Ant-Browser/server/services/admin-service"
	analyticsservice "github.com/zerlinpi/Ant-Browser/server/services/analytics-service"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	batchservice "github.com/zerlinpi/Ant-Browser/server/services/batch-service"
	billingservice "github.com/zerlinpi/Ant-Browser/server/services/billing-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	fingerprintservice "github.com/zerlinpi/Ant-Browser/server/services/fingerprint-service"
	gatewayservice "github.com/zerlinpi/Ant-Browser/server/services/gateway-service"
	profilesyncservice "github.com/zerlinpi/Ant-Browser/server/services/profile-sync-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

type envelope[T any] struct {
	Data T `json:"data"`
}

func TestControlPlaneTenantAndInstanceFlow(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	owner := register(t, handler, "owner@example.com")

	me := perform(t, handler, http.MethodGet, "/api/v1/me", owner.AccessToken, "", nil)
	assertStatus(t, me, http.StatusOK)

	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]interface{}{
		"name": "跨境团队",
	})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)
	if workspace.Slug == "" || !strings.HasPrefix(workspace.Slug, "ws-") {
		t.Fatalf("workspace slug %q is not a safe fallback", workspace.Slug)
	}

	deviceResponse := perform(t, handler, http.MethodPost, "/api/v1/devices", owner.AccessToken, "", map[string]interface{}{
		"workspaceId":  workspace.ID,
		"name":         "Operations workstation",
		"platform":     "windows",
		"agentVersion": "0.1.0",
		"capabilities": map[string]interface{}{"chromium": true},
	})
	assertStatus(t, deviceResponse, http.StatusCreated)
	var registration envelope[deviceservice.Registration]
	decodeResponse(t, deviceResponse, &registration)
	if registration.Data.Credential == "" || registration.Data.Device.WorkspaceID != workspace.ID {
		t.Fatal("device registration did not return a scoped one-time credential")
	}

	instanceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/browser-instances", owner.AccessToken, "", map[string]interface{}{
		"name":             "Amazon US 01",
		"platform":         "chromium",
		"assignedDeviceId": registration.Data.Device.ID,
		"tags":             []string{"amazon", "us"},
	})
	assertStatus(t, instanceResponse, http.StatusCreated)
	instance := decodeData[browserinstanceservice.BrowserInstance](t, instanceResponse)

	commandResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+instance.ID+"/commands", owner.AccessToken, "start-once", map[string]interface{}{
		"action": "instance.start", "expectedVersion": 1,
	})
	assertStatus(t, commandResponse, http.StatusAccepted)
	var commandEnvelope envelope[struct {
		Command  browserinstanceservice.Command         `json:"command"`
		Instance browserinstanceservice.BrowserInstance `json:"instance"`
	}]
	decodeResponse(t, commandResponse, &commandEnvelope)
	if commandEnvelope.Data.Instance.DesiredState != "running" || commandEnvelope.Data.Instance.Version != 2 {
		t.Fatalf("unexpected desired state/version: %+v", commandEnvelope.Data.Instance)
	}

	replayed := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+instance.ID+"/commands", owner.AccessToken, "start-once", map[string]interface{}{
		"action": "instance.start", "expectedVersion": 1,
	})
	assertStatus(t, replayed, http.StatusAccepted)
	replayedEnvelope := envelope[struct {
		Command browserinstanceservice.Command `json:"command"`
	}]{}
	decodeResponse(t, replayed, &replayedEnvelope)
	if replayedEnvelope.Data.Command.ID != commandEnvelope.Data.Command.ID {
		t.Fatal("idempotent request created a second command")
	}

	secondUser := register(t, handler, "viewer@example.com")
	forbidden := perform(t, handler, http.MethodGet, "/api/v1/workspaces/"+workspace.ID, secondUser.AccessToken, "", nil)
	assertStatus(t, forbidden, http.StatusForbidden)

	refreshResponse := perform(t, handler, http.MethodPost, "/api/v1/auth/refresh", "", "", map[string]string{"refreshToken": owner.RefreshToken})
	assertStatus(t, refreshResponse, http.StatusOK)
	replayedRefresh := perform(t, handler, http.MethodPost, "/api/v1/auth/refresh", "", "", map[string]string{"refreshToken": owner.RefreshToken})
	assertStatus(t, replayedRefresh, http.StatusUnauthorized)
}

func TestBrowserInstanceUpdateCloneMigrateAndDeleteFlow(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	owner := register(t, handler, "instance-lifecycle@example.com")
	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]interface{}{"name": "Lifecycle Team"})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)
	sourceDeviceResponse := perform(t, handler, http.MethodPost, "/api/v1/devices", owner.AccessToken, "", map[string]interface{}{
		"workspaceId": workspace.ID, "name": "Migration source", "platform": "windows", "agentVersion": "0.1.0",
	})
	assertStatus(t, sourceDeviceResponse, http.StatusCreated)
	sourceDevice := decodeData[deviceservice.Registration](t, sourceDeviceResponse)
	cloudProfileResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/profiles", owner.AccessToken, "", map[string]string{"name": "Migrated storefront profile"})
	assertStatus(t, cloudProfileResponse, http.StatusCreated)
	cloudProfile := decodeData[profilesyncservice.Profile](t, cloudProfileResponse)
	unassignedResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/browser-instances", owner.AccessToken, "", map[string]interface{}{
		"name": "Unassigned", "platform": "chromium",
	})
	assertStatus(t, unassignedResponse, http.StatusCreated)
	unassigned := decodeData[browserinstanceservice.BrowserInstance](t, unassignedResponse)
	assertStatus(t, perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+unassigned.ID+"/commands", owner.AccessToken, "unassigned-start", map[string]interface{}{
		"action": "instance.start", "expectedVersion": 1,
	}), http.StatusUnprocessableEntity)

	createdResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/browser-instances", owner.AccessToken, "", map[string]interface{}{
		"name": "Storefront", "platform": "chromium", "tags": []string{"shopify"}, "assignedDeviceId": sourceDevice.Device.ID, "profileId": cloudProfile.ID,
	})
	assertStatus(t, createdResponse, http.StatusCreated)
	created := decodeData[browserinstanceservice.BrowserInstance](t, createdResponse)

	updatedResponse := perform(t, handler, http.MethodPatch, "/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+created.ID, owner.AccessToken, "", map[string]interface{}{
		"expectedVersion": 1, "name": "Storefront US", "tags": []string{"shopify", "us", "us"},
	})
	assertStatus(t, updatedResponse, http.StatusOK)
	updated := decodeData[browserinstanceservice.BrowserInstance](t, updatedResponse)
	if updated.Name != "Storefront US" || updated.Version != 2 || len(updated.Tags) != 2 {
		t.Fatalf("unexpected updated instance: %+v", updated)
	}
	stale := perform(t, handler, http.MethodPatch, "/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+created.ID, owner.AccessToken, "", map[string]interface{}{
		"expectedVersion": 1, "name": "Stale update",
	})
	assertStatus(t, stale, http.StatusConflict)

	cloneResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+created.ID+"/clone", owner.AccessToken, "", map[string]interface{}{"name": "Storefront Backup"})
	assertStatus(t, cloneResponse, http.StatusCreated)
	cloned := decodeData[browserinstanceservice.BrowserInstance](t, cloneResponse)
	if cloned.ID == created.ID || cloned.Name != "Storefront Backup" || len(cloned.Tags) != 2 || cloned.DesiredState != "stopped" {
		t.Fatalf("unexpected cloned instance: %+v", cloned)
	}

	invalidMigration := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+created.ID+"/commands", owner.AccessToken, "migrate-invalid", map[string]interface{}{
		"action": "instance.migrate", "expectedVersion": 2, "payload": map[string]interface{}{"targetDeviceId": "00000000-0000-0000-0000-000000000123"},
	})
	assertStatus(t, invalidMigration, http.StatusUnprocessableEntity)
	deviceResponse := perform(t, handler, http.MethodPost, "/api/v1/devices", owner.AccessToken, "", map[string]interface{}{
		"workspaceId": workspace.ID, "name": "Migration destination", "platform": "windows", "agentVersion": "0.1.0",
	})
	assertStatus(t, deviceResponse, http.StatusCreated)
	device := decodeData[deviceservice.Registration](t, deviceResponse)
	migrateResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+created.ID+"/commands", owner.AccessToken, "migrate-once", map[string]interface{}{
		"action": "instance.migrate", "expectedVersion": 2, "payload": map[string]interface{}{"targetDeviceId": device.Device.ID},
	})
	assertStatus(t, migrateResponse, http.StatusAccepted)
	migrateResult := decodeData[struct {
		Command browserinstanceservice.Command `json:"command"`
	}](t, migrateResponse)
	migration := migrateResult.Command
	if window := migration.Deadline.Sub(migration.CreatedAt); window < 29*time.Minute || window > 31*time.Minute {
		t.Fatalf("migration command deadline window = %s, want approximately 30m", window)
	}
	conflictingReplay := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+created.ID+"/commands", owner.AccessToken, "migrate-once", map[string]interface{}{
		"action": "instance.migrate", "expectedVersion": 2, "payload": map[string]interface{}{"targetDeviceId": device.Device.ID, "changed": true},
	})
	assertStatus(t, conflictingReplay, http.StatusConflict)
	unsafeReassignment := perform(t, handler, http.MethodPatch, "/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+created.ID, owner.AccessToken, "", map[string]interface{}{
		"expectedVersion": 3, "assignedDeviceId": device.Device.ID,
	})
	assertStatus(t, unsafeReassignment, http.StatusConflict)

	deletedResponse := perform(t, handler, http.MethodDelete, "/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+cloned.ID, owner.AccessToken, "", map[string]interface{}{"expectedVersion": 1})
	assertStatus(t, deletedResponse, http.StatusNoContent)
	missing := perform(t, handler, http.MethodGet, "/api/v1/workspaces/"+workspace.ID+"/browser-instances/"+cloned.ID, owner.AccessToken, "", nil)
	assertStatus(t, missing, http.StatusNotFound)
}

func TestAdminAPIRequiresPlatformRoleAndRevokesSuspendedUser(t *testing.T) {
	t.Parallel()
	handler, store := newTestGatewayWithStore()
	admin := register(t, handler, "platform-admin@example.com")
	target := register(t, handler, "admin-target@example.com")
	store.SeedPlatformAdmin(adminservice.PlatformAdmin{
		UserID: admin.User.ID, Role: adminservice.PlatformAdminRole, Status: "active",
		GrantedBy: admin.User.ID, GrantedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})

	denied := perform(t, handler, http.MethodGet, "/api/v1/admin/users", target.AccessToken, "", nil)
	assertStatus(t, denied, http.StatusForbidden)
	listed := perform(t, handler, http.MethodGet, "/api/v1/admin/users?limit=10", admin.AccessToken, "", nil)
	assertStatus(t, listed, http.StatusOK)
	page := decodeData[adminservice.Page[adminservice.User]](t, listed)
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("unexpected admin user page: %+v", page)
	}

	suspended := perform(t, handler, http.MethodPatch, "/api/v1/admin/users/"+target.User.ID+"/status", admin.AccessToken, "", map[string]string{
		"status": adminservice.UserStatusSuspended, "reason": "automated security test",
	})
	assertStatus(t, suspended, http.StatusOK)
	if user := decodeData[adminservice.User](t, suspended); user.Status != adminservice.UserStatusSuspended {
		t.Fatalf("target was not suspended: %+v", user)
	}
	invalidated := perform(t, handler, http.MethodGet, "/api/v1/me", target.AccessToken, "", nil)
	assertStatus(t, invalidated, http.StatusUnauthorized)
	if events := store.AdminAuditEvents(); len(events) < 3 || events[len(events)-1].RequestID == "" {
		t.Fatalf("admin actions were not correlated in audit: %+v", events)
	}
	malformed := perform(t, handler, http.MethodPatch, "/api/v1/admin/users/not-a-uuid/status", admin.AccessToken, "", map[string]string{"status": "active"})
	assertStatus(t, malformed, http.StatusUnprocessableEntity)
}

func TestAnalyticsGatewayValidatesQueryBeforeRepository(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	owner := register(t, handler, "analytics-owner@example.com")
	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Analytics Team"})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)

	dashboard := perform(t, handler, http.MethodGet, "/api/v1/workspaces/"+workspace.ID+"/analytics/dashboard", owner.AccessToken, "", nil)
	assertStatus(t, dashboard, http.StatusOK)
	badWindow := perform(t, handler, http.MethodGet, "/api/v1/workspaces/"+workspace.ID+"/analytics/dashboard?from=not-a-time", owner.AccessToken, "", nil)
	assertStatus(t, badWindow, http.StatusUnprocessableEntity)
	badActor := perform(t, handler, http.MethodGet, "/api/v1/workspaces/"+workspace.ID+"/analytics/audit-events?actorUserId=not-a-uuid", owner.AccessToken, "", nil)
	assertStatus(t, badActor, http.StatusUnprocessableEntity)
}

func TestTaskAPIFlow(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	owner := register(t, handler, "task-owner@example.com")
	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Task Team"})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)

	create := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/tasks", owner.AccessToken, "health-once", map[string]interface{}{
		"taskType": "system.healthcheck", "payload": map[string]interface{}{"source": "api-test"},
	})
	assertStatus(t, create, http.StatusAccepted)
	task := decodeData[taskservice.Task](t, create)
	if task.Status != "queued" || task.RetryLimit != 3 {
		t.Fatalf("unexpected queued task: %+v", task)
	}
	replay := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/tasks", owner.AccessToken, "health-once", map[string]interface{}{
		"taskType": "system.healthcheck", "payload": map[string]interface{}{"source": "api-test"},
	})
	assertStatus(t, replay, http.StatusAccepted)
	if replayed := decodeData[taskservice.Task](t, replay); replayed.ID != task.ID {
		t.Fatal("task idempotency key created a duplicate task")
	}

	list := perform(t, handler, http.MethodGet, "/api/v1/workspaces/"+workspace.ID+"/tasks", owner.AccessToken, "", nil)
	assertStatus(t, list, http.StatusOK)
	items := decodeData[[]taskservice.Task](t, list)
	if len(items) != 1 || items[0].ID != task.ID {
		t.Fatalf("unexpected task list: %+v", items)
	}
	cancel := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/tasks/"+task.ID+"/cancel", owner.AccessToken, "", nil)
	assertStatus(t, cancel, http.StatusOK)
	if cancelled := decodeData[taskservice.Task](t, cancel); cancelled.Status != "cancelled" {
		t.Fatalf("task was not cancelled: %+v", cancelled)
	}
}

func TestFingerprintTemplateAPIFlow(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	owner := register(t, handler, "fingerprint-owner@example.com")
	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Fingerprint Team"})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)

	create := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/fingerprint-templates", owner.AccessToken, "", map[string]interface{}{
		"name": "US Windows", "mode": "fixed", "browserMajor": 144,
		"platform": "windows", "seed": "676448312360042767",
		"locale": "en-US", "timezone": "America/New_York",
		"configuration": map[string]interface{}{
			"windowWidth": 1920, "windowHeight": 1080, "hardwareConcurrency": 8,
			"canvasNoise": true, "clientRectsNoise": true,
		},
	})
	assertStatus(t, create, http.StatusCreated)
	template := decodeData[fingerprintservice.Template](t, create)
	if template.Seed != 676448312360042767 || len(template.RuntimeArgs) == 0 {
		t.Fatalf("fingerprint template was not normalized: %+v", template)
	}

	update := perform(t, handler, http.MethodPatch, "/api/v1/workspaces/"+workspace.ID+"/fingerprint-templates/"+template.ID, owner.AccessToken, "", map[string]interface{}{
		"name": "US Windows Updated", "mode": template.Mode, "browserMajor": 145,
		"platform": template.Platform, "seed": "676448312360042767",
		"locale": template.Locale, "timezone": template.Timezone,
		"configuration": template.Configuration, "version": template.Version,
	})
	assertStatus(t, update, http.StatusOK)
	updated := decodeData[fingerprintservice.Template](t, update)
	if updated.Version != 2 || updated.BrowserMajor != 145 {
		t.Fatalf("fingerprint template update failed: %+v", updated)
	}

	remove := perform(t, handler, http.MethodDelete, "/api/v1/workspaces/"+workspace.ID+"/fingerprint-templates/"+template.ID+"?version=2", owner.AccessToken, "", nil)
	assertStatus(t, remove, http.StatusNoContent)
	missing := perform(t, handler, http.MethodGet, "/api/v1/workspaces/"+workspace.ID+"/fingerprint-templates/"+template.ID, owner.AccessToken, "", nil)
	assertStatus(t, missing, http.StatusNotFound)
}

func TestWorkflowAPIFlow(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	owner := register(t, handler, "workflow-owner@example.com")
	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Automation Team"})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)
	definition := map[string]interface{}{
		"engine": "playwright",
		"steps": []map[string]interface{}{
			{"id": "open", "action": "navigate", "parameters": map[string]interface{}{"url": "https://seller.example.test"}},
			{"id": "capture", "action": "screenshot", "parameters": map[string]interface{}{"name": "seller-home", "format": "png"}},
			{"id": "finish", "action": "close"},
		},
	}
	create := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/workflows", owner.AccessToken, "", map[string]interface{}{
		"name": "Seller smoke test", "definition": definition,
	})
	assertStatus(t, create, http.StatusCreated)
	created := decodeData[struct {
		Workflow automationservice.Workflow        `json:"workflow"`
		Version  automationservice.WorkflowVersion `json:"version"`
	}](t, create)
	if created.Workflow.Status != "draft" || created.Version.ContentHash == "" {
		t.Fatalf("workflow was not created with an immutable version: %+v", created)
	}

	add := perform(t, handler, http.MethodPost,
		"/api/v1/workspaces/"+workspace.ID+"/workflows/"+created.Workflow.ID+"/versions",
		owner.AccessToken, "", map[string]interface{}{"expectedVersion": created.Workflow.Version, "definition": definition})
	assertStatus(t, add, http.StatusCreated)
	added := decodeData[struct {
		Workflow automationservice.Workflow        `json:"workflow"`
		Version  automationservice.WorkflowVersion `json:"version"`
	}](t, add)
	if added.Version.Version != 2 || added.Version.ContentHash != created.Version.ContentHash {
		t.Fatalf("workflow version was not deterministic: %+v", added)
	}

	publish := perform(t, handler, http.MethodPost,
		"/api/v1/workspaces/"+workspace.ID+"/workflows/"+created.Workflow.ID+"/publish",
		owner.AccessToken, "", map[string]interface{}{"expectedVersion": added.Workflow.Version, "workflowVersion": added.Version.Version})
	assertStatus(t, publish, http.StatusOK)
	if published := decodeData[automationservice.Workflow](t, publish); published.Status != "published" || published.PublishedVersionID != added.Version.ID {
		t.Fatalf("workflow was not published: %+v", published)
	}

	unsafe := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/workflows", owner.AccessToken, "", map[string]interface{}{
		"name": "Unsafe", "definition": map[string]interface{}{
			"engine": "cdp", "steps": []map[string]interface{}{{
				"id": "password", "action": "input",
				"parameters": map[string]interface{}{"selector": "#password", "value": "plaintext"},
			}},
		},
	})
	assertStatus(t, unsafe, http.StatusUnprocessableEntity)
}

func TestProfileSyncAPIFlow(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	owner := register(t, handler, "profile-owner@example.com")
	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Profile Team"})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)

	deviceResponse := perform(t, handler, http.MethodPost, "/api/v1/devices", owner.AccessToken, "", map[string]interface{}{
		"workspaceId": workspace.ID, "name": "Sync workstation", "platform": "windows",
		"agentVersion": "0.1.0", "capabilities": map[string]interface{}{"profileSync": true},
	})
	assertStatus(t, deviceResponse, http.StatusCreated)
	registration := decodeData[deviceservice.Registration](t, deviceResponse)

	create := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/profiles", owner.AccessToken, "", map[string]string{"name": "Amazon seller profile"})
	assertStatus(t, create, http.StatusCreated)
	profile := decodeData[profilesyncservice.Profile](t, create)

	acquire := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/profiles/"+profile.ID+"/lease", owner.AccessToken, "", map[string]interface{}{
		"deviceId": registration.Device.ID, "ttlSeconds": 300,
	})
	assertStatus(t, acquire, http.StatusCreated)
	grant := decodeData[profilesyncservice.LeaseGrant](t, acquire)
	if grant.Token == "" || grant.Lease.HolderDeviceID != registration.Device.ID {
		t.Fatalf("invalid profile lease grant: %+v", grant)
	}

	begin := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/profiles/"+profile.ID+"/revisions", owner.AccessToken, "", map[string]interface{}{
		"deviceId": registration.Device.ID, "leaseToken": grant.Token,
		"files": []map[string]interface{}{{
			"path": "Default/Cookies", "ciphertextSha256": strings.Repeat("a", 64),
			"sizeBytes": 128, "contentType": "application/octet-stream",
		}},
	})
	assertStatus(t, begin, http.StatusCreated)
	plan := decodeData[profilesyncservice.RevisionPlan](t, begin)
	if len(plan.Objects) != 1 || plan.Manifest.Files[0].Path != "Default/Cookies" {
		t.Fatalf("invalid revision upload plan: %+v", plan)
	}

	upload := perform(t, handler, http.MethodPost,
		"/api/v1/workspaces/"+workspace.ID+"/profiles/"+profile.ID+"/revisions/"+plan.Revision.ID+"/objects/"+plan.Objects[0].ID+"/upload",
		owner.AccessToken, "", map[string]string{"deviceId": registration.Device.ID, "token": grant.Token})
	assertStatus(t, upload, http.StatusOK)
	uploadGrant := decodeData[profilesyncservice.UploadGrant](t, upload)
	if !strings.HasPrefix(uploadGrant.URL, "memory://profile-objects/") || uploadGrant.Method != http.MethodPut {
		t.Fatalf("invalid object upload grant: %+v", uploadGrant)
	}

	commit := perform(t, handler, http.MethodPost,
		"/api/v1/workspaces/"+workspace.ID+"/profiles/"+profile.ID+"/revisions/"+plan.Revision.ID+"/commit",
		owner.AccessToken, "", map[string]string{"deviceId": registration.Device.ID, "token": grant.Token})
	assertStatus(t, commit, http.StatusOK)
	var committed envelope[struct {
		Profile  profilesyncservice.Profile  `json:"profile"`
		Revision profilesyncservice.Revision `json:"revision"`
	}]
	decodeResponse(t, commit, &committed)
	if committed.Data.Profile.CurrentRevisionID != plan.Revision.ID || committed.Data.Revision.Status != "committed" {
		t.Fatalf("profile revision was not promoted: %+v", committed.Data)
	}

	revisions := perform(t, handler, http.MethodGet, "/api/v1/workspaces/"+workspace.ID+"/profiles/"+profile.ID+"/revisions", owner.AccessToken, "", nil)
	assertStatus(t, revisions, http.StatusOK)
	if items := decodeData[[]profilesyncservice.Revision](t, revisions); len(items) != 1 || items[0].ID != plan.Revision.ID {
		t.Fatalf("unexpected profile revision history: %+v", items)
	}
	snapshot := perform(t, handler, http.MethodGet,
		"/api/v1/workspaces/"+workspace.ID+"/profiles/"+profile.ID+"/revisions/"+plan.Revision.ID,
		owner.AccessToken, "", nil)
	assertStatus(t, snapshot, http.StatusOK)
	if restored := decodeData[profilesyncservice.RevisionPlan](t, snapshot); restored.Manifest.FileCount != 1 {
		t.Fatalf("unexpected committed profile snapshot: %+v", restored)
	}
	download := perform(t, handler, http.MethodGet,
		"/api/v1/workspaces/"+workspace.ID+"/profiles/"+profile.ID+"/revisions/"+plan.Revision.ID+"/objects/"+plan.Objects[0].ID+"/download",
		owner.AccessToken, "", nil)
	assertStatus(t, download, http.StatusOK)
	if grant := decodeData[profilesyncservice.DownloadGrant](t, download); grant.Method != http.MethodGet || !strings.HasPrefix(grant.URL, "memory://") {
		t.Fatalf("invalid object download grant: %+v", grant)
	}

	secondAcquire := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/profiles/"+profile.ID+"/lease", owner.AccessToken, "", map[string]interface{}{
		"deviceId": registration.Device.ID, "ttlSeconds": 300,
	})
	assertStatus(t, secondAcquire, http.StatusCreated)
	secondGrant := decodeData[profilesyncservice.LeaseGrant](t, secondAcquire)
	stale := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/profiles/"+profile.ID+"/revisions", owner.AccessToken, "", map[string]interface{}{
		"deviceId": registration.Device.ID, "leaseToken": secondGrant.Token, "baseRevisionId": "",
		"files": []map[string]interface{}{{
			"path": "Default/Preferences", "ciphertextSha256": strings.Repeat("b", 64), "sizeBytes": 64,
		}},
	})
	assertStatus(t, stale, http.StatusConflict)
	stalePlan := decodeData[profilesyncservice.RevisionPlan](t, stale)
	if stalePlan.Conflict == nil || stalePlan.Conflict.RemoteRevisionID != plan.Revision.ID {
		t.Fatalf("stale base did not create a recoverable conflict: %+v", stalePlan)
	}

	resolve := perform(t, handler, http.MethodPost,
		"/api/v1/workspaces/"+workspace.ID+"/profiles/"+profile.ID+"/conflicts/"+stalePlan.Conflict.ID+"/resolve",
		owner.AccessToken, "", map[string]string{"resolution": "keep_remote"})
	assertStatus(t, resolve, http.StatusOK)
	if conflict := decodeData[profilesyncservice.Conflict](t, resolve); conflict.Status != "resolved" {
		t.Fatalf("profile conflict was not resolved: %+v", conflict)
	}

	missing := perform(t, handler, http.MethodGet, "/api/v1/workspaces/"+workspace.ID+"/profiles/00000000-0000-0000-0000-000000000001/revisions", owner.AccessToken, "", nil)
	assertStatus(t, missing, http.StatusNotFound)
}

func TestDeviceProfileAPIIsLimitedToAssignedProfiles(t *testing.T) {
	t.Parallel()
	handler := newTestGateway()
	owner := register(t, handler, "device-profile-owner@example.com")
	workspaceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces", owner.AccessToken, "", map[string]string{"name": "Device Profile Team"})
	assertStatus(t, workspaceResponse, http.StatusCreated)
	workspace := decodeData[workspaceservice.Workspace](t, workspaceResponse)
	deviceResponse := perform(t, handler, http.MethodPost, "/api/v1/devices", owner.AccessToken, "", map[string]interface{}{
		"workspaceId": workspace.ID, "name": "Assigned device", "platform": "windows", "agentVersion": "2.0.0",
	})
	assertStatus(t, deviceResponse, http.StatusCreated)
	device := decodeData[deviceservice.Registration](t, deviceResponse)
	otherDeviceResponse := perform(t, handler, http.MethodPost, "/api/v1/devices", owner.AccessToken, "", map[string]interface{}{
		"workspaceId": workspace.ID, "name": "Other device", "platform": "linux", "agentVersion": "2.0.0",
	})
	assertStatus(t, otherDeviceResponse, http.StatusCreated)
	otherDevice := decodeData[deviceservice.Registration](t, otherDeviceResponse)
	profileResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/profiles", owner.AccessToken, "", map[string]string{"name": "Assigned profile"})
	assertStatus(t, profileResponse, http.StatusCreated)
	profile := decodeData[profilesyncservice.Profile](t, profileResponse)
	unassignedResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/profiles", owner.AccessToken, "", map[string]string{"name": "Unassigned profile"})
	assertStatus(t, unassignedResponse, http.StatusCreated)
	unassigned := decodeData[profilesyncservice.Profile](t, unassignedResponse)
	instanceResponse := perform(t, handler, http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/browser-instances", owner.AccessToken, "", map[string]interface{}{
		"name": "Assigned instance", "platform": "chromium", "assignedDeviceId": device.Device.ID, "profileId": profile.ID,
	})
	assertStatus(t, instanceResponse, http.StatusCreated)

	get := performDevice(t, handler, http.MethodGet, "/api/v1/agent/profiles/"+profile.ID, device.Device.ID, device.Credential, nil)
	assertStatus(t, get, http.StatusOK)
	if got := decodeData[profilesyncservice.Profile](t, get); got.ID != profile.ID {
		t.Fatalf("device received wrong profile: %+v", got)
	}
	lease := performDevice(t, handler, http.MethodPost, "/api/v1/agent/profiles/"+profile.ID+"/lease", device.Device.ID, device.Credential, map[string]interface{}{
		"deviceId": device.Device.ID, "ttlSeconds": 300,
	})
	assertStatus(t, lease, http.StatusCreated)
	grant := decodeData[profilesyncservice.LeaseGrant](t, lease)
	if grant.Lease.HolderDeviceID != device.Device.ID || grant.Token == "" {
		t.Fatalf("device lease was not scoped: %+v", grant)
	}
	spoofed := performDevice(t, handler, http.MethodPost, "/api/v1/agent/profiles/"+profile.ID+"/lease", device.Device.ID, device.Credential, map[string]interface{}{
		"deviceId": otherDevice.Device.ID, "ttlSeconds": 300,
	})
	assertStatus(t, spoofed, http.StatusForbidden)
	assertStatus(t, performDevice(t, handler, http.MethodGet, "/api/v1/agent/profiles/"+unassigned.ID, device.Device.ID, device.Credential, nil), http.StatusNotFound)
	assertStatus(t, performDevice(t, handler, http.MethodGet, "/api/v1/agent/profiles/"+profile.ID, otherDevice.Device.ID, otherDevice.Credential, nil), http.StatusNotFound)
	assertStatus(t, performDevice(t, handler, http.MethodGet, "/api/v1/agent/profiles/"+profile.ID, device.Device.ID, "invalid", nil), http.StatusUnauthorized)
}

// newTestGateway builds a fully wired gateway over a fresh memory store.
// Extra options (for example gatewayservice.RateLimits or TrustedProxies) are
// appended to NewWithInfrastructure's options.
func newTestGateway(options ...interface{}) http.Handler {
	handler, _ := newTestGatewayWithStore(options...)
	return handler
}

func newTestGatewayWithStore(options ...interface{}) (http.Handler, *memory.Store) {
	store := memory.New()
	tokens := security.NewTokens("test-issuer", "01234567890123456789012345678901", 5*time.Minute)
	auth := authservice.New(store, security.NewPasswords(), tokens, security.NewOpaqueToken, 24*time.Hour)
	workspaces := workspaceservice.New(store)
	devices := deviceservice.New(store, security.NewOpaqueToken, workspaces)
	instances := browserinstanceservice.New(store, workspaces)
	tasks := taskservice.New(store, workspaces)
	fingerprints := fingerprintservice.New(store, workspaces)
	profiles := profilesyncservice.New(
		store, workspaces, security.NewOpaqueToken, profilesyncservice.MetadataVerifier{},
		"test-profile-key", "metadata",
	)
	workflows := automationservice.New(store, workspaces)
	encryption, err := secureenvelope.New("MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=", "test-secret-key", "v1")
	if err != nil {
		panic(err)
	}
	mfaSealer, err := secureenvelope.NewTextSealer(encryption)
	if err != nil {
		panic(err)
	}
	auth.ConfigureMFA(mfaSealer, "Ant Browser")
	secrets, err := secureenvelope.NewProxyProvider(store, encryption)
	if err != nil {
		panic(err)
	}
	accounts := accountservice.New(store, workspaces, encryption)
	proxies := proxyservice.New(store, workspaces, secrets)
	batches := batchservice.New(instances, accounts, proxies, tasks, workspaces)
	billing := billingservice.NewAuthorized(store, workspaces)
	analytics := analyticsservice.New(store, workspaces)
	admin := adminservice.New(store)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := gatewayservice.NewWithInfrastructure(
		context.Background(), auth, workspaces, devices, instances, tokens, store,
		realtime.NewDisabled(), tasks, taskwake.NewDisabled(), fingerprints, profiles, workflows, accounts, proxies, logger,
		append([]interface{}{analytics, admin, batches, billing}, options...)...,
	)
	return handler, store
}

func register(t *testing.T, handler http.Handler, email string) authservice.TokenPair {
	t.Helper()
	response := perform(t, handler, http.MethodPost, "/api/v1/auth/register", "", "", map[string]string{
		"email": email, "password": "SecurePassword123", "displayName": "Test User",
	})
	assertStatus(t, response, http.StatusCreated)
	return decodeData[authservice.TokenPair](t, response)
}

func perform(t *testing.T, handler http.Handler, method, path, accessToken, idempotencyKey string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	request.RemoteAddr = "127.0.0.1:12345"
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func performDevice(t *testing.T, handler http.Handler, method, path, deviceID, credential string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Authorization", "Device "+credential)
	request.Header.Set("X-Device-ID", deviceID)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeData[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var result envelope[T]
	decodeResponse(t, response, &result)
	return result.Data
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, target interface{}) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func assertStatus(t *testing.T, response *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if response.Code != expected {
		t.Fatalf("status %d, want %d; body=%s", response.Code, expected, response.Body.String())
	}
}

package gatewayservice

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	"github.com/zerlinpi/Ant-Browser/server/platform/realtime"
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
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
	profilesyncservice "github.com/zerlinpi/Ant-Browser/server/services/profile-sync-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	scheduleservice "github.com/zerlinpi/Ant-Browser/server/services/schedule-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

type dependency interface {
	Ping(context.Context) error
}

type principal struct {
	UserID    string
	SessionID string
}

type contextKey string

const principalKey contextKey = "principal"

type Gateway struct {
	auth            *authservice.Service
	workspaces      *workspaceservice.Service
	devices         *deviceservice.Service
	instances       *browserinstanceservice.Service
	tokens          security.Tokens
	dependency      dependency
	logger          *slog.Logger
	agentHub        *agentHub
	notificationHub *notificationHub
	realtime        realtime.Bus
	nodeID          string
	tasks           *taskservice.Service
	taskWake        taskwake.Bus
	fingerprints    *fingerprintservice.Service
	profiles        *profilesyncservice.Service
	workflows       *automationservice.Service
	accounts        *accountservice.Service
	proxies         *proxyservice.Service
	notifications   *notificationservice.Service
	schedules       *scheduleservice.Service
	analytics       *analyticsservice.Service
	admin           *adminservice.Service
	batch           *batchservice.Service
	billing         *billingservice.Service
	allowedOrigins  []string
	clientIPs       *httpx.ClientIPResolver
	limits          *rateLimiters
	// extensions holds feature dependencies registered in extensions.go.
	extensions map[interface{}]interface{}
}

// AllowedOrigins configures exact browser origins for authenticated CORS and
// notification WebSocket handshakes. Its distinct type avoids mistaking an
// arbitrary string slice for a service dependency in the compatibility
// options list.
type AllowedOrigins []string

func New(
	auth *authservice.Service,
	workspaces *workspaceservice.Service,
	devices *deviceservice.Service,
	instances *browserinstanceservice.Service,
	tokens security.Tokens,
	dependency dependency,
	logger *slog.Logger,
) http.Handler {
	return NewWithInfrastructure(
		context.Background(), auth, workspaces, devices, instances, tokens,
		dependency, realtime.NewDisabled(), nil, taskwake.NewDisabled(), nil, nil, nil, nil, nil, logger,
	)
}

func NewWithRealtime(
	ctx context.Context,
	auth *authservice.Service,
	workspaces *workspaceservice.Service,
	devices *deviceservice.Service,
	instances *browserinstanceservice.Service,
	tokens security.Tokens,
	dependency dependency,
	realtimeBus realtime.Bus,
	logger *slog.Logger,
) http.Handler {
	return NewWithInfrastructure(
		ctx, auth, workspaces, devices, instances, tokens,
		dependency, realtimeBus, nil, taskwake.NewDisabled(), nil, nil, nil, nil, nil, logger,
	)
}

func NewWithInfrastructure(
	ctx context.Context,
	auth *authservice.Service,
	workspaces *workspaceservice.Service,
	devices *deviceservice.Service,
	instances *browserinstanceservice.Service,
	tokens security.Tokens,
	dependency dependency,
	realtimeBus realtime.Bus,
	tasks *taskservice.Service,
	taskWake taskwake.Bus,
	fingerprints *fingerprintservice.Service,
	profiles *profilesyncservice.Service,
	workflows *automationservice.Service,
	accounts *accountservice.Service,
	proxies *proxyservice.Service,
	logger *slog.Logger,
	options ...interface{},
) http.Handler {
	if realtimeBus == nil {
		realtimeBus = realtime.NewDisabled()
	}
	if taskWake == nil {
		taskWake = taskwake.NewDisabled()
	}
	gateway := &Gateway{
		auth: auth, workspaces: workspaces, devices: devices, instances: instances,
		tokens: tokens, dependency: dependency, logger: logger, agentHub: newAgentHub(), notificationHub: newNotificationHub(),
		realtime: realtimeBus, nodeID: uuid.NewString(), tasks: tasks, taskWake: taskWake,
		fingerprints: fingerprints, profiles: profiles, workflows: workflows, accounts: accounts, proxies: proxies,
		extensions: make(map[interface{}]interface{}),
	}
	var trustedProxies TrustedProxies
	var rateLimits *RateLimits
	for _, option := range options {
		switch value := option.(type) {
		case *notificationservice.Service:
			gateway.notifications = value
		case *scheduleservice.Service:
			gateway.schedules = value
		case *analyticsservice.Service:
			gateway.analytics = value
		case *adminservice.Service:
			gateway.admin = value
		case *batchservice.Service:
			gateway.batch = value
		case *billingservice.Service:
			gateway.billing = value
		case AllowedOrigins:
			gateway.allowedOrigins = append([]string(nil), value...)
		case TrustedProxies:
			trustedProxies = append(TrustedProxies(nil), value...)
		case RateLimits:
			rateLimits = &value
		default:
			gateway.handleExtensionOption(option)
		}
	}
	gateway.clientIPs = gateway.newClientIPResolver(trustedProxies)
	gateway.limits = newRateLimiters(rateLimits)
	gateway.subscribeCommands(ctx)
	if gateway.notifications != nil {
		gateway.subscribeNotifications(ctx)
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", gateway.health)
	mux.HandleFunc("GET /readyz", gateway.ready)
	// Credential endpoints are rate limited per resolved client IP; login is
	// additionally limited per normalized email inside the handler.
	mux.HandleFunc("POST /api/v1/auth/register", gateway.limitByClientIP(gateway.limits.registerIP, gateway.register))
	mux.HandleFunc("POST /api/v1/auth/login", gateway.limitByClientIP(gateway.limits.loginIP, gateway.login))
	mux.HandleFunc("POST /api/v1/auth/refresh", gateway.limitByClientIP(gateway.limits.refreshIP, gateway.refresh))
	mux.HandleFunc("POST /api/v1/auth/mfa/verify", gateway.limitByClientIP(gateway.limits.mfaVerifyIP, gateway.verifyMFA))
	// Every device-credential route authenticates through
	// withAgentAuthentication, which turns infrastructure failures into 503
	// dependency_unavailable rather than a credential rejection.
	agentRoute := gateway.withAgentAuthentication
	mux.HandleFunc("GET /api/v1/agent/ws", agentRoute(gateway.agentSocket))
	if fingerprints != nil {
		mux.HandleFunc("GET /api/v1/agent/instances/{instanceID}/runtime-config", agentRoute(gateway.getAgentInstanceRuntimeConfig))
	}
	if profiles != nil {
		deviceProfile := func(next http.HandlerFunc) http.HandlerFunc {
			return agentRoute(gateway.deviceProfile(next))
		}
		mux.HandleFunc("GET /api/v1/agent/profiles/{profileID}", deviceProfile(gateway.getProfile))
		mux.HandleFunc("POST /api/v1/agent/profiles/{profileID}/lease", deviceProfile(gateway.acquireProfileLease))
		mux.HandleFunc("POST /api/v1/agent/profiles/{profileID}/lease/renew", deviceProfile(gateway.renewProfileLease))
		mux.HandleFunc("DELETE /api/v1/agent/profiles/{profileID}/lease", deviceProfile(gateway.releaseProfileLease))
		mux.HandleFunc("POST /api/v1/agent/profiles/{profileID}/revisions", deviceProfile(gateway.beginProfileRevision))
		mux.HandleFunc("GET /api/v1/agent/profiles/{profileID}/revisions", deviceProfile(gateway.listProfileRevisions))
		mux.HandleFunc("GET /api/v1/agent/profiles/{profileID}/revisions/{revisionID}", deviceProfile(gateway.getProfileRevision))
		mux.HandleFunc("POST /api/v1/agent/profiles/{profileID}/revisions/{revisionID}/objects/{objectID}/upload", deviceProfile(gateway.prepareProfileObjectUpload))
		mux.HandleFunc("GET /api/v1/agent/profiles/{profileID}/revisions/{revisionID}/objects/{objectID}/download", deviceProfile(gateway.prepareProfileObjectDownload))
		mux.HandleFunc("POST /api/v1/agent/profiles/{profileID}/revisions/{revisionID}/commit", deviceProfile(gateway.commitProfileRevision))
		mux.HandleFunc("POST /api/v1/agent/profiles/{profileID}/revisions/{revisionID}/restore", deviceProfile(gateway.restoreProfileRevision))
	}
	if gateway.billing != nil {
		mux.HandleFunc("GET /api/v1/billing/plans", gateway.listBillingPlans)
		mux.HandleFunc("GET /api/v1/billing/release-channels", gateway.listReleaseChannels)
	}
	if gateway.notifications != nil {
		mux.HandleFunc("GET /api/v1/notifications/ws", gateway.notificationSocket)
	}
	if tasks != nil {
		mux.HandleFunc("POST /api/v1/agent/tasks/claim", agentRoute(gateway.claimAgentTask))
		mux.HandleFunc("POST /api/v1/agent/tasks/{taskID}/report", agentRoute(gateway.reportAgentTask))
	}

	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/v1/me", gateway.me)
	protected.HandleFunc("GET /api/v1/me/sessions", gateway.listSessions)
	protected.HandleFunc("POST /api/v1/me/sessions/revoke-others", gateway.revokeOtherSessions)
	protected.HandleFunc("DELETE /api/v1/me/sessions/{sessionID}", gateway.revokeSession)
	protected.HandleFunc("GET /api/v1/me/mfa", gateway.getMFAStatus)
	protected.HandleFunc("DELETE /api/v1/me/mfa", gateway.disableMFA)
	protected.HandleFunc("POST /api/v1/me/mfa/totp/setup", gateway.setupTOTP)
	protected.HandleFunc("POST /api/v1/me/mfa/totp/confirm", gateway.confirmTOTP)
	protected.HandleFunc("POST /api/v1/me/mfa/recovery-codes", gateway.regenerateRecoveryCodes)
	protected.HandleFunc("POST /api/v1/auth/logout", gateway.logout)
	protected.HandleFunc("GET /api/v1/workspaces", gateway.listWorkspaces)
	protected.HandleFunc("POST /api/v1/workspaces", gateway.createWorkspace)
	protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}", gateway.getWorkspace)
	protected.HandleFunc("PATCH /api/v1/workspaces/{workspaceID}", gateway.updateWorkspace)
	protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/members", gateway.listMembers)
	protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/members", gateway.addMember)
	protected.HandleFunc("PATCH /api/v1/workspaces/{workspaceID}/members/{userID}", gateway.updateMember)
	protected.HandleFunc("DELETE /api/v1/workspaces/{workspaceID}/members/{userID}", gateway.removeMember)
	protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/invitations", gateway.listInvitations)
	protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/invitations", gateway.createInvitation)
	protected.HandleFunc("DELETE /api/v1/workspaces/{workspaceID}/invitations/{invitationID}", gateway.revokeInvitation)
	protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/invitations/accept", gateway.limitByClientIP(gateway.limits.acceptIP, gateway.acceptInvitation))
	protected.HandleFunc("GET /api/v1/devices", gateway.listDevices)
	protected.HandleFunc("POST /api/v1/devices", gateway.registerDevice)
	protected.HandleFunc("DELETE /api/v1/devices/{deviceID}", gateway.revokeDevice)
	protected.HandleFunc("POST /api/v1/devices/{deviceID}/rotate-credential", gateway.rotateDeviceCredential)
	protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/browser-instances", gateway.listInstances)
	protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/browser-instances", gateway.createInstance)
	protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/browser-instances/{instanceID}", gateway.getInstance)
	protected.HandleFunc("PATCH /api/v1/workspaces/{workspaceID}/browser-instances/{instanceID}", gateway.updateInstance)
	protected.HandleFunc("DELETE /api/v1/workspaces/{workspaceID}/browser-instances/{instanceID}", gateway.deleteInstance)
	protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/browser-instances/{instanceID}/clone", gateway.cloneInstance)
	protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/browser-instances/{instanceID}/commands", gateway.commandInstance)
	if tasks != nil {
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/tasks", gateway.listTasks)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/tasks", gateway.enqueueTask)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/tasks/{taskID}", gateway.getTask)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/tasks/{taskID}/cancel", gateway.cancelTask)
	}
	if fingerprints != nil {
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/fingerprint-presets", gateway.listFingerprintPresets)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/fingerprint-templates", gateway.listFingerprintTemplates)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/fingerprint-templates", gateway.createFingerprintTemplate)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/fingerprint-templates/batch", gateway.createFingerprintTemplateBatch)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/fingerprint-templates/{templateID}", gateway.getFingerprintTemplate)
		protected.HandleFunc("PATCH /api/v1/workspaces/{workspaceID}/fingerprint-templates/{templateID}", gateway.updateFingerprintTemplate)
		protected.HandleFunc("DELETE /api/v1/workspaces/{workspaceID}/fingerprint-templates/{templateID}", gateway.deleteFingerprintTemplate)
	}
	if profiles != nil {
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/profiles", gateway.listProfiles)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/profiles", gateway.createProfile)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/profiles/{profileID}", gateway.getProfile)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/profiles/{profileID}/lease", gateway.acquireProfileLease)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/profiles/{profileID}/lease/renew", gateway.renewProfileLease)
		protected.HandleFunc("DELETE /api/v1/workspaces/{workspaceID}/profiles/{profileID}/lease", gateway.releaseProfileLease)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions", gateway.beginProfileRevision)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions", gateway.listProfileRevisions)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions/{revisionID}", gateway.getProfileRevision)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions/{revisionID}/objects/{objectID}/upload", gateway.prepareProfileObjectUpload)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions/{revisionID}/objects/{objectID}/download", gateway.prepareProfileObjectDownload)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions/{revisionID}/commit", gateway.commitProfileRevision)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions/{revisionID}/restore", gateway.restoreProfileRevision)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/profiles/{profileID}/conflicts", gateway.listProfileConflicts)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/profiles/{profileID}/conflicts/{conflictID}/resolve", gateway.resolveProfileConflict)
	}
	if workflows != nil {
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/workflows", gateway.listWorkflows)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/workflows", gateway.createWorkflow)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/workflows/{workflowID}", gateway.getWorkflow)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/workflows/{workflowID}/versions", gateway.addWorkflowVersion)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/workflows/{workflowID}/versions/{version}", gateway.getWorkflowVersion)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/workflows/{workflowID}/publish", gateway.publishWorkflow)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/workflows/{workflowID}/archive", gateway.archiveWorkflow)
	}
	if gateway.schedules != nil {
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/schedules", gateway.listSchedules)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/schedules", gateway.createSchedule)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/schedules/{scheduleID}", gateway.getSchedule)
		protected.HandleFunc("PATCH /api/v1/workspaces/{workspaceID}/schedules/{scheduleID}", gateway.updateSchedule)
		protected.HandleFunc("DELETE /api/v1/workspaces/{workspaceID}/schedules/{scheduleID}", gateway.deleteSchedule)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/schedules/{scheduleID}/enable", gateway.enableSchedule)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/schedules/{scheduleID}/disable", gateway.disableSchedule)
	}
	if gateway.analytics != nil {
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/analytics/dashboard", gateway.analyticsDashboard)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/analytics/events", gateway.analyticsEvents)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/analytics/audit-events", gateway.analyticsAuditEvents)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/analytics/risk-events", gateway.analyticsRiskEvents)
	}
	if gateway.admin != nil {
		protected.HandleFunc("GET /api/v1/admin/users", gateway.adminListUsers)
		protected.HandleFunc("PATCH /api/v1/admin/users/{userID}/status", gateway.adminSetUserStatus)
		protected.HandleFunc("GET /api/v1/admin/organizations", gateway.adminListOrganizations)
		protected.HandleFunc("PATCH /api/v1/admin/organizations/{organizationID}/status", gateway.adminSetOrganizationStatus)
		protected.HandleFunc("GET /api/v1/admin/workspaces", gateway.adminListWorkspaces)
		protected.HandleFunc("PUT /api/v1/admin/platform-admins/{userID}", gateway.adminGrantPlatformAdmin)
		protected.HandleFunc("DELETE /api/v1/admin/platform-admins/{userID}", gateway.adminRevokePlatformAdmin)
	}
	if accounts != nil {
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/accounts", gateway.listAccounts)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/accounts", gateway.createAccount)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/accounts/{accountID}", gateway.getAccount)
		protected.HandleFunc("PATCH /api/v1/workspaces/{workspaceID}/accounts/{accountID}", gateway.updateAccount)
		protected.HandleFunc("DELETE /api/v1/workspaces/{workspaceID}/accounts/{accountID}", gateway.deleteAccount)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/accounts/{accountID}/status", gateway.setAccountStatus)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/accounts/{accountID}/risk-level", gateway.setAccountRiskLevel)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/accounts/{accountID}/secrets", gateway.listAccountSecrets)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/accounts/{accountID}/secrets", gateway.storeAccountSecret)
		protected.HandleFunc("DELETE /api/v1/workspaces/{workspaceID}/accounts/{accountID}/secrets/{secretID}", gateway.deleteAccountSecret)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/accounts/{accountID}/bindings", gateway.listAccountBindings)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/accounts/{accountID}/bindings", gateway.bindAccount)
		protected.HandleFunc("DELETE /api/v1/workspaces/{workspaceID}/accounts/{accountID}/bindings/{bindingID}", gateway.unbindAccount)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/accounts/{accountID}/risk-events", gateway.listAccountRiskEvents)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/accounts/{accountID}/risk-events", gateway.recordAccountRiskEvent)
	}
	if proxies != nil {
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/proxies", gateway.listProxies)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/proxies", gateway.createProxy)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/proxies/{proxyID}", gateway.getProxy)
		protected.HandleFunc("PATCH /api/v1/workspaces/{workspaceID}/proxies/{proxyID}", gateway.updateProxy)
		protected.HandleFunc("DELETE /api/v1/workspaces/{workspaceID}/proxies/{proxyID}", gateway.deleteProxy)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/proxies/{proxyID}/assignments", gateway.assignProxy)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/proxy-assignments", gateway.listProxyAssignments)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/proxy-assignments/{targetType}/{targetID}", gateway.getProxyAssignment)
		protected.HandleFunc("DELETE /api/v1/workspaces/{workspaceID}/proxy-assignments/{targetType}/{targetID}", gateway.unassignProxy)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/proxies/{proxyID}/health-checks", gateway.listProxyHealthChecks)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/proxies/{proxyID}/health-checks", gateway.requestProxyHealthCheck)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/proxy-health-checks/{checkID}", gateway.getProxyHealthCheck)
	}
	if gateway.notifications != nil {
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/notifications", gateway.listNotifications)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/notifications/unread-count", gateway.unreadNotificationCount)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/notifications/{notificationID}/read", gateway.markNotificationRead)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/notifications/read-all", gateway.markAllNotificationsRead)
		protected.HandleFunc("GET /api/v1/workspaces/{workspaceID}/notification-preferences", gateway.getNotificationPreferences)
		protected.HandleFunc("PUT /api/v1/workspaces/{workspaceID}/notification-preferences", gateway.updateNotificationPreferences)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/notifications/socket-ticket", gateway.issueNotificationSocketTicket)
	}
	if gateway.batch != nil {
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/batch/browser-instances", gateway.batchCreateInstances)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/batch/browser-instances/start", gateway.batchStartInstances)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/batch/browser-instances/stop", gateway.batchStopInstances)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/batch/account-bindings", gateway.batchBindAccounts)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/batch/proxy-assignments", gateway.batchAssignProxies)
		protected.HandleFunc("POST /api/v1/workspaces/{workspaceID}/batch/workflow-executions", gateway.batchExecuteWorkflows)
	}
	if gateway.billing != nil {
		protected.HandleFunc("GET /api/v1/organizations/{organizationID}/billing/subscription", gateway.currentSubscription)
		protected.HandleFunc("GET /api/v1/organizations/{organizationID}/billing/entitlements", gateway.listEntitlements)
		protected.HandleFunc("POST /api/v1/organizations/{organizationID}/billing/licenses/activate", gateway.activateLicense)
		protected.HandleFunc("POST /api/v1/organizations/{organizationID}/billing/licenses/validate", gateway.validateLicense)
		protected.HandleFunc("DELETE /api/v1/organizations/{organizationID}/billing/licenses/{activationID}", gateway.revokeLicense)
	}
	gateway.registerExtensionRoutes(mux, protected)
	mux.Handle("/api/v1/", gateway.authenticate(gateway.wrapProtected(protected)))

	return httpx.RequestIDMiddleware(
		httpx.SecurityHeaders(
			httpx.Recover(logger, httpx.AccessLog(logger, httpx.CORS(gateway.allowedOrigins, mux))),
		),
	)
}

func (g *Gateway) health(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ok", "service": "ant-browser-control-plane", "time": time.Now().UTC(),
	})
}

func (g *Gateway) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := g.dependency.Ping(ctx); err != nil {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusServiceUnavailable, Code: "dependency_unavailable", Message: "Control plane is not ready"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (g *Gateway) register(w http.ResponseWriter, r *http.Request) {
	var input authservice.RegisterInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	pair, err := g.auth.Register(r.Context(), input, g.requestMetadata(r))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": pair})
}

func (g *Gateway) login(w http.ResponseWriter, r *http.Request) {
	var input authservice.LoginInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	// The per-IP limit ran before decoding. The per-account limit slows
	// password guessing against one email spread across many IPs; it is
	// checked before any password hashing work.
	if key := loginEmailKey(input.Email); key != "" && !allowRequest(w, r, g.limits.loginEmail, key) {
		return
	}
	metadata := g.requestMetadata(r)
	metadata.DeviceID = strings.TrimSpace(input.DeviceID)
	result, err := g.auth.Login(r.Context(), input, metadata)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	// Accounts with two-factor authentication get a challenge for
	// /auth/mfa/verify; the sign-in is announced once it completes.
	if result.TokenPair != nil {
		g.publishLoginSecurityEvents(r.Context(), *result.TokenPair, metadata, "")
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

// publishLoginSecurityEvents announces a new session. secondFactor is the
// method that completed a two-factor sign-in, or empty.
func (g *Gateway) publishLoginSecurityEvents(ctx context.Context, pair authservice.TokenPair, metadata authservice.SessionMetadata, secondFactor string) {
	body := "A new session signed in to your Ant Browser account."
	payload := map[string]interface{}{
		"sessionId": pair.SessionID, "deviceId": metadata.DeviceID,
		"ipAddress": metadata.IPAddress, "userAgent": metadata.UserAgent,
	}
	if secondFactor != "" {
		payload["secondFactor"] = secondFactor
		if secondFactor == authservice.MFAMethodRecoveryCode {
			body = "A new session signed in to your Ant Browser account with a recovery code."
		}
	}
	g.publishSecurityEvent(ctx, pair.User.ID, "New account sign-in", body, payload, "security-login:"+pair.SessionID)
}

// publishSecurityEvent notifies the user in each of their workspaces. It is
// best effort: failures are logged and never fail the request.
func (g *Gateway) publishSecurityEvent(ctx context.Context, userID, title, body string, payload map[string]interface{}, idempotencyKey string) {
	if g.notifications == nil || g.workspaces == nil {
		return
	}
	workspaces, err := g.workspaces.List(ctx, userID)
	if err != nil {
		g.logger.WarnContext(ctx, "security_notification_workspace_list_failed", "user_id", userID, "error", err)
		return
	}
	for _, workspace := range workspaces {
		_, publishErr := g.notifications.Publish(ctx, notificationservice.CreateInput{
			WorkspaceID: workspace.ID, RecipientUserID: userID,
			EventType: "security.event", Title: title, Body: body,
			Payload: payload, IdempotencyKey: idempotencyKey,
		})
		if publishErr != nil {
			g.logger.WarnContext(ctx, "security_notification_publish_failed", "workspace_id", workspace.ID, "user_id", userID, "error", publishErr)
		}
	}
}

func (g *Gateway) refresh(w http.ResponseWriter, r *http.Request) {
	var input struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	pair, err := g.auth.Refresh(r.Context(), input.RefreshToken)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": pair})
}

func (g *Gateway) logout(w http.ResponseWriter, r *http.Request) {
	identity := mustPrincipal(r.Context())
	if err := g.auth.Logout(r.Context(), identity.UserID, identity.SessionID, "user_logout"); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	g.notificationHub.closeSessions(identity.UserID, identity.SessionID)
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) listSessions(w http.ResponseWriter, r *http.Request) {
	identity := mustPrincipal(r.Context())
	items, err := g.auth.ListSessions(r.Context(), identity.UserID, identity.SessionID)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) revokeSession(w http.ResponseWriter, r *http.Request) {
	identity := mustPrincipal(r.Context())
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if err := g.auth.RevokeUserSession(r.Context(), identity.UserID, identity.SessionID, sessionID); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	g.notificationHub.closeSessions(identity.UserID, sessionID)
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	identity := mustPrincipal(r.Context())
	revoked, err := g.auth.RevokeOtherSessions(r.Context(), identity.UserID, identity.SessionID)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	g.notificationHub.closeSessions(identity.UserID, revoked...)
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": map[string]int{"revoked": len(revoked)}})
}

func (g *Gateway) me(w http.ResponseWriter, r *http.Request) {
	user, err := g.auth.User(r.Context(), mustPrincipal(r.Context()).UserID)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": user})
}

func (g *Gateway) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	items, err := g.workspaces.List(r.Context(), mustPrincipal(r.Context()).UserID)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var input workspaceservice.CreateInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	workspace, err := g.workspaces.Create(r.Context(), mustPrincipal(r.Context()).UserID, input)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": workspace})
}

func (g *Gateway) getWorkspace(w http.ResponseWriter, r *http.Request) {
	workspace, err := g.workspaces.Get(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": workspace})
}

func (g *Gateway) updateWorkspace(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name    string `json:"name"`
		Version int64  `json:"version"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	workspace, err := g.workspaces.Update(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), input.Name, input.Version)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": workspace})
}

func (g *Gateway) listMembers(w http.ResponseWriter, r *http.Request) {
	items, err := g.workspaces.Members(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) addMember(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	role, ok := memberservice.ParseRole(input.Role)
	if !ok {
		httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "invalid_role", Message: "Role is invalid"})
		return
	}
	membership, err := g.workspaces.AddMember(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), input.UserID, role)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": membership})
}

func (g *Gateway) listDevices(w http.ResponseWriter, r *http.Request) {
	items, err := g.devices.List(r.Context(), mustPrincipal(r.Context()).UserID)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) registerDevice(w http.ResponseWriter, r *http.Request) {
	var input deviceservice.RegisterInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	registration, err := g.devices.Register(r.Context(), mustPrincipal(r.Context()).UserID, input)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": registration})
}

func (g *Gateway) revokeDevice(w http.ResponseWriter, r *http.Request) {
	deviceID := strings.TrimSpace(r.PathValue("deviceID"))
	if err := g.devices.Revoke(r.Context(), mustPrincipal(r.Context()).UserID, deviceID); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	// A revoked device must not keep a socket authenticated before revocation.
	g.disconnectAgentDevices(r.Context(), deviceID)
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) listInstances(w http.ResponseWriter, r *http.Request) {
	items, err := g.instances.List(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (g *Gateway) createInstance(w http.ResponseWriter, r *http.Request) {
	var input browserinstanceservice.CreateInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	instance, err := g.instances.Create(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), input)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": instance})
}

func (g *Gateway) getInstance(w http.ResponseWriter, r *http.Request) {
	instance, err := g.instances.Get(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("workspaceID"), r.PathValue("instanceID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": instance})
}

func (g *Gateway) updateInstance(w http.ResponseWriter, r *http.Request) {
	var input struct {
		browserinstanceservice.UpdateInput
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	instance, err := g.instances.Update(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("instanceID"), input.ExpectedVersion, input.UpdateInput,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": instance})
}

func (g *Gateway) deleteInstance(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ExpectedVersion int64 `json:"expectedVersion"`
	}
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := g.instances.Delete(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("instanceID"), input.ExpectedVersion,
	); err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) cloneInstance(w http.ResponseWriter, r *http.Request) {
	var input browserinstanceservice.CloneInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	instance, err := g.instances.Clone(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("instanceID"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]interface{}{"data": instance})
}

func (g *Gateway) commandInstance(w http.ResponseWriter, r *http.Request) {
	var input browserinstanceservice.CommandInput
	if err := httpx.DecodeJSON(w, r, &input); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	command, instance, err := g.instances.RequestCommand(
		r.Context(), mustPrincipal(r.Context()).UserID,
		r.PathValue("workspaceID"), r.PathValue("instanceID"), r.Header.Get("Idempotency-Key"), input,
	)
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	g.dispatchInstanceCommand(r.Context(), command)
	httpx.WriteJSON(w, http.StatusAccepted, map[string]interface{}{"data": map[string]interface{}{"command": command, "instance": instance}})
}

func (g *Gateway) dispatchInstanceCommand(ctx context.Context, command browserinstanceservice.Command) {
	if !g.agentHub.dispatch(command) && command.DeviceID != "" {
		payload, marshalErr := json.Marshal(command)
		if marshalErr != nil {
			g.logger.ErrorContext(ctx, "command_publish_encode_failed", "command_id", command.ID, "error", marshalErr)
		} else if publishErr := g.realtime.PublishCommand(ctx, command.DeviceID, payload); publishErr != nil {
			g.logger.ErrorContext(ctx, "command_publish_failed", "command_id", command.ID, "device_id", command.DeviceID, "error", publishErr)
		}
	}
}

func (g *Gateway) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "Bearer access token is required"})
			return
		}
		claims, err := g.tokens.ParseAccess(strings.TrimSpace(parts[1]))
		if err != nil || g.auth.ValidateSession(r.Context(), claims.UserID, claims.SessionID) != nil {
			httpx.WriteError(w, r, httpx.Problem{Status: http.StatusUnauthorized, Code: "invalid_access_token", Message: "Access token is invalid or expired"})
			return
		}
		identity := principal{UserID: claims.UserID, SessionID: claims.SessionID}
		requestContext := context.WithValue(r.Context(), principalKey, identity)
		// Every workspace HTTP route is scoped before it reaches a repository.
		// Store turns this request-local value into transaction-local PostgreSQL
		// settings for each operation.
		if workspaceID := strings.TrimSpace(r.PathValue("workspaceID")); workspaceID != "" {
			requestContext = postgres.WithTenantScope(requestContext, postgres.TenantScope{WorkspaceID: workspaceID, UserID: identity.UserID})
		} else if organizationID := strings.TrimSpace(r.PathValue("organizationID")); organizationID != "" {
			requestContext = postgres.WithTenantScope(requestContext, postgres.TenantScope{OrganizationID: organizationID, UserID: identity.UserID})
		} else {
			// User-scoped resources (for example GET /devices) still need a
			// database identity so RLS can constrain rows through memberships.
			requestContext = postgres.WithTenantScope(requestContext, postgres.TenantScope{UserID: identity.UserID})
		}
		request := r.WithContext(requestContext)
		if err := g.meterAPIRequest(requestContext, request, identity); err != nil {
			g.writeServiceError(w, request, err)
			return
		}
		next.ServeHTTP(w, request)
	})
}

// meterAPIRequest charges one api_calls unit for each authenticated tenant
// request. Workspace and organization membership is checked before charging,
// so unauthorized requests cannot consume another tenant's quota. Requests
// without a tenant path (for example /me and workspace creation) are not
// attributable to an organization and are intentionally not metered here.
func (g *Gateway) meterAPIRequest(ctx context.Context, r *http.Request, identity principal) error {
	if g.billing == nil {
		return nil
	}
	organizationID := strings.TrimSpace(r.PathValue("organizationID"))
	if workspaceID := strings.TrimSpace(r.PathValue("workspaceID")); workspaceID != "" {
		workspace, err := g.workspaces.Get(ctx, identity.UserID, workspaceID)
		if err != nil {
			// Let the route's own authorization and not-found behavior remain
			// authoritative; it will return the appropriate error below.
			return nil
		}
		organizationID = workspace.OrganizationID
	} else if organizationID != "" {
		if err := g.workspaces.RequireOrganization(ctx, organizationID, identity.UserID, memberservice.PermissionBillingRead); err != nil {
			return nil
		}
	}
	if organizationID == "" {
		return nil
	}
	now := time.Now().UTC()
	key := "api:" + uuid.NewString()
	reservation, err := g.billing.ReserveUsage(ctx, billingservice.ReserveUsageInput{
		OrganizationID: organizationID,
		WorkspaceID:    strings.TrimSpace(r.PathValue("workspaceID")),
		MetricCode:     billingservice.EntitlementAPICalls,
		Amount:         1,
		IdempotencyKey: key,
		ExpiresAt:      now.Add(time.Minute),
		Now:            now,
	})
	if err != nil {
		return err
	}
	// API calls are completed synchronously. WithoutCancel keeps usage from
	// being stranded when a client disconnects between reserve and commit.
	_, err = g.billing.CommitUsage(context.WithoutCancel(ctx), organizationID, reservation.ID, now)
	return err
}

func (g *Gateway) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	if problem, ok := g.mapExtensionError(w, r, err); ok {
		httpx.WriteError(w, r, problem)
		return
	}
	problem := httpx.Problem{Status: http.StatusInternalServerError, Code: "internal_error", Message: "The request could not be completed"}
	switch {
	case errors.Is(err, batchservice.ErrBatchTooLarge):
		problem = httpx.Problem{Status: http.StatusRequestEntityTooLarge, Code: "batch_too_large", Message: err.Error(), Details: map[string]int{"maxItems": batchservice.MaxBatchSize}}
	case errors.Is(err, batchservice.ErrEmptyBatch), errors.Is(err, batchservice.ErrIdempotencyKeyRequired), errors.Is(err, batchservice.ErrInvalidIdempotencyKey), errors.Is(err, batchservice.ErrInvalidAction), errors.Is(err, batchservice.ErrInvalidScope):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "invalid_batch", Message: err.Error()}
	case errors.Is(err, batchservice.ErrDependencyUnavailable):
		problem = httpx.Problem{Status: http.StatusServiceUnavailable, Code: "batch_dependency_unavailable", Message: "Required batch operation service is unavailable"}
	case errors.Is(err, context.DeadlineExceeded):
		problem = httpx.Problem{Status: http.StatusGatewayTimeout, Code: "deadline_exceeded", Message: "The batch operation deadline was exceeded"}
	case errors.Is(err, context.Canceled):
		problem = httpx.Problem{Status: http.StatusRequestTimeout, Code: "request_cancelled", Message: "The batch operation was cancelled"}
	case errors.Is(err, accountservice.ErrNotFound), errors.Is(err, proxyservice.ErrNotFound):
		problem = httpx.Problem{Status: http.StatusNotFound, Code: "not_found", Message: "Resource was not found"}
	case errors.Is(err, accountservice.ErrVersionConflict), errors.Is(err, proxyservice.ErrVersionConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "version_conflict", Message: "Resource changed; refresh before retrying"}
	case errors.Is(err, proxyservice.ErrAssignmentConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "proxy_assignment_conflict", Message: "The target is already assigned to another proxy"}
	case errors.Is(err, accountservice.ErrIdentifierConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "account_identifier_conflict", Message: err.Error()}
	case errors.Is(err, proxyservice.ErrNameConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "proxy_name_conflict", Message: err.Error()}
	case errors.Is(err, browserinstanceservice.ErrNameConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "instance_name_conflict", Message: err.Error()}
	case errors.Is(err, profilesyncservice.ErrNameConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "profile_name_conflict", Message: err.Error()}
	case errors.Is(err, automationservice.ErrNameConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "workflow_name_conflict", Message: err.Error()}
	case errors.Is(err, scheduleservice.ErrInvalidCron):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "invalid_cron_expression", Message: err.Error()}
	case errors.Is(err, scheduleservice.ErrInvalidTimezone):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "invalid_timezone", Message: err.Error()}
	case errors.Is(err, proxyservice.ErrInUse):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "proxy_in_use", Message: "Proxy has active assignments"}
	case errors.Is(err, proxyservice.ErrUnsupportedRoute), errors.Is(err, accountservice.ErrSecretRequired):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: err.Error()}
	case errors.Is(err, proxyservice.ErrSecretProvider), errors.Is(err, accountservice.ErrUnsupported):
		problem = httpx.Problem{Status: http.StatusServiceUnavailable, Code: "service_unavailable", Message: "Required service is unavailable"}
	case errors.Is(err, authservice.ErrMFALocked):
		retryAfter := 60
		var locked *authservice.MFALockedError
		if errors.As(err, &locked) {
			retryAfter = int(math.Ceil(time.Until(locked.Until).Seconds()))
		}
		if retryAfter < 1 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		problem = httpx.Problem{Status: http.StatusTooManyRequests, Code: "mfa_locked", Message: err.Error(), Details: map[string]int{"retryAfterSeconds": retryAfter}}
	// Second-factor failures on signed-in routes are 422, never 401: a 401
	// makes clients renew the access token and retry.
	case errors.Is(err, authservice.ErrMFAInvalidCode):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "mfa_invalid_code", Message: err.Error()}
	case errors.Is(err, authservice.ErrMFACodeRequired):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "mfa_code_required", Message: err.Error()}
	case errors.Is(err, authservice.ErrInvalidPassword):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "invalid_password", Message: err.Error()}
	case errors.Is(err, authservice.ErrMFAChallengeInvalid):
		problem = httpx.Problem{Status: http.StatusUnauthorized, Code: "mfa_challenge_invalid", Message: err.Error()}
	case errors.Is(err, authservice.ErrMFAAlreadyEnabled):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "mfa_already_enabled", Message: err.Error()}
	case errors.Is(err, authservice.ErrMFANotEnabled):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "mfa_not_enabled", Message: err.Error()}
	case errors.Is(err, authservice.ErrMFASetupRequired):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "mfa_setup_required", Message: err.Error()}
	case errors.Is(err, authservice.ErrMFAUnavailable):
		g.logger.ErrorContext(r.Context(), "mfa_unavailable", "request_id", httpx.RequestID(r.Context()), "error", err)
		problem = httpx.Problem{Status: http.StatusServiceUnavailable, Code: "mfa_unavailable", Message: "Two-factor authentication is unavailable"}
	case errors.Is(err, authservice.ErrInvalidCredentials):
		problem = httpx.Problem{Status: http.StatusUnauthorized, Code: "invalid_credentials", Message: "Email or password is incorrect"}
	case errors.Is(err, authservice.ErrEmailExists), errors.Is(err, workspaceservice.ErrMemberExists):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "resource_conflict", Message: err.Error()}
	case errors.Is(err, workspaceservice.ErrLastOwner):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "last_owner", Message: "The workspace must retain at least one owner"}
	case errors.Is(err, workspaceservice.ErrInvalidRole):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "invalid_role", Message: "Role must be admin, manager, operator, or viewer"}
	case errors.Is(err, authservice.ErrNotFound), errors.Is(err, workspaceservice.ErrNotFound), errors.Is(err, deviceservice.ErrNotFound), errors.Is(err, browserinstanceservice.ErrNotFound):
		problem = httpx.Problem{Status: http.StatusNotFound, Code: "not_found", Message: "Resource was not found"}
	case errors.Is(err, workspaceservice.ErrForbidden):
		problem = httpx.Problem{Status: http.StatusForbidden, Code: "forbidden", Message: "Workspace permission denied"}
	case errors.Is(err, workspaceservice.ErrInvitationInvalid), errors.Is(err, workspaceservice.ErrInvitationExpired), errors.Is(err, workspaceservice.ErrInvitationRevoked), errors.Is(err, workspaceservice.ErrInvitationUsed):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "invitation_invalid", Message: err.Error()}
	case errors.Is(err, billingservice.ErrQuotaExceeded):
		problem = httpx.Problem{Status: http.StatusPaymentRequired, Code: "quota_exceeded", Message: "The organization plan quota has been exhausted or the feature is unavailable"}
	case errors.Is(err, workspaceservice.ErrVersionConflict), errors.Is(err, browserinstanceservice.ErrVersionConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "version_conflict", Message: "Resource version is stale"}
	case errors.Is(err, browserinstanceservice.ErrStateConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "instance_state_conflict", Message: err.Error()}
	case errors.Is(err, browserinstanceservice.ErrIdempotencyConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "idempotency_conflict", Message: err.Error()}
	case errors.Is(err, browserinstanceservice.ErrInvalidInput), errors.Is(err, browserinstanceservice.ErrInvalidAction):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "instance_validation_failed", Message: err.Error()}
	case errors.Is(err, browserinstanceservice.ErrInvalidCommandTransition):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "command_transition_conflict", Message: err.Error()}
	case errors.Is(err, taskservice.ErrNotFound):
		problem = httpx.Problem{Status: http.StatusNotFound, Code: "not_found", Message: "Task was not found"}
	case errors.Is(err, taskservice.ErrStateConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "task_state_conflict", Message: "Task is already terminal or leased by another worker"}
	case errors.Is(err, taskservice.ErrQuotaExceeded):
		problem = httpx.Problem{Status: http.StatusPaymentRequired, Code: "quota_exceeded", Message: "The organization plan quota has been exhausted or the feature is unavailable"}
	case errors.Is(err, scheduleservice.ErrNotFound):
		problem = httpx.Problem{Status: http.StatusNotFound, Code: "not_found", Message: "Schedule was not found"}
	case errors.Is(err, scheduleservice.ErrVersionConflict), errors.Is(err, scheduleservice.ErrStateConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "schedule_state_conflict", Message: err.Error()}
	case errors.Is(err, notificationservice.ErrNotFound):
		problem = httpx.Problem{Status: http.StatusNotFound, Code: "not_found", Message: "Notification was not found"}
	case errors.Is(err, notificationservice.ErrConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "notification_conflict", Message: "Notification idempotency key was reused with different content"}
	case errors.Is(err, billingservice.ErrNotFound):
		problem = httpx.Problem{Status: http.StatusNotFound, Code: "billing_not_found", Message: "Billing resource was not found"}
	case errors.Is(err, billingservice.ErrInvalidInput):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "billing_validation_failed", Message: err.Error()}
	case errors.Is(err, billingservice.ErrQuotaExceeded):
		problem = httpx.Problem{Status: http.StatusPaymentRequired, Code: "quota_exceeded", Message: "The organization plan quota has been exhausted or the feature is unavailable"}
	case errors.Is(err, billingservice.ErrIdempotencyConflict), errors.Is(err, billingservice.ErrStateConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "billing_state_conflict", Message: err.Error()}
	case errors.Is(err, billingservice.ErrLicenseInvalid):
		problem = httpx.Problem{Status: http.StatusUnauthorized, Code: "license_invalid", Message: "License token is invalid"}
	case errors.Is(err, billingservice.ErrLicenseExpired):
		problem = httpx.Problem{Status: http.StatusForbidden, Code: "license_expired", Message: "License has expired"}
	case errors.Is(err, billingservice.ErrLicenseRevoked):
		problem = httpx.Problem{Status: http.StatusForbidden, Code: "license_revoked", Message: "License has been revoked"}
	case errors.Is(err, billingservice.ErrLicenseScope):
		problem = httpx.Problem{Status: http.StatusForbidden, Code: "license_scope_mismatch", Message: "License does not match this organization, workspace, or device"}
	case errors.Is(err, analyticsservice.ErrInvalidWindow), errors.Is(err, analyticsservice.ErrInvalidQuery):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: err.Error()}
	case errors.Is(err, adminservice.ErrForbidden):
		problem = httpx.Problem{Status: http.StatusForbidden, Code: "platform_admin_required", Message: "Platform administrator permission is required"}
	case errors.Is(err, adminservice.ErrNotFound):
		problem = httpx.Problem{Status: http.StatusNotFound, Code: "not_found", Message: "Admin resource was not found"}
	case errors.Is(err, adminservice.ErrInvalidStatus), errors.Is(err, adminservice.ErrInvalidRole):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: err.Error()}
	case errors.Is(err, adminservice.ErrSelfModification), errors.Is(err, adminservice.ErrLastPlatformAdmin):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "admin_state_conflict", Message: err.Error()}
	case errors.Is(err, fingerprintservice.ErrNotFound):
		problem = httpx.Problem{Status: http.StatusNotFound, Code: "not_found", Message: "Fingerprint template was not found"}
	case errors.Is(err, fingerprintservice.ErrVersionConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "version_conflict", Message: "Fingerprint template version is stale"}
	case errors.Is(err, fingerprintservice.ErrInUse):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "fingerprint_in_use", Message: err.Error()}
	case errors.Is(err, fingerprintservice.ErrNameConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "fingerprint_name_conflict", Message: err.Error()}
	case errors.Is(err, fingerprintservice.ErrBatchTooLarge):
		problem = httpx.Problem{Status: http.StatusRequestEntityTooLarge, Code: "fingerprint_batch_too_large", Message: err.Error(), Details: map[string]int{"maxItems": fingerprintservice.MaxBatchTemplates}}
	case errors.Is(err, fingerprintservice.ErrEmptyBatch), errors.Is(err, fingerprintservice.ErrInvalidInput):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "fingerprint_validation_failed", Message: err.Error()}
	case errors.Is(err, profilesyncservice.ErrNotFound):
		problem = httpx.Problem{Status: http.StatusNotFound, Code: "not_found", Message: "Cloud profile resource was not found"}
	case errors.Is(err, profilesyncservice.ErrVersionConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "version_conflict", Message: "Cloud profile version is stale"}
	case errors.Is(err, profilesyncservice.ErrLeaseHeld):
		problem = httpx.Problem{Status: http.StatusLocked, Code: "profile_lease_held", Message: err.Error()}
	case errors.Is(err, profilesyncservice.ErrLeaseInvalid):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "profile_lease_invalid", Message: err.Error()}
	case errors.Is(err, profilesyncservice.ErrRevisionConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "profile_revision_conflict", Message: err.Error()}
	case errors.Is(err, profilesyncservice.ErrConflictUnresolved):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "profile_conflict_unresolved", Message: "Resolve the open profile synchronization conflict first"}
	case errors.Is(err, profilesyncservice.ErrRevisionState):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "profile_revision_state", Message: err.Error()}
	case errors.Is(err, profilesyncservice.ErrObjectUnavailable):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "profile_object_unavailable", Message: err.Error()}
	case errors.Is(err, profilesyncservice.ErrStorageQuotaExceeded):
		problem = httpx.Problem{Status: http.StatusPaymentRequired, Code: "quota_exceeded", Message: "The organization profile storage quota is exhausted or unavailable"}
	case errors.Is(err, profilesyncservice.ErrDeviceScope):
		problem = httpx.Problem{Status: http.StatusForbidden, Code: "profile_device_scope", Message: "Profile operation is outside the authenticated device scope"}
	case errors.Is(err, automationservice.ErrNotFound):
		problem = httpx.Problem{Status: http.StatusNotFound, Code: "not_found", Message: "Workflow was not found"}
	case errors.Is(err, automationservice.ErrVersionConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "version_conflict", Message: "Workflow version is stale"}
	case errors.Is(err, automationservice.ErrStateConflict):
		problem = httpx.Problem{Status: http.StatusConflict, Code: "workflow_state_conflict", Message: err.Error()}
	case errors.Is(err, security.ErrWeakPassword):
		problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "weak_password", Message: security.ErrWeakPassword.Error()}
	case errors.Is(err, authservice.ErrUserDisabled), errors.Is(err, deviceservice.ErrRevoked):
		problem = httpx.Problem{Status: http.StatusForbidden, Code: "resource_disabled", Message: err.Error()}
	default:
		if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "must be") {
			problem = httpx.Problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: err.Error()}
		} else {
			g.logger.ErrorContext(r.Context(), "request_failed", "request_id", httpx.RequestID(r.Context()), "error", err)
		}
	}
	httpx.WriteError(w, r, problem)
}

// requestMetadata records the resolved client IP (see TrustedProxies) and user
// agent for sessions and login security events. The IP is always a parsed
// address or empty, never raw header text.
func (g *Gateway) requestMetadata(r *http.Request) authservice.SessionMetadata {
	return authservice.SessionMetadata{UserAgent: r.UserAgent(), IPAddress: g.clientIP(r)}
}

func mustPrincipal(ctx context.Context) principal {
	value, _ := ctx.Value(principalKey).(principal)
	return value
}

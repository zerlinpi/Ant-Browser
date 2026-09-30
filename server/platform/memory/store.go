package memory

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	adminservice "github.com/zerlinpi/Ant-Browser/server/services/admin-service"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	billingservice "github.com/zerlinpi/Ant-Browser/server/services/billing-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	fingerprintservice "github.com/zerlinpi/Ant-Browser/server/services/fingerprint-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
	profilesyncservice "github.com/zerlinpi/Ant-Browser/server/services/profile-sync-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

// Store is a concurrency-safe development adapter. Production must use the
// PostgreSQL adapter; this store exists for local evaluation and fast tests.
type Store struct {
	mu sync.RWMutex

	users       map[string]authservice.User
	userByEmail map[string]string
	sessions    map[string]authservice.Session
	refresh     map[string]authservice.RefreshToken

	mfaFactors       map[string]authservice.MFAFactor
	mfaRecoveryCodes map[string][]memoryRecoveryCode
	mfaChallenges    map[string]authservice.MFAChallenge

	organizations    map[string]workspaceservice.Organization
	workspaces       map[string]workspaceservice.Workspace
	memberships      map[string]workspaceservice.Membership
	invitations      map[string]workspaceservice.Invitation
	invitationHashes map[string]string

	devices           map[string]deviceservice.Device
	deviceCredentials map[string]deviceservice.Credential

	instances          map[string]browserinstanceservice.BrowserInstance
	commands           map[string]browserinstanceservice.Command
	commandIdempotency map[string]string

	tasks             map[string]taskservice.Task
	taskIdempotency   map[string]string
	taskAttempts      map[string]int
	taskRunIDs        map[string]string
	taskRuns          map[string]memoryTaskRun
	taskAttemptStates map[string]memoryTaskAttempt
	taskLeases        map[string]taskservice.Lease
	taskLeaseReceipts map[string]taskservice.Lease

	fingerprintTemplates map[string]fingerprintservice.Template
	proxies              map[string]proxyservice.Proxy
	proxyAssignments     map[string]proxyservice.Assignment
	proxyHealthChecks    map[string]proxyservice.HealthCheck

	cloudProfiles      map[string]profilesyncservice.Profile
	profileLeases      map[string]memoryProfileLease
	profileRevisions   map[string]profilesyncservice.Revision
	profileRevisionIDs map[string][]string
	profileManifests   map[string]profilesyncservice.Manifest
	profileObjects     map[string][]profilesyncservice.Object
	profileConflicts   map[string]profilesyncservice.Conflict
	profileConflictIDs map[string][]string
	// profileStorageFiles is the materialized current file set for each
	// profile. Keeping this state makes incremental revisions and replacements
	// cheap and lets the in-memory adapter exercise the same quota semantics as
	// PostgreSQL.
	profileStorageFiles map[string]map[string]memoryProfileStorageFile
	profileStorageUsage map[string]int64

	notifications           map[string]notificationservice.Notification
	notificationIdempotency map[string]string
	notificationPreferences map[string]notificationservice.NotificationPreference
	platformAdmins          map[string]adminservice.PlatformAdmin
	adminAudit              []adminservice.AuditEvent

	plans              map[string]billingservice.Plan
	planByCode         map[string]string
	subscriptions      map[string]billingservice.Subscription
	entitlements       map[string]billingservice.Entitlement
	usageCounters      map[string]billingservice.UsageCounter
	usageReservations  map[string]billingservice.UsageReservation
	usageByIdempotency map[string]string
	licenses           map[string]billingservice.LicenseActivation
	licenseByHash      map[string]string
	releaseChannels    map[string]billingservice.ReleaseChannel
	releaseByCode      map[string]string

	// features holds per-feature state; see extensions.go. featuresMu guards
	// the map itself; the state inside is guarded by mu.
	featuresMu sync.Mutex
	features   map[interface{}]interface{}
}

func New() *Store {
	store := &Store{
		users: make(map[string]authservice.User), userByEmail: make(map[string]string),
		sessions: make(map[string]authservice.Session), refresh: make(map[string]authservice.RefreshToken),
		mfaFactors: make(map[string]authservice.MFAFactor), mfaRecoveryCodes: make(map[string][]memoryRecoveryCode),
		mfaChallenges: make(map[string]authservice.MFAChallenge),
		organizations: make(map[string]workspaceservice.Organization), workspaces: make(map[string]workspaceservice.Workspace),
		memberships: make(map[string]workspaceservice.Membership),
		invitations: make(map[string]workspaceservice.Invitation), invitationHashes: make(map[string]string),
		devices: make(map[string]deviceservice.Device), deviceCredentials: make(map[string]deviceservice.Credential),
		instances: make(map[string]browserinstanceservice.BrowserInstance), commands: make(map[string]browserinstanceservice.Command),
		commandIdempotency: make(map[string]string),
		tasks:              make(map[string]taskservice.Task), taskIdempotency: make(map[string]string),
		taskAttempts: make(map[string]int), taskRunIDs: make(map[string]string),
		taskRuns: make(map[string]memoryTaskRun), taskAttemptStates: make(map[string]memoryTaskAttempt),
		taskLeases:           make(map[string]taskservice.Lease),
		taskLeaseReceipts:    make(map[string]taskservice.Lease),
		fingerprintTemplates: make(map[string]fingerprintservice.Template),
		proxies:              make(map[string]proxyservice.Proxy), proxyAssignments: make(map[string]proxyservice.Assignment), proxyHealthChecks: make(map[string]proxyservice.HealthCheck),
		cloudProfiles: make(map[string]profilesyncservice.Profile), profileLeases: make(map[string]memoryProfileLease),
		profileRevisions: make(map[string]profilesyncservice.Revision), profileRevisionIDs: make(map[string][]string),
		profileManifests: make(map[string]profilesyncservice.Manifest), profileObjects: make(map[string][]profilesyncservice.Object),
		profileConflicts: make(map[string]profilesyncservice.Conflict), profileConflictIDs: make(map[string][]string),
		profileStorageFiles: make(map[string]map[string]memoryProfileStorageFile), profileStorageUsage: make(map[string]int64),
		notifications: make(map[string]notificationservice.Notification), notificationIdempotency: make(map[string]string),
		notificationPreferences: make(map[string]notificationservice.NotificationPreference),
		platformAdmins:          make(map[string]adminservice.PlatformAdmin), adminAudit: make([]adminservice.AuditEvent, 0),
		plans: make(map[string]billingservice.Plan), planByCode: make(map[string]string),
		subscriptions: make(map[string]billingservice.Subscription), entitlements: make(map[string]billingservice.Entitlement),
		usageCounters: make(map[string]billingservice.UsageCounter), usageReservations: make(map[string]billingservice.UsageReservation), usageByIdempotency: make(map[string]string),
		licenses: make(map[string]billingservice.LicenseActivation), licenseByHash: make(map[string]string),
		releaseChannels: make(map[string]billingservice.ReleaseChannel), releaseByCode: make(map[string]string),
		features: make(map[interface{}]interface{}),
	}
	// Keep local development useful out of the box while production seeds the
	// same catalog through an explicitly trusted bootstrap operation.
	for _, plan := range billingservice.DefaultPlanCatalog(time.Now().UTC()) {
		store.plans[plan.ID] = plan
		store.planByCode[plan.Code] = plan.ID
	}
	return store
}

type memoryProfileLease struct {
	lease profilesyncservice.Lease
	hash  string
}

type memoryProfileStorageFile struct {
	WorkspaceID string
	ObjectKey   string
	SizeBytes   int64
	RevisionID  string
}

type memoryTaskRun struct {
	status     string
	finishedAt *time.Time
	errorCode  string
}

type memoryTaskAttempt struct {
	runID      string
	status     string
	finishedAt *time.Time
	errorCode  string
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func (s *Store) Ping(context.Context) error { return nil }
func (s *Store) Close() error               { return nil }

func (s *Store) CreateUser(_ context.Context, user authservice.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	email := strings.ToLower(user.Email)
	if _, exists := s.userByEmail[email]; exists {
		return authservice.ErrEmailExists
	}
	s.users[user.ID] = cloneUser(user)
	s.userByEmail[email] = user.ID
	return nil
}

func (s *Store) FindUserByEmail(_ context.Context, email string) (authservice.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.userByEmail[strings.ToLower(email)]
	if !ok {
		return authservice.User{}, authservice.ErrNotFound
	}
	return cloneUser(s.users[id]), nil
}

func (s *Store) FindUserByID(_ context.Context, id string) (authservice.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	user, ok := s.users[id]
	if !ok {
		return authservice.User{}, authservice.ErrNotFound
	}
	return cloneUser(user), nil
}

func (s *Store) SaveSession(_ context.Context, session authservice.Session, refresh authservice.RefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[session.UserID]; !ok {
		return authservice.ErrNotFound
	}
	if _, exists := s.refresh[refresh.TokenHash]; exists {
		return errors.New("refresh token collision")
	}
	s.sessions[session.ID] = session
	s.refresh[refresh.TokenHash] = refresh
	return nil
}

func (s *Store) FindRefreshToken(_ context.Context, hash string) (authservice.RefreshToken, authservice.Session, authservice.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	token, ok := s.refresh[hash]
	if !ok {
		return authservice.RefreshToken{}, authservice.Session{}, authservice.User{}, authservice.ErrNotFound
	}
	session, ok := s.sessions[token.SessionID]
	if !ok {
		return authservice.RefreshToken{}, authservice.Session{}, authservice.User{}, authservice.ErrNotFound
	}
	user, ok := s.users[session.UserID]
	if !ok {
		return authservice.RefreshToken{}, authservice.Session{}, authservice.User{}, authservice.ErrNotFound
	}
	return token, session, cloneUser(user), nil
}

func (s *Store) RotateRefreshToken(_ context.Context, currentHash string, next authservice.RefreshToken) (authservice.Session, authservice.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.refresh[currentHash]
	now := time.Now().UTC()
	if !ok || current.ConsumedAt != nil || current.RevokedAt != nil || !current.ExpiresAt.After(now) {
		return authservice.Session{}, authservice.User{}, authservice.ErrInvalidCredentials
	}
	session, ok := s.sessions[current.SessionID]
	if !ok || session.RevokedAt != nil || !session.ExpiresAt.After(now) {
		return authservice.Session{}, authservice.User{}, authservice.ErrSessionRevoked
	}
	if _, exists := s.refresh[next.TokenHash]; exists {
		return authservice.Session{}, authservice.User{}, errors.New("refresh token collision")
	}
	current.ConsumedAt = &now
	current.ReplacedByID = next.ID
	s.refresh[currentHash] = current
	s.refresh[next.TokenHash] = next
	session.LastSeenAt = now
	s.sessions[session.ID] = session
	user, ok := s.users[session.UserID]
	if !ok {
		return authservice.Session{}, authservice.User{}, authservice.ErrNotFound
	}
	return session, cloneUser(user), nil
}

func (s *Store) RevokeSession(_ context.Context, userID, sessionID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[sessionID]
	if !ok || session.UserID != userID {
		return authservice.ErrNotFound
	}
	now := time.Now().UTC()
	session.RevokedAt = &now
	session.RevokeReason = reason
	s.sessions[sessionID] = session
	for hash, token := range s.refresh {
		if token.SessionID == sessionID && token.RevokedAt == nil {
			token.RevokedAt = &now
			s.refresh[hash] = token
		}
	}
	return nil
}

func (s *Store) SessionActive(_ context.Context, userID, sessionID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok || session.UserID != userID {
		return false, nil
	}
	user, userExists := s.users[userID]
	return userExists && user.Status == "active" && session.RevokedAt == nil && session.ExpiresAt.After(time.Now().UTC()), nil
}

func (s *Store) ListActiveSessions(_ context.Context, userID string, now time.Time, limit int) ([]authservice.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]authservice.Session, 0)
	for _, session := range s.sessions {
		if session.UserID == userID && session.RevokedAt == nil && session.ExpiresAt.After(now) {
			items = append(items, session)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].LastSeenAt.Equal(items[j].LastSeenAt) {
			return items[i].LastSeenAt.After(items[j].LastSeenAt)
		}
		return items[i].ID < items[j].ID
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Store) RevokeOtherSessions(_ context.Context, userID, keepSessionID, reason string, now time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	revoked := make([]string, 0)
	for id, session := range s.sessions {
		if session.UserID != userID || id == keepSessionID || session.RevokedAt != nil || !session.ExpiresAt.After(now) {
			continue
		}
		revokedAt := now
		session.RevokedAt, session.RevokeReason = &revokedAt, reason
		s.sessions[id] = session
		revoked = append(revoked, id)
	}
	for hash, token := range s.refresh {
		for _, id := range revoked {
			if token.SessionID == id && token.RevokedAt == nil {
				revokedAt := now
				token.RevokedAt = &revokedAt
				s.refresh[hash] = token
				break
			}
		}
	}
	sort.Strings(revoked)
	return revoked, nil
}

func (s *Store) CreateOrganizationWorkspace(_ context.Context, organization workspaceservice.Organization, workspace workspaceservice.Workspace, membership workspaceservice.Membership) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[membership.UserID]; !ok {
		return authservice.ErrNotFound
	}
	if organization.Status == "" {
		organization.Status = "active"
	}
	// Role is a per-caller projection and is never persisted (PostgreSQL has
	// no such column).
	workspace.Role = ""
	s.organizations[organization.ID] = organization
	s.workspaces[workspace.ID] = workspace
	s.memberships[membershipKey(workspace.ID, membership.UserID)] = storedMembership(membership)
	s.provisionFreeBillingLocked(organization.ID, organization.CreatedAt)
	return nil
}

func (s *Store) provisionFreeBillingLocked(organizationID string, now time.Time) {
	planID, ok := s.planByCode[billingservice.PlanFree]
	if !ok {
		return
	}
	plan := s.plans[planID]
	if now.IsZero() {
		now = time.Now().UTC()
	}
	subscription := billingservice.Subscription{
		ID: billingMemoryID(), OrganizationID: organizationID, PlanID: plan.ID,
		Status: "active", Provider: "internal", ProviderSubscriptionRef: "free:" + organizationID,
		CurrentPeriodStart: now, CurrentPeriodEnd: now.AddDate(100, 0, 0), CreatedAt: now, UpdatedAt: now,
	}
	s.subscriptions[subscription.ID] = subscription
	for _, configured := range plan.Entitlements {
		entitlement := billingservice.Entitlement{
			ID: billingMemoryID(), OrganizationID: organizationID, Code: configured.Code,
			SourceSubscriptionID: subscription.ID, Limit: configured.Limit,
			FeatureEnabled: configured.FeatureEnabled, ValidFrom: now,
		}
		s.entitlements[organizationID+"|"+configured.Code] = entitlement
	}
}

func billingMemoryID() string { return uuid.NewString() }

func (s *Store) ListWorkspaces(_ context.Context, userID string) ([]workspaceservice.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]workspaceservice.Workspace, 0)
	for _, membership := range s.memberships {
		if membership.UserID != userID || membership.Status != "active" {
			continue
		}
		workspace, ok := s.workspaces[membership.WorkspaceID]
		organization, organizationExists := s.organizations[workspace.OrganizationID]
		if ok && workspace.DeletedAt == nil && workspace.Status == "active" && organizationExists && organization.Status != "suspended" && organization.Status != "deleted" {
			workspace.Role = membership.Role
			items = append(items, workspace)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *Store) FindWorkspace(_ context.Context, id string) (workspaceservice.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	workspace, ok := s.workspaces[id]
	organization, organizationExists := s.organizations[workspace.OrganizationID]
	if !ok || workspace.DeletedAt != nil || workspace.Status == "suspended" || !organizationExists || organization.Status == "suspended" || organization.Status == "deleted" {
		return workspaceservice.Workspace{}, workspaceservice.ErrNotFound
	}
	return workspace, nil
}

func (s *Store) UpdateWorkspace(_ context.Context, workspace workspaceservice.Workspace, expectedVersion int64) (workspaceservice.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.workspaces[workspace.ID]
	organization, organizationExists := s.organizations[current.OrganizationID]
	if !ok || current.DeletedAt != nil || current.Status == "suspended" || !organizationExists || organization.Status == "suspended" || organization.Status == "deleted" {
		return workspaceservice.Workspace{}, workspaceservice.ErrNotFound
	}
	if expectedVersion <= 0 || current.Version != expectedVersion {
		return workspaceservice.Workspace{}, workspaceservice.ErrVersionConflict
	}
	workspace.Version = current.Version + 1
	workspace.Role = ""
	s.workspaces[workspace.ID] = workspace
	return workspace, nil
}

func (s *Store) FindMembership(_ context.Context, workspaceID, userID string) (workspaceservice.Membership, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	membership, ok := s.memberships[membershipKey(workspaceID, userID)]
	if !ok {
		return workspaceservice.Membership{}, workspaceservice.ErrNotFound
	}
	return membership, nil
}

func (s *Store) FindOrganizationMembership(_ context.Context, organizationID, userID string) (workspaceservice.OrganizationMembership, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	organization, ok := s.organizations[organizationID]
	user, userOK := s.users[userID]
	if !ok || !userOK || organization.Status != "active" || user.Status != "active" {
		return workspaceservice.OrganizationMembership{}, workspaceservice.ErrNotFound
	}
	best := memberservice.Role("")
	for _, membership := range s.memberships {
		workspace, exists := s.workspaces[membership.WorkspaceID]
		if !exists || workspace.OrganizationID != organizationID || workspace.Status != "active" || workspace.DeletedAt != nil || membership.UserID != userID || membership.Status != "active" {
			continue
		}
		if organizationRoleRank(membership.Role) > organizationRoleRank(best) {
			best = membership.Role
		}
	}
	if best == "" {
		return workspaceservice.OrganizationMembership{}, workspaceservice.ErrNotFound
	}
	return workspaceservice.OrganizationMembership{OrganizationID: organizationID, UserID: userID, Role: best, Status: "active"}, nil
}

func organizationRoleRank(role memberservice.Role) int {
	switch role {
	case memberservice.RoleOwner:
		return 5
	case memberservice.RoleAdmin:
		return 4
	case memberservice.RoleManager:
		return 3
	case memberservice.RoleOperator:
		return 2
	case memberservice.RoleViewer:
		return 1
	default:
		return 0
	}
}

func (s *Store) ListMembers(_ context.Context, workspaceID string) ([]workspaceservice.Membership, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.workspaces[workspaceID]; !ok {
		return nil, workspaceservice.ErrNotFound
	}
	items := make([]workspaceservice.Membership, 0)
	for _, membership := range s.memberships {
		if membership.WorkspaceID == workspaceID && membership.Status == "active" {
			items = append(items, s.memberWithProfileLocked(membership))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].JoinedAt.Equal(items[j].JoinedAt) {
			return items[i].UserID < items[j].UserID
		}
		return items[i].JoinedAt.Before(items[j].JoinedAt)
	})
	return items, nil
}

// AddMember inserts a membership. A previously removed membership of the same
// user is re-activated in place (matching PostgreSQL's upsert); an active or
// invited one is a conflict.
func (s *Store) AddMember(_ context.Context, membership workspaceservice.Membership) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, ok := s.workspaces[membership.WorkspaceID]
	if !ok {
		return workspaceservice.ErrNotFound
	}
	if _, ok := s.users[membership.UserID]; !ok {
		return authservice.ErrNotFound
	}
	key := membershipKey(membership.WorkspaceID, membership.UserID)
	if existing, exists := s.memberships[key]; exists && existing.Status != "removed" {
		return workspaceservice.ErrMemberExists
	}
	if membership.Status == "active" && !s.organizationHasMemberLocked(workspace.OrganizationID, membership.UserID) {
		limit, allowed := s.activeEntitlementLimitLocked(workspace.OrganizationID, billingservice.EntitlementTeamMembers)
		if !allowed || (limit != nil && s.organizationMemberCountLocked(workspace.OrganizationID)+1 > *limit) {
			return billingservice.ErrQuotaExceeded
		}
	}
	s.memberships[key] = storedMembership(membership)
	return nil
}

// UpdateMemberRole changes the role of an active membership. Demoting the last
// active owner fails with ErrLastOwner.
func (s *Store) UpdateMemberRole(_ context.Context, workspaceID, userID string, role memberservice.Role) (workspaceservice.Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := membershipKey(workspaceID, userID)
	membership, ok := s.memberships[key]
	if !ok || membership.Status != "active" {
		return workspaceservice.Membership{}, workspaceservice.ErrNotFound
	}
	if membership.Role == memberservice.RoleOwner && role != memberservice.RoleOwner && s.ownerCountLocked(workspaceID) <= 1 {
		return workspaceservice.Membership{}, workspaceservice.ErrLastOwner
	}
	membership.Role = role
	membership.UpdatedAt = time.Now().UTC()
	s.memberships[key] = membership
	return s.memberWithProfileLocked(membership), nil
}

// RemoveMember marks an active membership removed and, under the same lock,
// revokes the member's devices and device credentials in the workspace so
// their agents lose access together with the membership. The removed row is
// kept (as in PostgreSQL, where notifications reference it); AddMember and
// AcceptInvitation re-activate it.
func (s *Store) RemoveMember(_ context.Context, workspaceID, userID string, now time.Time) (workspaceservice.MemberRemoval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := membershipKey(workspaceID, userID)
	membership, ok := s.memberships[key]
	if !ok || membership.Status != "active" {
		return workspaceservice.MemberRemoval{}, workspaceservice.ErrNotFound
	}
	if membership.Role == memberservice.RoleOwner && s.ownerCountLocked(workspaceID) <= 1 {
		return workspaceservice.MemberRemoval{}, workspaceservice.ErrLastOwner
	}
	membership.Status = "removed"
	membership.UpdatedAt = now
	s.memberships[key] = membership
	revoked := make([]string, 0)
	for id, device := range s.devices {
		if device.WorkspaceID != workspaceID || device.UserID != userID {
			continue
		}
		if device.RevokedAt == nil {
			device.Status = "revoked"
			device.RevokedAt = timePtr(now)
			device.UpdatedAt = now
			s.devices[id] = device
			revoked = append(revoked, id)
		}
		if credential, exists := s.deviceCredentials[id]; exists && credential.RevokedAt == nil {
			credential.RevokedAt = timePtr(now)
			s.deviceCredentials[id] = credential
		}
	}
	sort.Strings(revoked)
	return workspaceservice.MemberRemoval{Membership: membership, RevokedDeviceIDs: revoked}, nil
}

// storedMembership drops response-only projections before persisting.
func storedMembership(membership workspaceservice.Membership) workspaceservice.Membership {
	membership.Email = ""
	membership.DisplayName = ""
	return membership
}

// memberWithProfileLocked adds the member's user email and display name.
func (s *Store) memberWithProfileLocked(membership workspaceservice.Membership) workspaceservice.Membership {
	if user, ok := s.users[membership.UserID]; ok {
		membership.Email = user.Email
		membership.DisplayName = user.DisplayName
	}
	return membership
}

func (s *Store) CreateInvitation(_ context.Context, invitation workspaceservice.Invitation, tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workspaces[invitation.WorkspaceID]; !ok {
		return workspaceservice.ErrNotFound
	}
	invitation.Token = ""
	s.invitations[invitation.ID] = invitation
	s.invitationHashes[invitation.ID] = tokenHash
	return nil
}
func (s *Store) ListInvitations(_ context.Context, workspaceID string) ([]workspaceservice.Invitation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.workspaces[workspaceID]; !ok {
		return nil, workspaceservice.ErrNotFound
	}
	items := make([]workspaceservice.Invitation, 0)
	for _, item := range s.invitations {
		if item.WorkspaceID == workspaceID && item.Status == "pending" {
			item.Token = ""
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}
func (s *Store) RevokeInvitation(_ context.Context, workspaceID, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.invitations[id]
	if !ok || item.WorkspaceID != workspaceID {
		return workspaceservice.ErrNotFound
	}
	if item.Status != "pending" {
		return workspaceservice.ErrInvitationInvalid
	}
	item.Status = "revoked"
	item.RevokedAt = &now
	s.invitations[id] = item
	return nil
}
func (s *Store) AcceptInvitation(_ context.Context, workspaceID, tokenHash string, now time.Time, membership workspaceservice.Membership) (workspaceservice.Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var item workspaceservice.Invitation
	var id string
	for key, candidate := range s.invitations {
		if candidate.WorkspaceID == workspaceID && s.invitationHashes[key] == tokenHash {
			item = candidate
			id = key
			break
		}
	}
	if id == "" {
		return workspaceservice.Membership{}, workspaceservice.ErrInvitationInvalid
	}
	if item.Status == "revoked" {
		return workspaceservice.Membership{}, workspaceservice.ErrInvitationRevoked
	}
	if item.Status != "pending" {
		return workspaceservice.Membership{}, workspaceservice.ErrInvitationUsed
	}
	if !item.ExpiresAt.After(now) {
		return workspaceservice.Membership{}, workspaceservice.ErrInvitationExpired
	}
	user, ok := s.users[membership.UserID]
	if !ok || user.Status != "active" || !strings.EqualFold(item.Email, user.Email) || membership.WorkspaceID != workspaceID {
		return workspaceservice.Membership{}, workspaceservice.ErrInvitationInvalid
	}
	workspace, ok := s.workspaces[workspaceID]
	organization := s.organizations[workspace.OrganizationID]
	if !ok || workspace.Status != "active" || workspace.DeletedAt != nil || organization.Status != "active" {
		return workspaceservice.Membership{}, workspaceservice.ErrNotFound
	}
	// A removed membership is re-activated by a valid invitation.
	if existing, exists := s.memberships[membershipKey(workspaceID, membership.UserID)]; exists && existing.Status != "removed" {
		return workspaceservice.Membership{}, workspaceservice.ErrMemberExists
	}
	if !s.organizationHasMemberLocked(workspace.OrganizationID, membership.UserID) {
		limit, allowed := s.activeEntitlementLimitLocked(workspace.OrganizationID, billingservice.EntitlementTeamMembers)
		if !allowed || (limit != nil && s.organizationMemberCountLocked(workspace.OrganizationID)+1 > *limit) {
			return workspaceservice.Membership{}, billingservice.ErrQuotaExceeded
		}
	}
	membership.Role = item.Role
	membership = storedMembership(membership)
	s.memberships[membershipKey(workspaceID, membership.UserID)] = membership
	item.Status = "accepted"
	item.AcceptedAt = &now
	item.Token = ""
	s.invitations[id] = item
	return membership, nil
}

func (s *Store) CreateDevice(_ context.Context, device deviceservice.Device, credential deviceservice.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[device.UserID]; !ok {
		return authservice.ErrNotFound
	}
	if _, ok := s.workspaces[device.WorkspaceID]; !ok {
		return workspaceservice.ErrNotFound
	}
	s.devices[device.ID] = cloneDevice(device)
	s.deviceCredentials[device.ID] = credential
	return nil
}

func (s *Store) ListDevices(_ context.Context, userID string) ([]deviceservice.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]deviceservice.Device, 0)
	for _, device := range s.devices {
		if device.UserID == userID {
			items = append(items, cloneDevice(device))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func (s *Store) FindDevice(_ context.Context, id string) (deviceservice.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	device, ok := s.devices[id]
	if !ok {
		return deviceservice.Device{}, deviceservice.ErrNotFound
	}
	return cloneDevice(device), nil
}

func (s *Store) Heartbeat(_ context.Context, deviceID, version string, capabilities map[string]interface{}, now time.Time) (deviceservice.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[deviceID]
	if !ok {
		return deviceservice.Device{}, deviceservice.ErrNotFound
	}
	if device.RevokedAt != nil {
		return deviceservice.Device{}, deviceservice.ErrRevoked
	}
	device.Status = "online"
	device.LastSeenAt = &now
	device.UpdatedAt = now
	if version != "" {
		device.AgentVersion = version
	}
	if capabilities != nil {
		device.Capabilities = capabilities
	}
	s.devices[deviceID] = cloneDevice(device)
	return cloneDevice(device), nil
}

func (s *Store) RevokeDevice(_ context.Context, userID, deviceID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[deviceID]
	if !ok || device.UserID != userID {
		return deviceservice.ErrNotFound
	}
	// Keep the first revocation time, as PostgreSQL does with COALESCE.
	device.Status = "revoked"
	if device.RevokedAt == nil {
		device.RevokedAt = timePtr(now)
	}
	device.UpdatedAt = now
	s.devices[deviceID] = device
	if credential, exists := s.deviceCredentials[deviceID]; exists && credential.RevokedAt == nil {
		credential.RevokedAt = timePtr(now)
		s.deviceCredentials[deviceID] = credential
	}
	return nil
}

// RotateDeviceCredential replaces the device's credential under the store
// lock. The memory adapter keeps only the current credential per device, so
// replacing it revokes the previous secret immediately.
func (s *Store) RotateDeviceCredential(_ context.Context, userID, deviceID string, credential deviceservice.Credential, _ time.Time) (deviceservice.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[deviceID]
	if !ok || device.UserID != userID {
		return deviceservice.Device{}, deviceservice.ErrNotFound
	}
	if device.RevokedAt != nil {
		return deviceservice.Device{}, deviceservice.ErrRevoked
	}
	credential.DeviceID = deviceID
	credential.RevokedAt = nil
	s.deviceCredentials[deviceID] = credential
	return cloneDevice(device), nil
}

func (s *Store) AuthenticateDevice(_ context.Context, deviceID, credentialHash string) (deviceservice.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	device, ok := s.devices[deviceID]
	credential, credentialOK := s.deviceCredentials[deviceID]
	if !ok || !credentialOK || credential.SecretHash != credentialHash || credential.RevokedAt != nil ||
		device.RevokedAt != nil || (credential.ExpiresAt != nil && !credential.ExpiresAt.After(time.Now())) {
		return deviceservice.Device{}, deviceservice.ErrRevoked
	}
	return cloneDevice(device), nil
}

func (s *Store) CreateInstance(_ context.Context, instance browserinstanceservice.BrowserInstance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace, ok := s.workspaces[instance.WorkspaceID]
	if !ok {
		return workspaceservice.ErrNotFound
	}
	if err := s.validateInstanceReferencesLocked(instance); err != nil {
		return err
	}
	if s.instanceNameTakenLocked(instance) {
		return browserinstanceservice.ErrNameConflict
	}
	limit, allowed := s.activeEntitlementLimitLocked(workspace.OrganizationID, billingservice.EntitlementInstances)
	if !allowed || (limit != nil && s.organizationInstanceCountLocked(workspace.OrganizationID)+1 > *limit) {
		return billingservice.ErrQuotaExceeded
	}
	s.instances[instance.ID] = cloneInstance(instance)
	return nil
}

func (s *Store) FindInstance(_ context.Context, workspaceID, instanceID string) (browserinstanceservice.BrowserInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	instance, ok := s.instances[instanceID]
	if !ok || instance.WorkspaceID != workspaceID || instance.DeletedAt != nil {
		return browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrNotFound
	}
	return cloneInstance(instance), nil
}

func (s *Store) ListInstances(_ context.Context, workspaceID string) ([]browserinstanceservice.BrowserInstance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]browserinstanceservice.BrowserInstance, 0)
	for _, instance := range s.instances {
		if instance.WorkspaceID == workspaceID && instance.DeletedAt == nil {
			items = append(items, cloneInstance(instance))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func (s *Store) UpdateInstance(_ context.Context, instance browserinstanceservice.BrowserInstance, expectedVersion int64) (browserinstanceservice.BrowserInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.instances[instance.ID]
	if !ok || current.WorkspaceID != instance.WorkspaceID || current.DeletedAt != nil {
		return browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrNotFound
	}
	if expectedVersion <= 0 || current.Version != expectedVersion {
		return browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrVersionConflict
	}
	if err := s.validateInstanceReferencesLocked(instance); err != nil {
		return browserinstanceservice.BrowserInstance{}, err
	}
	if s.instanceNameTakenLocked(instance) {
		return browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrNameConflict
	}
	instance.Version = current.Version + 1
	instance.UpdatedAt = time.Now().UTC()
	s.instances[instance.ID] = cloneInstance(instance)
	return cloneInstance(instance), nil
}

// instanceNameTakenLocked mirrors browser_instances_live_name_uq: names are
// unique per workspace among live instances, ignoring letter case.
func (s *Store) instanceNameTakenLocked(instance browserinstanceservice.BrowserInstance) bool {
	for id, existing := range s.instances {
		if id != instance.ID && existing.WorkspaceID == instance.WorkspaceID && existing.DeletedAt == nil && strings.EqualFold(existing.Name, instance.Name) {
			return true
		}
	}
	return false
}

func (s *Store) validateInstanceReferencesLocked(instance browserinstanceservice.BrowserInstance) error {
	if instance.WorkspaceID == "" {
		return workspaceservice.ErrNotFound
	}
	if instance.AssignedDeviceID != "" {
		device, ok := s.devices[instance.AssignedDeviceID]
		if !ok || device.WorkspaceID != instance.WorkspaceID || device.RevokedAt != nil {
			return deviceservice.ErrNotFound
		}
	}
	if instance.ProfileID != "" {
		profile, ok := s.cloudProfiles[instance.ProfileID]
		if !ok || profile.WorkspaceID != instance.WorkspaceID || profile.DeletedAt != nil {
			return profilesyncservice.ErrNotFound
		}
	}
	if instance.FingerprintTemplateID != "" {
		template, ok := s.fingerprintTemplates[instance.FingerprintTemplateID]
		if !ok || template.WorkspaceID != instance.WorkspaceID || template.DeletedAt != nil {
			return fingerprintservice.ErrNotFound
		}
	}
	if instance.ProxyAssignmentID != "" {
		// Assignments are keyed by target; the instance references one by ID.
		found := false
		for _, assignment := range s.proxyAssignments {
			if assignment.ID == instance.ProxyAssignmentID && assignment.WorkspaceID == instance.WorkspaceID && assignment.DeletedAt == nil {
				found = true
				break
			}
		}
		if !found {
			return proxyservice.ErrNotFound
		}
	}
	return nil
}

func (s *Store) SoftDeleteInstance(_ context.Context, workspaceID, instanceID string, expectedVersion int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	instance, ok := s.instances[instanceID]
	if !ok || instance.WorkspaceID != workspaceID || instance.DeletedAt != nil {
		return browserinstanceservice.ErrNotFound
	}
	if expectedVersion <= 0 || instance.Version != expectedVersion {
		return browserinstanceservice.ErrVersionConflict
	}
	instance.DeletedAt = &now
	instance.UpdatedAt = now
	instance.Version++
	s.instances[instanceID] = instance
	return nil
}

func (s *Store) CreateCommand(_ context.Context, command browserinstanceservice.Command, desiredState string) (browserinstanceservice.Command, browserinstanceservice.BrowserInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := commandKey(command.WorkspaceID, command.IdempotencyKey)
	if commandID, exists := s.commandIdempotency[key]; exists {
		existing := s.commands[commandID]
		if !existing.MatchesRequest(command.InstanceID, command.Action, command.ExpectedVersion, command.Payload) {
			return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrIdempotencyConflict
		}
		return existing, cloneInstance(s.instances[existing.InstanceID]), nil
	}
	instance, ok := s.instances[command.InstanceID]
	if !ok || instance.WorkspaceID != command.WorkspaceID || instance.DeletedAt != nil {
		return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrNotFound
	}
	if command.ExpectedVersion <= 0 || instance.Version != command.ExpectedVersion {
		return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrVersionConflict
	}
	instance.DesiredState = desiredState
	instance.Version++
	instance.UpdatedAt = command.CreatedAt
	s.instances[instance.ID] = cloneInstance(instance)
	s.commands[command.ID] = command
	s.commandIdempotency[key] = command.ID
	return command, cloneInstance(instance), nil
}

func (s *Store) FindCommandByIdempotencyKey(_ context.Context, workspaceID, idempotencyKey string) (browserinstanceservice.Command, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.commandIdempotency[commandKey(workspaceID, idempotencyKey)]
	if !ok {
		return browserinstanceservice.Command{}, browserinstanceservice.ErrNotFound
	}
	return s.commands[id], nil
}

func (s *Store) ExpireCommands(_ context.Context, workspaceID, deviceID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, command := range s.commands {
		if command.WorkspaceID != workspaceID || command.DeviceID != deviceID ||
			browserinstanceservice.IsTerminalCommandStatus(command.Status) || command.Deadline.IsZero() || command.Deadline.After(now) {
			continue
		}
		command.Status = "expired"
		command.FailureCode = "command_expired"
		command.FailureMessage = "Command was not completed before its deadline"
		command.CompletedAt = &now
		s.commands[id] = command
	}
	return nil
}

func (s *Store) ListPendingCommands(_ context.Context, workspaceID, deviceID string) ([]browserinstanceservice.Command, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]browserinstanceservice.Command, 0)
	for _, command := range s.commands {
		if command.WorkspaceID != workspaceID || command.DeviceID != deviceID {
			continue
		}
		switch command.Status {
		case "pending", "queued", "accepted", "running":
			items = append(items, command)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	if len(items) > 100 {
		items = items[:100]
	}
	return items, nil
}

func (s *Store) TransitionCommand(_ context.Context, workspaceID, deviceID, commandID, status, failureCode, failureMessage string, now time.Time) (browserinstanceservice.Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	command, ok := s.commands[commandID]
	if !ok || command.WorkspaceID != workspaceID || command.DeviceID != deviceID {
		return browserinstanceservice.Command{}, browserinstanceservice.ErrNotFound
	}
	if !browserinstanceservice.CanTransitionCommand(command.Status, status) {
		return browserinstanceservice.Command{}, browserinstanceservice.ErrInvalidCommandTransition
	}
	if command.Status == status {
		return command, nil
	}
	command.Status = status
	command.FailureCode = failureCode
	command.FailureMessage = failureMessage
	switch status {
	case "accepted", "running":
		if command.AcknowledgedAt == nil {
			command.AcknowledgedAt = &now
		}
	case "completed", "failed":
		command.CompletedAt = &now
	}
	if status == "completed" && command.Action == "instance.migrate" {
		target, ok := command.Payload["targetDeviceId"].(string)
		device, deviceExists := s.devices[target]
		instance, instanceExists := s.instances[command.InstanceID]
		if !ok || !deviceExists || device.WorkspaceID != workspaceID || device.RevokedAt != nil ||
			!instanceExists || instance.WorkspaceID != workspaceID || instance.DeletedAt != nil ||
			instance.DesiredState != "migrating" || instance.AssignedDeviceID != command.DeviceID {
			return browserinstanceservice.Command{}, browserinstanceservice.ErrStateConflict
		}
		followUpKey := commandKey(workspaceID, browserinstanceservice.MigrationStartIdempotencyKey(command.ID))
		if _, exists := s.commandIdempotency[followUpKey]; exists {
			return browserinstanceservice.Command{}, browserinstanceservice.ErrIdempotencyConflict
		}
		instance.AssignedDeviceID = target
		instance.DesiredState = "running"
		instance.Version++
		instance.UpdatedAt = now
		s.instances[instance.ID] = cloneInstance(instance)
		followUp := browserinstanceservice.Command{
			ID: uuid.NewString(), WorkspaceID: workspaceID, InstanceID: instance.ID,
			DeviceID: target, Action: "instance.start",
			IdempotencyKey:  browserinstanceservice.MigrationStartIdempotencyKey(command.ID),
			ExpectedVersion: instance.Version, Status: "pending", Payload: map[string]interface{}{},
			Deadline: now.Add(2 * time.Minute), CreatedBy: command.CreatedBy, CreatedAt: now,
		}
		s.commands[followUp.ID] = followUp
		s.commandIdempotency[followUpKey] = followUp.ID
	}
	s.commands[commandID] = command
	return command, nil
}

func (s *Store) UpdateObservedState(_ context.Context, workspaceID, deviceID, instanceID, state string, _ map[string]interface{}, now time.Time) (browserinstanceservice.BrowserInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	instance, ok := s.instances[instanceID]
	if !ok || instance.WorkspaceID != workspaceID || instance.AssignedDeviceID != deviceID || instance.DeletedAt != nil {
		return browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrNotFound
	}
	instance.ObservedState = state
	instance.LastSeenAt = &now
	instance.UpdatedAt = now
	instance.Version++
	s.instances[instanceID] = cloneInstance(instance)
	return cloneInstance(instance), nil
}

func (s *Store) CreateFingerprintTemplate(ctx context.Context, template fingerprintservice.Template) error {
	return s.CreateFingerprintTemplates(ctx, []fingerprintservice.Template{template})
}

func (s *Store) CreateFingerprintTemplates(_ context.Context, templates []fingerprintservice.Template) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make(map[string]struct{}, len(templates))
	for _, template := range templates {
		if _, ok := s.workspaces[template.WorkspaceID]; !ok {
			return fingerprintservice.ErrNotFound
		}
		if _, exists := s.fingerprintTemplates[template.ID]; exists {
			return fingerprintservice.ErrNameConflict
		}
		nameKey := template.WorkspaceID + "\x00" + strings.ToLower(template.Name)
		if _, duplicate := names[nameKey]; duplicate {
			return fingerprintservice.ErrNameConflict
		}
		names[nameKey] = struct{}{}
		for _, existing := range s.fingerprintTemplates {
			if existing.WorkspaceID == template.WorkspaceID && existing.DeletedAt == nil && strings.EqualFold(existing.Name, template.Name) {
				return fingerprintservice.ErrNameConflict
			}
		}
	}
	for _, template := range templates {
		s.fingerprintTemplates[template.ID] = cloneFingerprintTemplate(template)
	}
	return nil
}

func (s *Store) FindFingerprintTemplate(_ context.Context, workspaceID, templateID string) (fingerprintservice.Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	template, ok := s.fingerprintTemplates[templateID]
	if !ok || template.WorkspaceID != workspaceID || template.DeletedAt != nil {
		return fingerprintservice.Template{}, fingerprintservice.ErrNotFound
	}
	return cloneFingerprintTemplate(template), nil
}

func (s *Store) ListFingerprintTemplates(_ context.Context, workspaceID string) ([]fingerprintservice.Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]fingerprintservice.Template, 0)
	for _, template := range s.fingerprintTemplates {
		if template.WorkspaceID == workspaceID && template.DeletedAt == nil {
			items = append(items, cloneFingerprintTemplate(template))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func (s *Store) UpdateFingerprintTemplate(_ context.Context, template fingerprintservice.Template, expectedVersion int64) (fingerprintservice.Template, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.fingerprintTemplates[template.ID]
	if !ok || current.WorkspaceID != template.WorkspaceID || current.DeletedAt != nil {
		return fingerprintservice.Template{}, fingerprintservice.ErrNotFound
	}
	if expectedVersion <= 0 || current.Version != expectedVersion {
		return fingerprintservice.Template{}, fingerprintservice.ErrVersionConflict
	}
	for id, existing := range s.fingerprintTemplates {
		if id != template.ID && existing.WorkspaceID == template.WorkspaceID && existing.DeletedAt == nil && strings.EqualFold(existing.Name, template.Name) {
			return fingerprintservice.Template{}, fingerprintservice.ErrNameConflict
		}
	}
	template.Version = current.Version + 1
	template.CreatedAt = current.CreatedAt
	template.CreatedBy = current.CreatedBy
	s.fingerprintTemplates[template.ID] = cloneFingerprintTemplate(template)
	return cloneFingerprintTemplate(template), nil
}

func (s *Store) DeleteFingerprintTemplate(_ context.Context, workspaceID, templateID string, expectedVersion int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	template, ok := s.fingerprintTemplates[templateID]
	if !ok || template.WorkspaceID != workspaceID || template.DeletedAt != nil {
		return fingerprintservice.ErrNotFound
	}
	if expectedVersion <= 0 || template.Version != expectedVersion {
		return fingerprintservice.ErrVersionConflict
	}
	for _, instance := range s.instances {
		if instance.WorkspaceID == workspaceID && instance.FingerprintTemplateID == templateID && instance.DeletedAt == nil {
			return fingerprintservice.ErrInUse
		}
	}
	template.DeletedAt = &now
	template.UpdatedAt = now
	template.Version++
	s.fingerprintTemplates[templateID] = template
	return nil
}

func (s *Store) CreateProxy(_ context.Context, proxy proxyservice.Proxy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workspaces[proxy.WorkspaceID]; !ok {
		return proxyservice.ErrNotFound
	}
	for _, existing := range s.proxies {
		if existing.WorkspaceID == proxy.WorkspaceID && existing.DeletedAt == nil && strings.EqualFold(existing.Name, proxy.Name) {
			return proxyservice.ErrNameConflict
		}
	}
	s.proxies[proxy.ID] = proxy
	return nil
}

func (s *Store) FindProxy(_ context.Context, workspaceID, proxyID string) (proxyservice.Proxy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	proxy, ok := s.proxies[proxyID]
	if !ok || proxy.WorkspaceID != workspaceID || proxy.DeletedAt != nil {
		return proxyservice.Proxy{}, proxyservice.ErrNotFound
	}
	return proxy, nil
}

func (s *Store) ListProxies(_ context.Context, workspaceID string) ([]proxyservice.Proxy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]proxyservice.Proxy, 0)
	for _, proxy := range s.proxies {
		if proxy.WorkspaceID == workspaceID && proxy.DeletedAt == nil {
			items = append(items, proxy)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func (s *Store) UpdateProxy(_ context.Context, proxy proxyservice.Proxy, expectedVersion int64) (proxyservice.Proxy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.proxies[proxy.ID]
	if !ok || current.WorkspaceID != proxy.WorkspaceID || current.DeletedAt != nil {
		return proxyservice.Proxy{}, proxyservice.ErrNotFound
	}
	if expectedVersion <= 0 || current.Version != expectedVersion {
		return proxyservice.Proxy{}, proxyservice.ErrVersionConflict
	}
	for id, existing := range s.proxies {
		if id != proxy.ID && existing.WorkspaceID == proxy.WorkspaceID && existing.DeletedAt == nil && strings.EqualFold(existing.Name, proxy.Name) {
			return proxyservice.Proxy{}, proxyservice.ErrNameConflict
		}
	}
	proxy.Version = current.Version + 1
	proxy.CreatedAt, proxy.CreatedBy = current.CreatedAt, current.CreatedBy
	s.proxies[proxy.ID] = proxy
	return proxy, nil
}

func (s *Store) DeleteProxy(_ context.Context, workspaceID, proxyID string, expectedVersion int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	proxy, ok := s.proxies[proxyID]
	if !ok || proxy.WorkspaceID != workspaceID || proxy.DeletedAt != nil {
		return proxyservice.ErrNotFound
	}
	if expectedVersion <= 0 || proxy.Version != expectedVersion {
		return proxyservice.ErrVersionConflict
	}
	for _, assignment := range s.proxyAssignments {
		if assignment.WorkspaceID == workspaceID && assignment.ProxyID == proxyID && assignment.DeletedAt == nil {
			return proxyservice.ErrInUse
		}
	}
	proxy.DeletedAt, proxy.UpdatedAt = &now, now
	proxy.Version++
	s.proxies[proxyID] = proxy
	return nil
}

func (s *Store) CreateProxyAssignment(_ context.Context, assignment proxyservice.Assignment) (proxyservice.Assignment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	proxy, ok := s.proxies[assignment.ProxyID]
	if !ok || proxy.WorkspaceID != assignment.WorkspaceID || proxy.DeletedAt != nil {
		return proxyservice.Assignment{}, proxyservice.ErrNotFound
	}
	key := assignmentKey(assignment.WorkspaceID, assignment.TargetType, assignment.TargetID)
	switch assignment.TargetType {
	case "account":
		state := s.accountCenter()
		state.mu.RLock()
		account, exists := state.accounts[assignment.TargetID]
		valid := exists && account.WorkspaceID == assignment.WorkspaceID && account.DeletedAt == nil
		state.mu.RUnlock()
		if !valid {
			return proxyservice.Assignment{}, proxyservice.ErrNotFound
		}
	case "profile":
		profile, exists := s.cloudProfiles[assignment.TargetID]
		if !exists || profile.WorkspaceID != assignment.WorkspaceID || profile.DeletedAt != nil {
			return proxyservice.Assignment{}, proxyservice.ErrNotFound
		}
	case "browser_instance":
		instance, exists := s.instances[assignment.TargetID]
		if !exists || instance.WorkspaceID != assignment.WorkspaceID {
			return proxyservice.Assignment{}, proxyservice.ErrNotFound
		}
	default:
		return proxyservice.Assignment{}, proxyservice.ErrNotFound
	}
	if current, ok := s.proxyAssignments[key]; ok && current.DeletedAt == nil {
		if current.ProxyID == assignment.ProxyID {
			return current, nil
		}
		return proxyservice.Assignment{}, proxyservice.ErrAssignmentConflict
	}
	s.proxyAssignments[key] = assignment
	return assignment, nil
}

func (s *Store) FindProxyAssignment(_ context.Context, workspaceID, targetID, targetType string) (proxyservice.Assignment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	assignment, ok := s.proxyAssignments[assignmentKey(workspaceID, targetType, targetID)]
	if !ok || assignment.DeletedAt != nil {
		return proxyservice.Assignment{}, proxyservice.ErrNotFound
	}
	return s.namedAssignmentLocked(assignment), nil
}

func (s *Store) ListProxyAssignments(_ context.Context, workspaceID string, filter proxyservice.AssignmentFilter) ([]proxyservice.Assignment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]proxyservice.Assignment, 0)
	for _, assignment := range s.proxyAssignments {
		if assignment.WorkspaceID != workspaceID || assignment.DeletedAt != nil ||
			(filter.ProxyID != "" && assignment.ProxyID != filter.ProxyID) ||
			(filter.TargetType != "" && assignment.TargetType != filter.TargetType) {
			continue
		}
		items = append(items, s.namedAssignmentLocked(assignment))
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (s *Store) namedAssignmentLocked(assignment proxyservice.Assignment) proxyservice.Assignment {
	if proxy, ok := s.proxies[assignment.ProxyID]; ok && proxy.WorkspaceID == assignment.WorkspaceID {
		assignment.ProxyName = proxy.Name
	}
	return assignment
}

func (s *Store) DeleteProxyAssignment(_ context.Context, workspaceID, targetID, targetType string, expectedVersion int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := assignmentKey(workspaceID, targetType, targetID)
	assignment, ok := s.proxyAssignments[key]
	if !ok || assignment.DeletedAt != nil {
		return proxyservice.ErrNotFound
	}
	if expectedVersion <= 0 || assignment.Version != expectedVersion {
		return proxyservice.ErrVersionConflict
	}
	assignment.DeletedAt, assignment.UpdatedAt = &now, now
	assignment.Version++
	s.proxyAssignments[key] = assignment
	return nil
}

func (s *Store) CreateProxyHealthCheck(_ context.Context, check proxyservice.HealthCheck) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	proxy, ok := s.proxies[check.ProxyID]
	if !ok || proxy.WorkspaceID != check.WorkspaceID || proxy.DeletedAt != nil {
		return proxyservice.ErrNotFound
	}
	s.proxyHealthChecks[check.ID] = check
	task := taskservice.Task{
		ID: check.RequestID, WorkspaceID: check.WorkspaceID, TaskType: "proxy.health_check",
		RequestedBy: check.CreatedBy, IdempotencyKey: "proxy-health:" + check.ID, Status: "queued",
		Payload:    map[string]interface{}{"checkId": check.ID, "proxyId": check.ProxyID, "connectorType": string(check.ConnectorType), "kernel": string(check.Kernel)},
		RetryLimit: 3, AvailableAt: check.CreatedAt, CreatedAt: check.CreatedAt, UpdatedAt: check.CreatedAt,
	}
	s.tasks[task.ID] = task
	s.taskIdempotency[commandKey(task.WorkspaceID, task.IdempotencyKey)] = task.ID
	return nil
}

func (s *Store) FindProxyHealthCheck(_ context.Context, workspaceID, checkID string) (proxyservice.HealthCheck, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	check, ok := s.proxyHealthChecks[checkID]
	if !ok || check.WorkspaceID != workspaceID {
		return proxyservice.HealthCheck{}, proxyservice.ErrNotFound
	}
	return check, nil
}

func (s *Store) ListProxyHealthChecks(_ context.Context, workspaceID, proxyID string, limit int) ([]proxyservice.HealthCheck, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]proxyservice.HealthCheck, 0)
	for _, check := range s.proxyHealthChecks {
		if check.WorkspaceID == workspaceID && (proxyID == "" || check.ProxyID == proxyID) {
			items = append(items, check)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Store) CompleteProxyHealthCheck(_ context.Context, workspaceID, checkID string, result proxyservice.HealthResult, now time.Time) (proxyservice.HealthCheck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	check, ok := s.proxyHealthChecks[checkID]
	if !ok || check.WorkspaceID != workspaceID {
		return proxyservice.HealthCheck{}, proxyservice.ErrNotFound
	}
	if check.CompletedAt != nil {
		return check, nil
	}
	check.Status, check.IP, check.LatencyMS = result.Status, result.IP, result.LatencyMS
	check.ErrorCode, check.ErrorMessage, check.CompletedAt = result.ErrorCode, result.ErrorMessage, &now
	s.proxyHealthChecks[checkID] = check
	return check, nil
}

func assignmentKey(workspaceID, targetType, targetID string) string {
	return workspaceID + ":" + targetType + ":" + targetID
}

func (s *Store) CreateCloudProfile(_ context.Context, profile profilesyncservice.Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workspaces[profile.WorkspaceID]; !ok {
		return profilesyncservice.ErrNotFound
	}
	if profile.FingerprintTemplateID != "" {
		template, ok := s.fingerprintTemplates[profile.FingerprintTemplateID]
		if !ok || template.WorkspaceID != profile.WorkspaceID || template.DeletedAt != nil {
			return fingerprintservice.ErrNotFound
		}
	}
	for _, existing := range s.cloudProfiles {
		if existing.WorkspaceID == profile.WorkspaceID && existing.DeletedAt == nil && strings.EqualFold(existing.Name, profile.Name) {
			return profilesyncservice.ErrNameConflict
		}
	}
	s.cloudProfiles[profile.ID] = cloneCloudProfile(profile)
	return nil
}

func (s *Store) FindCloudProfile(_ context.Context, workspaceID, profileID string) (profilesyncservice.Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.cloudProfiles[profileID]
	if !ok || profile.WorkspaceID != workspaceID || profile.DeletedAt != nil {
		return profilesyncservice.Profile{}, profilesyncservice.ErrNotFound
	}
	return cloneCloudProfile(profile), nil
}

func (s *Store) ListCloudProfiles(_ context.Context, workspaceID string) ([]profilesyncservice.Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]profilesyncservice.Profile, 0)
	for _, profile := range s.cloudProfiles {
		if profile.WorkspaceID == workspaceID && profile.DeletedAt == nil {
			items = append(items, cloneCloudProfile(profile))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func (s *Store) AcquireProfileLease(_ context.Context, lease profilesyncservice.Lease, hash string, reclaimOwn bool, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.cloudProfiles[lease.ProfileID]
	if !ok || profile.WorkspaceID != lease.WorkspaceID || profile.DeletedAt != nil {
		return profilesyncservice.ErrNotFound
	}
	device, ok := s.devices[lease.HolderDeviceID]
	if !ok || device.WorkspaceID != lease.WorkspaceID || device.RevokedAt != nil {
		return deviceservice.ErrNotFound
	}
	if s.openProfileConflictLocked(lease.ProfileID) {
		return profilesyncservice.ErrConflictUnresolved
	}
	if active, ok := s.profileLeases[lease.ProfileID]; ok {
		if active.lease.ReleasedAt == nil && active.lease.ExpiresAt.After(now) &&
			(!reclaimOwn || active.lease.HolderDeviceID != lease.HolderDeviceID) {
			return profilesyncservice.ErrLeaseHeld
		}
	}
	s.profileLeases[lease.ProfileID] = memoryProfileLease{lease: lease, hash: hash}
	profile.Status = "syncing"
	profile.UpdatedAt = now
	profile.Version++
	s.cloudProfiles[profile.ID] = profile
	return nil
}

func (s *Store) ValidateProfileLease(_ context.Context, workspaceID, profileID, deviceID, hash string, now time.Time) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.cloudProfiles[profileID]
	if !ok || profile.WorkspaceID != workspaceID || profile.DeletedAt != nil {
		return profilesyncservice.ErrNotFound
	}
	if !s.validProfileLeaseLocked(profileID, deviceID, hash, now) {
		return profilesyncservice.ErrLeaseInvalid
	}
	return nil
}

func (s *Store) RenewProfileLease(_ context.Context, workspaceID, profileID, deviceID, hash string, now, expiresAt time.Time) (profilesyncservice.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.profileLeases[profileID]
	if !ok || record.lease.WorkspaceID != workspaceID || record.lease.HolderDeviceID != deviceID || record.hash != hash || record.lease.ReleasedAt != nil || !record.lease.ExpiresAt.After(now) {
		return profilesyncservice.Lease{}, profilesyncservice.ErrLeaseInvalid
	}
	record.lease.RenewedAt = &now
	record.lease.ExpiresAt = expiresAt
	s.profileLeases[profileID] = record
	return record.lease, nil
}

func (s *Store) ReleaseProfileLease(_ context.Context, workspaceID, profileID, deviceID, hash string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.profileLeases[profileID]
	if !ok || record.lease.WorkspaceID != workspaceID || record.lease.HolderDeviceID != deviceID || record.hash != hash || record.lease.ReleasedAt != nil || !record.lease.ExpiresAt.After(now) {
		return profilesyncservice.ErrLeaseInvalid
	}
	record.lease.ReleasedAt = &now
	s.profileLeases[profileID] = record
	profile := s.cloudProfiles[profileID]
	if profile.Status != "conflict" {
		profile.Status = "active"
	}
	profile.UpdatedAt = now
	profile.Version++
	s.cloudProfiles[profileID] = profile
	return nil
}

func (s *Store) BeginProfileRevision(
	_ context.Context,
	revision profilesyncservice.Revision,
	manifest profilesyncservice.Manifest,
	objects []profilesyncservice.Object,
	leaseHash, conflictID string,
	now time.Time,
) (profilesyncservice.Revision, profilesyncservice.Profile, *profilesyncservice.Conflict, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.cloudProfiles[revision.ProfileID]
	if !ok || profile.WorkspaceID != revision.WorkspaceID || profile.DeletedAt != nil {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, profilesyncservice.ErrNotFound
	}
	if !s.validProfileLeaseLocked(revision.ProfileID, revision.DeviceID, leaseHash, now) {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, profilesyncservice.ErrLeaseInvalid
	}
	if s.openProfileConflictLocked(revision.ProfileID) {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, profilesyncservice.ErrConflictUnresolved
	}
	if profile.CurrentRevisionID == "" && revision.BaseRevisionID != "" {
		return profilesyncservice.Revision{}, profilesyncservice.Profile{}, nil, profilesyncservice.ErrRevisionConflict
	}
	revision.Revision = int64(len(s.profileRevisionIDs[revision.ProfileID]) + 1)
	s.profileRevisions[revision.ID] = revision
	s.profileRevisionIDs[revision.ProfileID] = append(s.profileRevisionIDs[revision.ProfileID], revision.ID)
	s.profileManifests[revision.ID] = cloneProfileManifest(manifest)
	s.profileObjects[revision.ID] = cloneProfileObjects(objects)
	if profile.CurrentRevisionID != revision.BaseRevisionID {
		conflict := profilesyncservice.Conflict{
			ID: conflictID, WorkspaceID: revision.WorkspaceID, ProfileID: revision.ProfileID,
			LocalRevisionID: revision.ID, RemoteRevisionID: profile.CurrentRevisionID,
			Status: "open", CreatedAt: now,
		}
		s.profileConflicts[conflict.ID] = conflict
		s.profileConflictIDs[revision.ProfileID] = append(s.profileConflictIDs[revision.ProfileID], conflict.ID)
		profile.Status = "conflict"
		profile.UpdatedAt = now
		profile.Version++
		s.cloudProfiles[profile.ID] = profile
		return revision, profile, &conflict, nil
	}
	profile.Status = "syncing"
	profile.UpdatedAt = now
	profile.Version++
	s.cloudProfiles[profile.ID] = profile
	return revision, profile, nil, nil
}

func (s *Store) LoadProfileRevisionPlan(_ context.Context, workspaceID, profileID, revisionID, deviceID, leaseHash string, now time.Time) (profilesyncservice.RevisionPlan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.cloudProfiles[profileID]
	if !ok || profile.WorkspaceID != workspaceID || profile.DeletedAt != nil {
		return profilesyncservice.RevisionPlan{}, profilesyncservice.ErrNotFound
	}
	if !s.validProfileLeaseLocked(profileID, deviceID, leaseHash, now) {
		return profilesyncservice.RevisionPlan{}, profilesyncservice.ErrLeaseInvalid
	}
	revision, ok := s.profileRevisions[revisionID]
	if !ok || revision.ProfileID != profileID || revision.Status != "uploading" {
		return profilesyncservice.RevisionPlan{}, profilesyncservice.ErrRevisionState
	}
	return profilesyncservice.RevisionPlan{
		Revision: revision, Manifest: cloneProfileManifest(s.profileManifests[revisionID]),
		Objects: cloneProfileObjects(s.profileObjects[revisionID]),
	}, nil
}

func (s *Store) LoadProfileRevisionSnapshot(_ context.Context, workspaceID, profileID, revisionID string) (profilesyncservice.RevisionPlan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.cloudProfiles[profileID]
	if !ok || profile.WorkspaceID != workspaceID || profile.DeletedAt != nil {
		return profilesyncservice.RevisionPlan{}, profilesyncservice.ErrNotFound
	}
	revision, ok := s.profileRevisions[revisionID]
	if !ok || revision.ProfileID != profileID || (revision.Status != "committed" && revision.Status != "superseded") {
		return profilesyncservice.RevisionPlan{}, profilesyncservice.ErrRevisionState
	}
	return profilesyncservice.RevisionPlan{
		Revision: revision, Manifest: cloneProfileManifest(s.profileManifests[revisionID]),
		Objects: cloneProfileObjects(s.profileObjects[revisionID]),
	}, nil
}

func (s *Store) CommitProfileRevision(_ context.Context, workspaceID, profileID, revisionID, deviceID, leaseHash string, now time.Time) (profilesyncservice.Profile, profilesyncservice.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.cloudProfiles[profileID]
	if !ok || profile.WorkspaceID != workspaceID || profile.DeletedAt != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrNotFound
	}
	if !s.validProfileLeaseLocked(profileID, deviceID, leaseHash, now) {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrLeaseInvalid
	}
	revision, ok := s.profileRevisions[revisionID]
	if !ok || revision.ProfileID != profileID || revision.Status != "uploading" {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrRevisionState
	}
	for _, conflict := range s.profileConflicts {
		if conflict.LocalRevisionID == revisionID && conflict.Status == "open" {
			return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrRevisionConflict
		}
	}
	if revision.BaseRevisionID != profile.CurrentRevisionID {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrRevisionState
	}
	if err := s.applyProfileStorageQuotaLocked(workspaceID, profileID, revisionID, revision.BaseRevisionID, now); err != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, err
	}
	if previous, ok := s.profileRevisions[profile.CurrentRevisionID]; ok {
		previous.Status = "superseded"
		s.profileRevisions[previous.ID] = previous
	}
	revision.Status = "committed"
	revision.CommittedAt = &now
	s.profileRevisions[revision.ID] = revision
	profile.CurrentRevisionID = revision.ID
	profile.Status = "active"
	profile.UpdatedAt = now
	profile.Version++
	s.cloudProfiles[profile.ID] = profile
	record := s.profileLeases[profileID]
	record.lease.ReleasedAt = &now
	s.profileLeases[profileID] = record
	return cloneCloudProfile(profile), revision, nil
}

// applyProfileStorageQuotaLocked atomically applies the resulting current
// file-set delta to the organization ledger. The caller holds s.mu, so commits
// from different workspaces in one organization cannot race the check.
func (s *Store) applyProfileStorageQuotaLocked(workspaceID, profileID, revisionID, baseRevisionID string, now time.Time) error {
	workspace, ok := s.workspaces[workspaceID]
	if !ok || strings.TrimSpace(workspace.OrganizationID) == "" {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	organizationID := workspace.OrganizationID
	entitlement, ok := s.entitlements[organizationID+"|"+billingservice.EntitlementStorageBytes]
	if !ok || !entitlement.FeatureEnabled || entitlement.ValidFrom.After(now) || (entitlement.ValidUntil != nil && !entitlement.ValidUntil.After(now)) {
		return profilesyncservice.ErrStorageQuotaExceeded
	}

	current := s.profileStorageFiles[profileID]
	if current == nil {
		current = make(map[string]memoryProfileStorageFile)
		// A non-empty current revision with no materialized state indicates a
		// corrupt/incompletely migrated ledger. Do not fail open.
		profile := s.cloudProfiles[profileID]
		if profile.CurrentRevisionID != "" {
			return profilesyncservice.ErrStorageQuotaExceeded
		}
	}
	next := cloneProfileStorageFiles(current)
	manifest := s.profileManifests[revisionID]
	if manifest.Mode == "snapshot" {
		next = make(map[string]memoryProfileStorageFile, len(manifest.Files))
	} else if manifest.Mode != "incremental" || strings.TrimSpace(baseRevisionID) == "" {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	for _, deleted := range manifest.DeletedPaths {
		delete(next, deleted)
	}
	for _, file := range manifest.Files {
		if file.SizeBytes < 0 {
			return profilesyncservice.ErrStorageQuotaExceeded
		}
		next[file.Path] = memoryProfileStorageFile{
			WorkspaceID: workspaceID, ObjectKey: file.ObjectKey, SizeBytes: file.SizeBytes, RevisionID: revisionID,
		}
	}
	var currentBytes, nextBytes int64
	for _, file := range current {
		if file.SizeBytes < 0 || currentBytes > (1<<63-1)-file.SizeBytes {
			return profilesyncservice.ErrStorageQuotaExceeded
		}
		currentBytes += file.SizeBytes
	}
	for _, file := range next {
		if file.SizeBytes < 0 || nextBytes > (1<<63-1)-file.SizeBytes {
			return profilesyncservice.ErrStorageQuotaExceeded
		}
		nextBytes += file.SizeBytes
	}
	used := s.profileStorageUsage[organizationID]
	if used < 0 || nextBytes-currentBytes < -used || used > (1<<63-1)-(nextBytes-currentBytes) {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	result := used + (nextBytes - currentBytes)
	if entitlement.Limit != nil && result > *entitlement.Limit {
		return profilesyncservice.ErrStorageQuotaExceeded
	}
	s.profileStorageUsage[organizationID] = result
	s.profileStorageFiles[profileID] = next
	entitlement.Consumed = result
	s.entitlements[organizationID+"|"+billingservice.EntitlementStorageBytes] = entitlement
	return nil
}

func cloneProfileStorageFiles(value map[string]memoryProfileStorageFile) map[string]memoryProfileStorageFile {
	if value == nil {
		return nil
	}
	clone := make(map[string]memoryProfileStorageFile, len(value))
	for key, file := range value {
		clone[key] = file
	}
	return clone
}

func (s *Store) RestoreProfileRevision(_ context.Context, workspaceID, profileID, revisionID, deviceID, leaseHash, _ string, now time.Time) (profilesyncservice.Profile, profilesyncservice.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.cloudProfiles[profileID]
	if !ok || profile.WorkspaceID != workspaceID || profile.DeletedAt != nil {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrNotFound
	}
	if !s.validProfileLeaseLocked(profileID, deviceID, leaseHash, now) {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrLeaseInvalid
	}
	if profile.CurrentRevisionID == revisionID {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrRevisionState
	}
	for _, conflict := range s.profileConflicts {
		if conflict.ProfileID == profileID && conflict.Status == "open" {
			return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrRevisionConflict
		}
	}
	target, ok := s.profileRevisions[revisionID]
	if !ok || target.ProfileID != profileID || (target.Status != "committed" && target.Status != "superseded") {
		return profilesyncservice.Profile{}, profilesyncservice.Revision{}, profilesyncservice.ErrRevisionState
	}
	if current, exists := s.profileRevisions[profile.CurrentRevisionID]; exists {
		current.Status = "superseded"
		s.profileRevisions[current.ID] = current
	}
	target.Status = "committed"
	s.profileRevisions[target.ID] = target
	profile.CurrentRevisionID = target.ID
	profile.Status = "active"
	profile.UpdatedAt = now
	profile.Version++
	s.cloudProfiles[profile.ID] = profile
	record := s.profileLeases[profileID]
	record.lease.ReleasedAt = &now
	s.profileLeases[profileID] = record
	return cloneCloudProfile(profile), target, nil
}

func (s *Store) ListProfileRevisions(_ context.Context, workspaceID, profileID string) ([]profilesyncservice.Revision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.cloudProfiles[profileID]
	if !ok || profile.WorkspaceID != workspaceID || profile.DeletedAt != nil {
		return nil, profilesyncservice.ErrNotFound
	}
	ids := s.profileRevisionIDs[profileID]
	items := make([]profilesyncservice.Revision, 0, len(ids))
	for index := len(ids) - 1; index >= 0; index-- {
		items = append(items, s.profileRevisions[ids[index]])
	}
	return items, nil
}

func (s *Store) ListProfileConflicts(_ context.Context, workspaceID, profileID string) ([]profilesyncservice.Conflict, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.cloudProfiles[profileID]
	if !ok || profile.WorkspaceID != workspaceID || profile.DeletedAt != nil {
		return nil, profilesyncservice.ErrNotFound
	}
	ids := s.profileConflictIDs[profileID]
	items := make([]profilesyncservice.Conflict, 0, len(ids))
	for index := len(ids) - 1; index >= 0; index-- {
		items = append(items, s.profileConflicts[ids[index]])
	}
	return items, nil
}

func (s *Store) LoadProfileConflictPlan(_ context.Context, workspaceID, profileID, conflictID string) (profilesyncservice.Conflict, profilesyncservice.RevisionPlan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.cloudProfiles[profileID]
	conflict, conflictOK := s.profileConflicts[conflictID]
	if !ok || profile.WorkspaceID != workspaceID || profile.DeletedAt != nil || !conflictOK || conflict.ProfileID != profileID {
		return profilesyncservice.Conflict{}, profilesyncservice.RevisionPlan{}, profilesyncservice.ErrNotFound
	}
	revision, ok := s.profileRevisions[conflict.LocalRevisionID]
	if !ok || revision.ProfileID != profileID {
		return profilesyncservice.Conflict{}, profilesyncservice.RevisionPlan{}, profilesyncservice.ErrNotFound
	}
	return conflict, profilesyncservice.RevisionPlan{
		Revision: revision, Manifest: cloneProfileManifest(s.profileManifests[revision.ID]),
		Objects: cloneProfileObjects(s.profileObjects[revision.ID]),
	}, nil
}

// ResolveProfileConflict mirrors the PostgreSQL implementation: keep_local
// promotes the uploaded local snapshot (the service verified its objects)
// while no other device holds a lease; keep_remote discards the local
// revision. Either way the profile stays in conflict while other conflicts
// remain open.
func (s *Store) ResolveProfileConflict(_ context.Context, workspaceID, profileID, conflictID, resolution, actorID string, now time.Time) (profilesyncservice.Conflict, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.cloudProfiles[profileID]
	conflict, conflictOK := s.profileConflicts[conflictID]
	if !ok || profile.WorkspaceID != workspaceID || profile.DeletedAt != nil || !conflictOK || conflict.ProfileID != profileID {
		return profilesyncservice.Conflict{}, profilesyncservice.ErrNotFound
	}
	if conflict.Status != "open" {
		return profilesyncservice.Conflict{}, profilesyncservice.ErrRevisionState
	}
	local, localOK := s.profileRevisions[conflict.LocalRevisionID]
	switch resolution {
	case "keep_local":
		if !localOK || local.Status != "uploading" || s.profileManifests[local.ID].Mode != "snapshot" ||
			profile.CurrentRevisionID != conflict.RemoteRevisionID {
			return profilesyncservice.Conflict{}, profilesyncservice.ErrRevisionState
		}
		if record, leased := s.profileLeases[profileID]; leased && record.lease.ReleasedAt == nil && record.lease.ExpiresAt.After(now) {
			return profilesyncservice.Conflict{}, profilesyncservice.ErrLeaseHeld
		}
		if err := s.applyProfileStorageQuotaLocked(workspaceID, profileID, local.ID, local.BaseRevisionID, now); err != nil {
			return profilesyncservice.Conflict{}, err
		}
		if remote, exists := s.profileRevisions[conflict.RemoteRevisionID]; exists && remote.Status == "committed" {
			remote.Status = "superseded"
			s.profileRevisions[remote.ID] = remote
		}
		local.Status = "committed"
		local.CommittedAt = &now
		s.profileRevisions[local.ID] = local
		profile.CurrentRevisionID = local.ID
	case "keep_remote":
		if localOK && local.Status == "uploading" {
			local.Status = "superseded"
			s.profileRevisions[local.ID] = local
		}
	default:
		return profilesyncservice.Conflict{}, profilesyncservice.ErrRevisionState
	}
	conflict.Status = "resolved"
	conflict.Resolution = resolution
	conflict.ResolvedBy = actorID
	conflict.ResolvedAt = &now
	s.profileConflicts[conflict.ID] = conflict
	profile.Status = "active"
	if s.openProfileConflictLocked(profileID) {
		profile.Status = "conflict"
	}
	profile.UpdatedAt = now
	profile.Version++
	s.cloudProfiles[profile.ID] = profile
	return conflict, nil
}

func (s *Store) openProfileConflictLocked(profileID string) bool {
	for _, id := range s.profileConflictIDs[profileID] {
		if s.profileConflicts[id].Status == "open" {
			return true
		}
	}
	return false
}

func (s *Store) validProfileLeaseLocked(profileID, deviceID, hash string, now time.Time) bool {
	record, ok := s.profileLeases[profileID]
	return ok && record.lease.HolderDeviceID == deviceID && record.hash == hash && record.lease.ReleasedAt == nil && record.lease.ExpiresAt.After(now)
}

func (s *Store) CreateTask(_ context.Context, task taskservice.Task) (taskservice.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := commandKey(task.WorkspaceID, task.IdempotencyKey)
	if taskID, ok := s.taskIdempotency[key]; ok {
		if !taskservice.SameWorkflowRequest(s.tasks[taskID], task) {
			return taskservice.Task{}, taskservice.ErrStateConflict
		}
		return cloneTask(s.tasks[taskID]), nil
	}
	if _, ok := s.workspaces[task.WorkspaceID]; !ok {
		return taskservice.Task{}, taskservice.ErrNotFound
	}
	if task.TaskType == "workflow.execute" {
		state := s.automation()
		state.mu.RLock()
		defer state.mu.RUnlock()
		workflow, ok := state.workflows[task.WorkflowID]
		if !ok || workflow.WorkspaceID != task.WorkspaceID {
			return taskservice.Task{}, taskservice.ErrNotFound
		}
		if workflow.Status != "published" || workflow.PublishedVersionID != task.WorkflowVersionID {
			return taskservice.Task{}, taskservice.ErrStateConflict
		}
		found := false
		for _, version := range state.versions[task.WorkflowID] {
			if version.ID == task.WorkflowVersionID && version.WorkspaceID == task.WorkspaceID {
				found = true
			}
		}
		instanceID, _ := task.Payload["instanceId"].(string)
		instance, exists := s.instances[instanceID]
		if !found || !exists || instance.WorkspaceID != task.WorkspaceID || instance.DeletedAt != nil {
			return taskservice.Task{}, taskservice.ErrNotFound
		}
	}
	s.tasks[task.ID] = cloneTask(task)
	s.taskIdempotency[key] = task.ID
	return cloneTask(task), nil
}

func (s *Store) FindTask(_ context.Context, workspaceID, taskID string) (taskservice.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[taskID]
	if !ok || task.WorkspaceID != workspaceID {
		return taskservice.Task{}, taskservice.ErrNotFound
	}
	return cloneTask(task), nil
}

func (s *Store) ListTasks(_ context.Context, workspaceID string, limit int) ([]taskservice.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]taskservice.Task, 0)
	for _, task := range s.tasks {
		if task.WorkspaceID == workspaceID {
			items = append(items, cloneTask(task))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Store) CancelTask(_ context.Context, workspaceID, taskID string, now time.Time) (taskservice.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[taskID]
	if !ok || task.WorkspaceID != workspaceID {
		return taskservice.Task{}, taskservice.ErrNotFound
	}
	switch task.Status {
	case "cancelled":
		return cloneTask(task), nil
	case "succeeded", "failed", "dead_letter":
		return taskservice.Task{}, taskservice.ErrStateConflict
	}
	if lease, ok := s.taskLeases[taskID]; ok {
		run := s.taskRuns[lease.RunID]
		run.status = "cancelled"
		run.finishedAt = timePtr(now)
		run.errorCode = "task_cancelled"
		s.taskRuns[lease.RunID] = run
		attempt := s.taskAttemptStates[lease.AttemptID]
		attempt.status = "expired"
		attempt.finishedAt = timePtr(now)
		attempt.errorCode = "task_cancelled"
		s.taskAttemptStates[lease.AttemptID] = attempt
	}
	task.Status = "cancelled"
	task.CompletedAt = &now
	task.UpdatedAt = now
	task.LeaseOwner = ""
	task.LeaseExpiresAt = nil
	s.tasks[taskID] = task
	s.syncProxyHealthTerminalLocked(task, now)
	delete(s.taskLeases, taskID)
	return cloneTask(task), nil
}

func (s *Store) ClaimNextTask(_ context.Context, workerID string, supportedTypes []string, leaseTTL time.Duration, now time.Time) (taskservice.Lease, error) {
	return s.claimTasks(workerID, supportedTypes, leaseTTL, now, "", "")
}

func (s *Store) ClaimDeviceWorkflow(_ context.Context, workspaceID, deviceID string, leaseTTL time.Duration, now time.Time) (taskservice.Lease, error) {
	return s.claimTasks("device:"+deviceID, []string{"workflow.execute"}, leaseTTL, now, workspaceID, deviceID)
}

func (s *Store) claimTasks(workerID string, supportedTypes []string, leaseTTL time.Duration, now time.Time, workspaceID, deviceID string) (taskservice.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	supported := make(map[string]struct{}, len(supportedTypes))
	for _, taskType := range supportedTypes {
		supported[taskType] = struct{}{}
	}
	candidates := make([]taskservice.Task, 0)
	for _, task := range s.tasks {
		if task.TaskType == "workflow.execute" {
			if deviceID == "" || task.WorkspaceID != workspaceID {
				continue
			}
			device, ok := s.devices[deviceID]
			if !ok || device.WorkspaceID != workspaceID || device.RevokedAt != nil {
				continue
			}
			instanceID, _ := task.Payload["instanceId"].(string)
			instance, ok := s.instances[instanceID]
			if !ok || instance.WorkspaceID != workspaceID || instance.AssignedDeviceID != deviceID || instance.DeletedAt != nil {
				continue
			}
			version, err := s.FindWorkflowVersionByID(context.Background(), workspaceID, task.WorkflowID, task.WorkflowVersionID)
			if err != nil || (version.Definition.Engine != "playwright" && version.Definition.Engine != "cdp") {
				continue
			}
		}
		if _, ok := supported[task.TaskType]; !ok {
			continue
		}
		ready := task.Status == "queued" && !task.AvailableAt.After(now)
		expired := (task.Status == "leased" || task.Status == "running") && task.LeaseExpiresAt != nil && !task.LeaseExpiresAt.After(now)
		if ready || expired {
			candidates = append(candidates, task)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority > candidates[j].Priority
		}
		if !candidates[i].AvailableAt.Equal(candidates[j].AvailableAt) {
			return candidates[i].AvailableAt.Before(candidates[j].AvailableAt)
		}
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})
	for _, task := range candidates {
		attempt := s.taskAttempts[task.ID] + 1
		if attempt > task.RetryLimit+1 {
			task.Status = "dead_letter"
			task.ErrorCode = "retry_limit_exhausted"
			task.ErrorMessage = "Task lease expired after the retry limit was exhausted"
			task.CompletedAt = &now
			task.UpdatedAt = now
			s.tasks[task.ID] = task
			s.syncProxyHealthTerminalLocked(task, now)
			continue
		}
		runID := s.taskRunIDs[task.ID]
		if runID == "" {
			runID = uuid.NewString()
			s.taskRunIDs[task.ID] = runID
		}
		if previous, ok := s.taskLeases[task.ID]; ok {
			attemptState := s.taskAttemptStates[previous.AttemptID]
			attemptState.status = "expired"
			attemptState.finishedAt = timePtr(now)
			attemptState.errorCode = "lease_expired"
			s.taskAttemptStates[previous.AttemptID] = attemptState
		}
		expiresAt := now.Add(leaseTTL)
		lease := taskservice.Lease{
			Task: task, RunID: runID, AttemptID: uuid.NewString(), Attempt: attempt, ExpiresAt: expiresAt,
		}
		lease.Task.Status = "leased"
		lease.Task.LeaseOwner = workerID
		lease.Task.LeaseExpiresAt = &expiresAt
		lease.Task.UpdatedAt = now
		lease.Task.ErrorCode = ""
		lease.Task.ErrorMessage = ""
		s.tasks[task.ID] = cloneTask(lease.Task)
		s.taskAttempts[task.ID] = attempt
		s.taskRuns[runID] = memoryTaskRun{status: "queued"}
		s.taskAttemptStates[lease.AttemptID] = memoryTaskAttempt{runID: runID, status: "leased"}
		s.taskLeases[task.ID] = lease
		return cloneLease(lease), nil
	}
	return taskservice.Lease{}, taskservice.ErrNoWork
}

func (s *Store) StartTask(_ context.Context, lease taskservice.Lease, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, task, ok := s.currentTaskLease(lease)
	if !ok || current.Task.Status != "leased" || !current.ExpiresAt.After(now) {
		return taskservice.ErrStateConflict
	}
	task.Status = "running"
	task.UpdatedAt = now
	s.tasks[task.ID] = task
	run := s.taskRuns[current.RunID]
	run.status = "running"
	s.taskRuns[current.RunID] = run
	attempt := s.taskAttemptStates[current.AttemptID]
	attempt.status = "running"
	s.taskAttemptStates[current.AttemptID] = attempt
	current.Task = task
	s.taskLeases[task.ID] = current
	return nil
}

func (s *Store) CompleteTask(_ context.Context, lease taskservice.Lease, _ map[string]interface{}, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, task, ok := s.currentTaskLease(lease)
	if !ok || !current.ExpiresAt.After(now) || (task.Status != "leased" && task.Status != "running") {
		return taskservice.ErrStateConflict
	}
	task.Status = "succeeded"
	task.CompletedAt = &now
	task.UpdatedAt = now
	task.LeaseOwner = ""
	task.LeaseExpiresAt = nil
	s.tasks[task.ID] = task
	run := s.taskRuns[current.RunID]
	run.status = "succeeded"
	run.finishedAt = timePtr(now)
	run.errorCode = ""
	s.taskRuns[current.RunID] = run
	attempt := s.taskAttemptStates[current.AttemptID]
	attempt.status = "succeeded"
	attempt.finishedAt = timePtr(now)
	attempt.errorCode = ""
	s.taskAttemptStates[current.AttemptID] = attempt
	receipt := current
	receipt.Task = task
	receipt.Task.LeaseOwner = current.Task.LeaseOwner
	s.taskLeaseReceipts[task.ID] = cloneLease(receipt)
	delete(s.taskLeases, task.ID)
	return nil
}

func (s *Store) FailTask(_ context.Context, lease taskservice.Lease, code, message string, retryable bool, retryAt, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, task, ok := s.currentTaskLease(lease)
	if !ok || !current.ExpiresAt.After(now) || (task.Status != "leased" && task.Status != "running") {
		return taskservice.ErrStateConflict
	}
	if retryable && lease.Attempt <= task.RetryLimit {
		task.Status = "queued"
		task.AvailableAt = retryAt
		task.CompletedAt = nil
	} else if retryable {
		task.Status = "dead_letter"
		task.CompletedAt = &now
	} else {
		task.Status = "failed"
		task.CompletedAt = &now
	}
	task.ErrorCode = code
	task.ErrorMessage = message
	task.UpdatedAt = now
	task.LeaseOwner = ""
	task.LeaseExpiresAt = nil
	s.tasks[task.ID] = task
	run := s.taskRuns[current.RunID]
	run.status = "failed"
	run.finishedAt = timePtr(now)
	run.errorCode = code
	s.taskRuns[current.RunID] = run
	attempt := s.taskAttemptStates[current.AttemptID]
	attempt.status = "failed"
	attempt.finishedAt = timePtr(now)
	attempt.errorCode = code
	s.taskAttemptStates[current.AttemptID] = attempt
	s.syncProxyHealthTerminalLocked(task, now)
	if task.Status != "queued" {
		receipt := current
		receipt.Task = task
		receipt.Task.LeaseOwner = current.Task.LeaseOwner
		s.taskLeaseReceipts[task.ID] = cloneLease(receipt)
	}
	delete(s.taskLeases, task.ID)
	return nil
}

func (s *Store) syncProxyHealthTerminalLocked(task taskservice.Task, now time.Time) {
	if task.TaskType != "proxy.health_check" {
		return
	}
	status, code := "failed", "task_failed"
	switch task.Status {
	case "cancelled":
		status, code = "cancelled", "task_cancelled"
	case "dead_letter":
		code = "retry_limit_exhausted"
	case "failed":
	default:
		return
	}
	for id, check := range s.proxyHealthChecks {
		if check.WorkspaceID == task.WorkspaceID && check.RequestID == task.ID && check.CompletedAt == nil {
			check.Status, check.ErrorCode, check.ErrorMessage, check.CompletedAt = status, code, "", &now
			s.proxyHealthChecks[id] = check
		}
	}
}

func (s *Store) currentTaskLease(lease taskservice.Lease) (taskservice.Lease, taskservice.Task, bool) {
	current, ok := s.taskLeases[lease.Task.ID]
	if !ok || current.AttemptID != lease.AttemptID || current.Task.LeaseOwner != lease.Task.LeaseOwner || current.Task.WorkspaceID != lease.Task.WorkspaceID || current.RunID != lease.RunID || current.Attempt != lease.Attempt {
		return taskservice.Lease{}, taskservice.Task{}, false
	}
	task, ok := s.tasks[lease.Task.ID]
	if ok && strings.HasPrefix(task.LeaseOwner, "device:") {
		deviceID := strings.TrimPrefix(task.LeaseOwner, "device:")
		device, exists := s.devices[deviceID]
		instanceID, _ := task.Payload["instanceId"].(string)
		instance, assigned := s.instances[instanceID]
		if !exists || device.RevokedAt != nil || device.WorkspaceID != task.WorkspaceID || !assigned || instance.DeletedAt != nil || instance.WorkspaceID != task.WorkspaceID || instance.AssignedDeviceID != deviceID {
			return taskservice.Lease{}, taskservice.Task{}, false
		}
	}
	return current, task, ok
}

func (s *Store) FindTaskLease(_ context.Context, workspaceID, taskID string) (taskservice.Lease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lease, ok := s.taskLeases[taskID]
	if !ok {
		lease, ok = s.taskLeaseReceipts[taskID]
	}
	if !ok || lease.Task.WorkspaceID != workspaceID {
		return taskservice.Lease{}, taskservice.ErrNotFound
	}
	owner := lease.Task.LeaseOwner
	lease.Task = s.tasks[taskID]
	lease.Task.LeaseOwner = owner
	return cloneLease(lease), nil
}

func (s *Store) ownerCountLocked(workspaceID string) int {
	count := 0
	for _, membership := range s.memberships {
		if membership.WorkspaceID == workspaceID && membership.Status == "active" && membership.Role == memberservice.RoleOwner {
			count++
		}
	}
	return count
}

func (s *Store) activeEntitlementLimitLocked(organizationID, code string) (*int64, bool) {
	entitlement, ok := s.entitlements[organizationID+"|"+code]
	now := time.Now().UTC()
	if !ok || !entitlement.FeatureEnabled || entitlement.ValidFrom.After(now) || (entitlement.ValidUntil != nil && !entitlement.ValidUntil.After(now)) {
		return nil, false
	}
	return entitlement.Limit, true
}

func (s *Store) organizationInstanceCountLocked(organizationID string) int64 {
	var count int64
	for _, instance := range s.instances {
		workspace, ok := s.workspaces[instance.WorkspaceID]
		if ok && workspace.OrganizationID == organizationID && instance.DeletedAt == nil {
			count++
		}
	}
	return count
}

func (s *Store) organizationHasMemberLocked(organizationID, userID string) bool {
	for _, membership := range s.memberships {
		workspace, ok := s.workspaces[membership.WorkspaceID]
		if ok && workspace.OrganizationID == organizationID && membership.UserID == userID && membership.Status == "active" {
			return true
		}
	}
	return false
}

func (s *Store) organizationMemberCountLocked(organizationID string) int64 {
	users := make(map[string]struct{})
	for _, membership := range s.memberships {
		workspace, ok := s.workspaces[membership.WorkspaceID]
		if ok && workspace.OrganizationID == organizationID && membership.Status == "active" {
			users[membership.UserID] = struct{}{}
		}
	}
	return int64(len(users))
}

func membershipKey(workspaceID, userID string) string { return workspaceID + ":" + userID }
func commandKey(workspaceID, key string) string       { return workspaceID + ":" + key }

func cloneUser(value authservice.User) authservice.User { return value }

func cloneDevice(value deviceservice.Device) deviceservice.Device {
	if value.Capabilities != nil {
		copyCapabilities := make(map[string]interface{}, len(value.Capabilities))
		for key, item := range value.Capabilities {
			copyCapabilities[key] = item
		}
		value.Capabilities = copyCapabilities
	}
	return value
}

func cloneInstance(value browserinstanceservice.BrowserInstance) browserinstanceservice.BrowserInstance {
	value.Tags = append([]string(nil), value.Tags...)
	return value
}

func cloneTask(value taskservice.Task) taskservice.Task {
	if value.Payload != nil {
		payload := make(map[string]interface{}, len(value.Payload))
		for key, item := range value.Payload {
			payload[key] = item
		}
		value.Payload = payload
	}
	return value
}

func cloneLease(value taskservice.Lease) taskservice.Lease {
	value.Task = cloneTask(value.Task)
	return value
}

func cloneFingerprintTemplate(value fingerprintservice.Template) fingerprintservice.Template {
	value.RuntimeArgs = append([]string(nil), value.RuntimeArgs...)
	value.Configuration.Fonts = append([]string(nil), value.Configuration.Fonts...)
	if value.Configuration.MaxTouchPoints != nil {
		copyValue := *value.Configuration.MaxTouchPoints
		value.Configuration.MaxTouchPoints = &copyValue
	}
	if value.Configuration.MediaDevices != nil {
		copyValue := *value.Configuration.MediaDevices
		value.Configuration.MediaDevices = &copyValue
	}
	if value.Configuration.Battery != nil {
		copyValue := *value.Configuration.Battery
		value.Configuration.Battery = &copyValue
	}
	return value
}

func cloneCloudProfile(value profilesyncservice.Profile) profilesyncservice.Profile { return value }

func cloneProfileManifest(value profilesyncservice.Manifest) profilesyncservice.Manifest {
	value.Files = append([]profilesyncservice.ManifestFile(nil), value.Files...)
	value.DeletedPaths = append([]string(nil), value.DeletedPaths...)
	return value
}

func cloneProfileObjects(values []profilesyncservice.Object) []profilesyncservice.Object {
	return append([]profilesyncservice.Object(nil), values...)
}

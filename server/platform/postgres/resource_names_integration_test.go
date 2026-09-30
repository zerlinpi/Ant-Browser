package postgres_test

import (
	"context"
	"encoding/base32"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	"github.com/zerlinpi/Ant-Browser/server/platform/secureenvelope"
	"github.com/zerlinpi/Ant-Browser/server/platform/security"
	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	profilesyncservice "github.com/zerlinpi/Ant-Browser/server/services/profile-sync-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	scheduleservice "github.com/zerlinpi/Ant-Browser/server/services/schedule-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

// TestResourceRulesAgainstPostgres covers migration 027 and the store error
// mapping: names (and account identifiers per platform) are unique among
// live rows, case-insensitively, and conflicts surface as typed errors. It
// also reads proxy assignments with their proxy name and pauses/resumes a
// schedule. Everything runs as the RLS-enforced ant_control_plane role.
func TestResourceRulesAgainstPostgres(t *testing.T) {
	databaseURL := os.Getenv("ANT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ANT_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	migrationsPath, err := filepath.Abs(filepath.Join("..", "..", "..", "database", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, databaseURL, migrationsPath); err != nil {
		t.Fatal(err)
	}
	admin, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const runtimePassword = "integration-control-plane"
	if _, err := pool.Exec(ctx, `ALTER ROLE ant_control_plane WITH LOGIN PASSWORD '`+runtimePassword+`'`); err != nil {
		t.Fatal(err)
	}
	runtimeURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	runtimeURL.User = url.UserPassword("ant_control_plane", runtimePassword)
	store, err := postgres.Open(ctx, runtimeURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const workerPassword = "integration-worker"
	if _, err := pool.Exec(ctx, `ALTER ROLE ant_worker WITH LOGIN PASSWORD '`+workerPassword+`'`); err != nil {
		t.Fatal(err)
	}
	workerURL := *runtimeURL
	workerURL.User = url.UserPassword("ant_worker", workerPassword)
	worker, err := postgres.Open(ctx, workerURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	owner := authservice.User{
		ID: uuid.NewString(), Email: uuid.NewString() + "@resource-rules.test", PasswordHash: "test",
		DisplayName: "Owner", Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := admin.CreateUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	suffix := uuid.NewString()[:8]
	organization := workspaceservice.Organization{ID: uuid.NewString(), Name: "Rules", Slug: "org-" + suffix, Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	workspace := workspaceservice.Workspace{ID: uuid.NewString(), OrganizationID: organization.ID, Name: "Rules", Slug: "ws-" + suffix, Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := admin.CreateOrganizationWorkspace(ctx, organization, workspace, workspaceservice.Membership{
		ID: uuid.NewString(), WorkspaceID: workspace.ID, UserID: owner.ID, Role: memberservice.RoleOwner, Status: "active", JoinedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	user := postgres.WithTenantScope(ctx, postgres.TenantScope{WorkspaceID: workspace.ID, UserID: owner.ID})

	t.Run("instance names", func(t *testing.T) {
		// The Free plan allows three live instances; stay below it so the
		// quota trigger cannot mask a name conflict.
		instance := func(name string) browserinstanceservice.BrowserInstance {
			return browserinstanceservice.BrowserInstance{
				ID: uuid.NewString(), WorkspaceID: workspace.ID, Name: name, Platform: "chromium",
				DesiredState: "stopped", ObservedState: "offline", Version: 1, Tags: []string{}, CreatedAt: now, UpdatedAt: now,
			}
		}
		first := instance("Shop 1")
		if err := store.CreateInstance(user, first); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateInstance(user, instance("SHOP 1")); !errors.Is(err, browserinstanceservice.ErrNameConflict) {
			t.Fatalf("case-variant duplicate error=%v", err)
		}
		second := instance("Shop 2")
		if err := store.CreateInstance(user, second); err != nil {
			t.Fatal(err)
		}
		second.Name = "shop 1"
		if _, err := store.UpdateInstance(user, second, 1); !errors.Is(err, browserinstanceservice.ErrNameConflict) {
			t.Fatalf("rename conflict error=%v", err)
		}
		if err := store.SoftDeleteInstance(user, workspace.ID, first.ID, 1, now); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateInstance(user, instance("Shop 1")); err != nil {
			t.Fatalf("name of a deleted instance was not freed: %v", err)
		}
	})

	t.Run("proxy names and assignment reads", func(t *testing.T) {
		proxies := proxyservice.New(store, allowProfileAuthorizer{})
		residential, err := proxies.Create(user, owner.ID, workspace.ID, proxyservice.CreateInput{Name: "Residential", Protocol: "direct", ConnectorType: proxyservice.ConnectorXray})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := proxies.Create(user, owner.ID, workspace.ID, proxyservice.CreateInput{Name: "residential", Protocol: "direct", ConnectorType: proxyservice.ConnectorXray}); !errors.Is(err, proxyservice.ErrNameConflict) {
			t.Fatalf("proxy duplicate error=%v", err)
		}
		datacenter, err := proxies.Create(user, owner.ID, workspace.ID, proxyservice.CreateInput{Name: "Datacenter", Protocol: "direct", ConnectorType: proxyservice.ConnectorXray})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := proxies.Update(user, owner.ID, workspace.ID, datacenter.ID, proxyservice.UpdateInput{Name: "RESIDENTIAL", Protocol: "direct", ConnectorType: proxyservice.ConnectorXray, Version: datacenter.Version}); !errors.Is(err, proxyservice.ErrNameConflict) {
			t.Fatalf("proxy rename conflict error=%v", err)
		}

		instances, err := store.ListInstances(user, workspace.ID)
		if err != nil || len(instances) == 0 {
			t.Fatalf("instances=%v err=%v", instances, err)
		}
		target := instances[0]
		assigned, err := proxies.Assign(user, owner.ID, workspace.ID, residential.ID, target.ID, "browser_instance")
		if err != nil {
			t.Fatal(err)
		}
		if assigned.ProxyName != "Residential" {
			t.Fatalf("assign result has proxy name %q", assigned.ProxyName)
		}
		if _, err := proxies.Assign(user, owner.ID, workspace.ID, datacenter.ID, target.ID, "browser_instance"); !errors.Is(err, proxyservice.ErrAssignmentConflict) {
			t.Fatalf("reassignment error=%v", err)
		}
		read, err := proxies.GetAssignment(user, owner.ID, workspace.ID, "browser_instance", target.ID)
		if err != nil || read.ID != assigned.ID || read.ProxyName != "Residential" || read.Version != 1 {
			t.Fatalf("assignment read=%+v err=%v", read, err)
		}
		all, err := proxies.ListAssignments(user, owner.ID, workspace.ID, proxyservice.AssignmentFilter{})
		if err != nil || len(all) != 1 || all[0].ProxyName != "Residential" {
			t.Fatalf("assignments=%+v err=%v", all, err)
		}
		none, err := proxies.ListAssignments(user, owner.ID, workspace.ID, proxyservice.AssignmentFilter{ProxyID: datacenter.ID})
		if err != nil || len(none) != 0 {
			t.Fatalf("datacenter assignments=%+v err=%v", none, err)
		}
		accountsOnly, err := proxies.ListAssignments(user, owner.ID, workspace.ID, proxyservice.AssignmentFilter{TargetType: "account"})
		if err != nil || len(accountsOnly) != 0 {
			t.Fatalf("account assignments=%+v err=%v", accountsOnly, err)
		}
		if err := proxies.Unassign(user, owner.ID, workspace.ID, target.ID, "browser_instance", read.Version); err != nil {
			t.Fatal(err)
		}
		if _, err := proxies.GetAssignment(user, owner.ID, workspace.ID, "browser_instance", target.ID); !errors.Is(err, proxyservice.ErrNotFound) {
			t.Fatalf("released assignment read error=%v", err)
		}
		if err := proxies.Delete(user, owner.ID, workspace.ID, residential.ID, residential.Version); err != nil {
			t.Fatal(err)
		}
		if _, err := proxies.Create(user, owner.ID, workspace.ID, proxyservice.CreateInput{Name: "Residential", Protocol: "direct", ConnectorType: proxyservice.ConnectorXray}); err != nil {
			t.Fatalf("name of a deleted proxy was not freed: %v", err)
		}
	})

	t.Run("account identifiers", func(t *testing.T) {
		accounts := accountservice.New(store, allowProfileAuthorizer{})
		seller, err := accounts.Create(user, owner.ID, workspace.ID, accountservice.CreateInput{Platform: "amazon", Name: "Seller", Identifier: "seller@example.test"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := accounts.Create(user, owner.ID, workspace.ID, accountservice.CreateInput{Platform: "amazon", Name: "Seller again", Identifier: "Seller@Example.TEST"}); !errors.Is(err, accountservice.ErrIdentifierConflict) {
			t.Fatalf("identifier duplicate error=%v", err)
		}
		if _, err := accounts.Create(user, owner.ID, workspace.ID, accountservice.CreateInput{Platform: "ebay", Name: "Seller", Identifier: "seller@example.test"}); err != nil {
			t.Fatalf("same identifier on another platform: %v", err)
		}
		second, err := accounts.Create(user, owner.ID, workspace.ID, accountservice.CreateInput{Platform: "amazon", Name: "Second", Identifier: "second@example.test"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := accounts.Update(user, owner.ID, workspace.ID, second.ID, second.Version, accountservice.UpdateInput{Name: "Second", Identifier: "SELLER@example.test"}); !errors.Is(err, accountservice.ErrIdentifierConflict) {
			t.Fatalf("identifier rename conflict error=%v", err)
		}
		if err := accounts.Delete(user, owner.ID, workspace.ID, seller.ID, seller.Version); err != nil {
			t.Fatal(err)
		}
		if _, err := accounts.Create(user, owner.ID, workspace.ID, accountservice.CreateInput{Platform: "amazon", Name: "Seller", Identifier: "seller@example.test"}); err != nil {
			t.Fatalf("identifier of a deleted account was not freed: %v", err)
		}
		if _, err := accounts.RecordRiskEvent(user, owner.ID, workspace.ID, second.ID, "info", "policy_warning", ""); err != nil {
			t.Fatalf("info risk event rejected by risk_events.severity: %v", err)
		}
	})

	t.Run("profile and workflow names", func(t *testing.T) {
		profiles := profilesyncservice.New(store, allowProfileAuthorizer{}, security.NewOpaqueToken, profilesyncservice.MetadataVerifier{}, "integration-key", "memory-test")
		if _, err := profiles.CreateProfile(user, owner.ID, workspace.ID, profilesyncservice.CreateProfileInput{Name: "Storefront"}); err != nil {
			t.Fatal(err)
		}
		if _, err := profiles.CreateProfile(user, owner.ID, workspace.ID, profilesyncservice.CreateProfileInput{Name: "STOREFRONT"}); !errors.Is(err, profilesyncservice.ErrNameConflict) {
			t.Fatalf("profile duplicate error=%v", err)
		}
		workflows := automationservice.New(store, allowProfileAuthorizer{})
		definition := automationservice.Definition{Engine: "playwright", Steps: []automationservice.Step{{ID: "open", Action: "navigate", Parameters: map[string]interface{}{"url": "https://example.com"}}}}
		if _, _, err := workflows.Create(user, owner.ID, workspace.ID, automationservice.CreateInput{Name: "Nightly", Definition: definition}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := workflows.Create(user, owner.ID, workspace.ID, automationservice.CreateInput{Name: "Nightly", Definition: definition}); !errors.Is(err, automationservice.ErrNameConflict) {
			t.Fatalf("workflow duplicate error=%v", err)
		}
	})

	t.Run("schedule pause and resume", func(t *testing.T) {
		workflows := automationservice.New(store, allowProfileAuthorizer{})
		definition := automationservice.Definition{Engine: "playwright", Steps: []automationservice.Step{{ID: "open", Action: "navigate", Parameters: map[string]interface{}{"url": "https://example.com"}}}}
		workflow, version, err := workflows.Create(user, owner.ID, workspace.ID, automationservice.CreateInput{Name: "Scheduled", Definition: definition})
		if err != nil {
			t.Fatal(err)
		}
		instances, err := store.ListInstances(user, workspace.ID)
		if err != nil || len(instances) == 0 {
			t.Fatalf("instances=%v err=%v", instances, err)
		}
		stale := now.Add(-72 * time.Hour)
		schedule, err := store.CreateSchedule(user, scheduleservice.Schedule{
			ID: uuid.NewString(), WorkspaceID: workspace.ID, WorkflowID: workflow.ID, WorkflowVersionID: version.ID,
			InstanceID: instances[0].ID, CronExpression: "0 * * * *", Timezone: "UTC", Enabled: true, Status: "active",
			NextRunAt: &stale, Version: 1, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		schedules := scheduleservice.New(store, nil, nil, nil)
		paused, err := schedules.Disable(user, owner.ID, workspace.ID, schedule.ID)
		if err != nil {
			t.Fatal(err)
		}
		if paused.Enabled || paused.Status != "paused" || paused.NextRunAt != nil {
			t.Fatalf("paused schedule=%+v", paused)
		}
		resumed, err := schedules.Enable(user, owner.ID, workspace.ID, schedule.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !resumed.Enabled || resumed.Status != "active" || resumed.NextRunAt == nil || !resumed.NextRunAt.After(time.Now().Add(-time.Minute)) || resumed.NextRunAt.UTC().Minute() != 0 {
			t.Fatalf("resumed schedule=%+v next=%v", resumed, resumed.NextRunAt)
		}
		if _, err := store.SetScheduleEnabled(user, workspace.ID, schedule.ID, false, nil, paused.Version, now); !errors.Is(err, scheduleservice.ErrVersionConflict) {
			t.Fatalf("stale enable toggle error=%v", err)
		}

		// The worker dispatches a due schedule of a published workflow as the
		// ant_worker role and advances its next run.
		if _, err := workflows.Publish(user, owner.ID, workspace.ID, workflow.ID, automationservice.StateInput{ExpectedVersion: workflow.Version, WorkflowVersion: version.Version}); err != nil {
			t.Fatal(err)
		}
		due := time.Now().UTC().Add(-time.Minute).Truncate(time.Minute)
		dueSchedule, err := store.CreateSchedule(user, scheduleservice.Schedule{
			ID: uuid.NewString(), WorkspaceID: workspace.ID, WorkflowID: workflow.ID, WorkflowVersionID: version.ID,
			InstanceID: instances[0].ID, CronExpression: "* * * * *", Timezone: "UTC", Enabled: true, Status: "active",
			NextRunAt: &due, Version: 1, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		// The claim is cross-workspace; a reused test database can hold due
		// schedules of earlier runs, so only this schedule is inspected.
		ownDispatches := func(dispatches []scheduleservice.Dispatch) []scheduleservice.Dispatch {
			var own []scheduleservice.Dispatch
			for _, dispatch := range dispatches {
				if dispatch.Schedule.ID == dueSchedule.ID {
					own = append(own, dispatch)
				}
			}
			return own
		}
		t.Cleanup(func() {
			if current, err := store.FindSchedule(user, workspace.ID, dueSchedule.ID); err == nil {
				_, _ = store.SetScheduleEnabled(user, workspace.ID, dueSchedule.ID, false, nil, current.Version, time.Now().UTC())
			}
		})
		dispatchedAt := time.Now().UTC()
		dispatches, err := worker.ClaimDueSchedules(ctx, "integration-worker", dispatchedAt, 100)
		if err != nil {
			t.Fatalf("schedule dispatch as ant_worker: %v", err)
		}
		own := ownDispatches(dispatches)
		if len(own) != 1 || own[0].Task.TaskType != "workflow.execute" ||
			own[0].Task.WorkflowVersionID != version.ID || !own[0].Task.AvailableAt.Equal(due) {
			t.Fatalf("dispatches=%+v", own)
		}
		advanced, err := store.FindSchedule(user, workspace.ID, dueSchedule.ID)
		if err != nil {
			t.Fatal(err)
		}
		if advanced.LastRunAt == nil || !advanced.LastRunAt.Equal(due) || advanced.NextRunAt == nil || !advanced.NextRunAt.After(dispatchedAt) || advanced.Status != "active" {
			t.Fatalf("dispatched schedule=%+v", advanced)
		}
		if again, err := worker.ClaimDueSchedules(ctx, "integration-worker", dispatchedAt, 100); err != nil || len(ownDispatches(again)) != 0 {
			t.Fatalf("second dispatch=%+v err=%v", ownDispatches(again), err)
		}
	})

	t.Run("user sessions", func(t *testing.T) {
		auth := authservice.New(store, nil, nil, nil, time.Hour)
		saveSession := func(ip, userAgent string, lastSeen time.Time, expiresAt time.Time) string {
			t.Helper()
			id := uuid.NewString()
			if err := store.SaveSession(ctx, authservice.Session{
				ID: id, UserID: owner.ID, UserAgent: userAgent, IPAddress: ip,
				CreatedAt: lastSeen, LastSeenAt: lastSeen, ExpiresAt: expiresAt,
			}, authservice.RefreshToken{
				ID: uuid.NewString(), SessionID: id, TokenHash: uuid.NewString()[:8] + "00000000000000000000000000000000000000000000000000000000",
				CreatedAt: lastSeen, ExpiresAt: expiresAt,
			}); err != nil {
				t.Fatal(err)
			}
			return id
		}
		current := saveSession("203.0.113.7", "Desktop", now.Add(-2*time.Hour), now.Add(time.Hour))
		phone := saveSession("2001:db8::1", "Phone", now.Add(-time.Minute), now.Add(time.Hour))
		saveSession("198.51.100.1", "Expired", now.Add(-3*time.Hour), now.Add(-time.Minute))

		sessions, err := auth.ListSessions(ctx, owner.ID, current)
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 2 || sessions[0].ID != current || !sessions[0].Current || sessions[0].IPAddress != "203.0.113.7" ||
			sessions[1].ID != phone || sessions[1].IPAddress != "2001:db8::1" {
			t.Fatalf("sessions=%+v", sessions)
		}
		if err := auth.RevokeUserSession(ctx, owner.ID, current, uuid.NewString()); !errors.Is(err, authservice.ErrNotFound) {
			t.Fatalf("unknown session revoke error=%v", err)
		}
		revoked, err := auth.RevokeOtherSessions(ctx, owner.ID, current)
		if err != nil || len(revoked) != 1 || revoked[0] != phone {
			t.Fatalf("revoked=%v err=%v", revoked, err)
		}
		if active, err := store.SessionActive(ctx, owner.ID, phone); err != nil || active {
			t.Fatalf("revoked session active=%v err=%v", active, err)
		}
		var liveTokens int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM refresh_tokens WHERE session_id = $1::uuid AND revoked_at IS NULL`, phone).Scan(&liveTokens); err != nil || liveTokens != 0 {
			t.Fatalf("refresh tokens of a revoked session left live: %d err=%v", liveTokens, err)
		}
		if err := auth.RevokeUserSession(ctx, owner.ID, current, current); err != nil {
			t.Fatal(err)
		}
		if remaining, err := auth.ListSessions(ctx, owner.ID, current); err != nil || len(remaining) != 0 {
			t.Fatalf("remaining=%+v err=%v", remaining, err)
		}
	})

	// Migration 029: TOTP factors, recovery codes and login challenges, run
	// through the auth service as ant_control_plane.
	t.Run("user mfa", func(t *testing.T) {
		const password = "SecurePassword123"
		passwords := security.NewPasswords()
		passwordHash, err := passwords.Hash(password)
		if err != nil {
			t.Fatal(err)
		}
		mfaUser := authservice.User{
			ID: uuid.NewString(), Email: uuid.NewString() + "@mfa.test", PasswordHash: passwordHash,
			DisplayName: "MFA", Status: "active", CreatedAt: now, UpdatedAt: now,
		}
		if err := admin.CreateUser(ctx, mfaUser); err != nil {
			t.Fatal(err)
		}
		crypto, err := secureenvelope.New("MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=", "integration-mfa", "v1")
		if err != nil {
			t.Fatal(err)
		}
		sealer, err := secureenvelope.NewTextSealer(crypto)
		if err != nil {
			t.Fatal(err)
		}
		auth := authservice.New(store, passwords, security.NewTokens("integration", "01234567890123456789012345678901", time.Minute), security.NewOpaqueToken, time.Hour)
		auth.ConfigureMFA(sealer, "Ant Browser")
		code := func(secret string, offset time.Duration) string {
			t.Helper()
			value, err := authservice.GenerateTOTPCode(secret, time.Now().Add(offset))
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
		challenge := func() string {
			t.Helper()
			result, err := auth.Login(ctx, authservice.LoginInput{Email: mfaUser.Email, Password: password, DeviceID: "integration-device"}, authservice.SessionMetadata{})
			if err != nil || !result.MFARequired || result.MFAChallenge == nil {
				t.Fatalf("login result=%+v err=%v", result, err)
			}
			return result.MFAChallenge.Token
		}
		metadata := authservice.SessionMetadata{UserAgent: "integration", IPAddress: "203.0.113.9"}

		if _, err := auth.SetupTOTP(ctx, mfaUser.ID, password); err != nil {
			t.Fatal(err)
		}
		// A second setup replaces the unconfirmed one.
		setup, err := auth.SetupTOTP(ctx, mfaUser.ID, password)
		if err != nil {
			t.Fatal(err)
		}
		// Reused verbatim below: recomputing it could land on the next step.
		confirmCode := code(setup.Secret, 0)
		recovery, err := auth.ConfirmTOTP(ctx, mfaUser.ID, confirmCode)
		if err != nil || len(recovery.Codes) != 10 {
			t.Fatalf("confirm codes=%v err=%v", recovery.Codes, err)
		}
		if _, err := auth.SetupTOTP(ctx, mfaUser.ID, password); !errors.Is(err, authservice.ErrMFAAlreadyEnabled) {
			t.Fatalf("setup while enabled error=%v", err)
		}
		if status, err := auth.MFAStatus(ctx, mfaUser.ID); err != nil || !status.Enabled || status.RecoveryCodesRemaining != 10 {
			t.Fatalf("status=%+v err=%v", status, err)
		}
		var sealed string
		if err := pool.QueryRow(ctx, `SELECT secret_envelope FROM user_mfa_factors WHERE user_id = $1::uuid`, mfaUser.ID).Scan(&sealed); err != nil {
			t.Fatal(err)
		}
		if raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setup.Secret); err != nil || strings.Contains(sealed, setup.Secret) || strings.Contains(sealed, string(raw)) {
			t.Fatalf("the TOTP secret is stored unsealed: %s", sealed)
		}

		pending := challenge()
		if _, err := auth.VerifyMFA(ctx, authservice.MFAVerifyInput{ChallengeToken: pending, Code: confirmCode}, metadata); !errors.Is(err, authservice.ErrMFAInvalidCode) {
			t.Fatalf("replayed confirmation code error=%v", err)
		}
		pair, err := auth.VerifyMFA(ctx, authservice.MFAVerifyInput{ChallengeToken: pending, Code: code(setup.Secret, 30*time.Second)}, metadata)
		if err != nil {
			t.Fatal(err)
		}
		if active, err := store.SessionActive(ctx, mfaUser.ID, pair.SessionID); err != nil || !active {
			t.Fatalf("verified session active=%v err=%v", active, err)
		}
		var deviceID string
		if err := pool.QueryRow(ctx, `SELECT device_id FROM sessions WHERE id = $1::uuid`, pair.SessionID).Scan(&deviceID); err != nil || deviceID != "integration-device" {
			t.Fatalf("session device=%q err=%v", deviceID, err)
		}
		if _, err := auth.VerifyMFA(ctx, authservice.MFAVerifyInput{ChallengeToken: pending, RecoveryCode: recovery.Codes[0]}, metadata); !errors.Is(err, authservice.ErrMFAChallengeInvalid) {
			t.Fatalf("consumed challenge error=%v", err)
		}
		if _, err := auth.VerifyMFA(ctx, authservice.MFAVerifyInput{ChallengeToken: challenge(), RecoveryCode: recovery.Codes[0]}, metadata); err != nil {
			t.Fatalf("recovery code sign-in: %v", err)
		}
		if _, err := auth.VerifyMFA(ctx, authservice.MFAVerifyInput{ChallengeToken: challenge(), RecoveryCode: recovery.Codes[0]}, metadata); !errors.Is(err, authservice.ErrMFAInvalidCode) {
			t.Fatalf("reused recovery code error=%v", err)
		}
		fresh, err := auth.RegenerateRecoveryCodes(ctx, mfaUser.ID, authservice.MFACodeInput{RecoveryCode: recovery.Codes[1]})
		if err != nil || len(fresh.Codes) != 10 {
			t.Fatalf("regenerated=%v err=%v", fresh.Codes, err)
		}
		var storedCodes int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_mfa_recovery_codes WHERE user_id = $1::uuid AND used_at IS NULL`, mfaUser.ID).Scan(&storedCodes); err != nil || storedCodes != 10 {
			t.Fatalf("stored recovery codes=%d err=%v", storedCodes, err)
		}

		// Lockout accounting against explicit clocks.
		base := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		for attempt := 1; attempt <= 5; attempt++ {
			factor, err := store.BeginMFAAttempt(ctx, mfaUser.ID, authservice.MFAStatusActive, base, 5, 15*time.Minute)
			if err != nil || factor.FailedAttempts != attempt || (attempt == 5) != (factor.LockedUntil != nil) {
				t.Fatalf("attempt %d factor=%+v err=%v", attempt, factor, err)
			}
		}
		var locked *authservice.MFALockedError
		if _, err := store.BeginMFAAttempt(ctx, mfaUser.ID, authservice.MFAStatusActive, base.Add(time.Minute), 5, 15*time.Minute); !errors.As(err, &locked) || !locked.Until.Equal(base.Add(15*time.Minute)) {
			t.Fatalf("attempt while locked error=%v", err)
		}
		if factor, err := store.BeginMFAAttempt(ctx, mfaUser.ID, authservice.MFAStatusActive, base.Add(16*time.Minute), 5, 15*time.Minute); err != nil || factor.FailedAttempts != 1 || factor.LockedUntil != nil {
			t.Fatalf("attempt after the lockout factor=%+v err=%v", factor, err)
		}
		if _, err := store.BeginMFAAttempt(ctx, mfaUser.ID, authservice.MFAStatusPending, base, 5, 15*time.Minute); !errors.Is(err, authservice.ErrNotFound) {
			t.Fatalf("attempt against the wrong status error=%v", err)
		}
		if accepted, err := store.AcceptTOTPStep(ctx, mfaUser.ID, 1, base); err != nil || accepted {
			t.Fatalf("stale TOTP step accepted=%v err=%v", accepted, err)
		}

		// ant_worker cannot read second factors.
		workerPool, err := pgxpool.New(ctx, workerURL.String())
		if err != nil {
			t.Fatal(err)
		}
		defer workerPool.Close()
		var pgError *pgconn.PgError
		if _, err := workerPool.Exec(ctx, `SELECT 1 FROM user_mfa_factors LIMIT 1`); !errors.As(err, &pgError) || pgError.Code != "42501" {
			t.Fatalf("worker read of user_mfa_factors error=%v", err)
		}

		leftover := challenge()
		if err := auth.DisableMFA(ctx, mfaUser.ID, authservice.DisableMFAInput{Password: password, RecoveryCode: fresh.Codes[0]}); err != nil {
			t.Fatal(err)
		}
		var remainingCodes, remainingChallenges int
		if err := pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM user_mfa_recovery_codes WHERE user_id = $1::uuid),
			       (SELECT count(*) FROM mfa_login_challenges WHERE user_id = $1::uuid)
		`, mfaUser.ID).Scan(&remainingCodes, &remainingChallenges); err != nil || remainingCodes != 0 || remainingChallenges != 0 {
			t.Fatalf("after disable codes=%d challenges=%d err=%v", remainingCodes, remainingChallenges, err)
		}
		if _, err := auth.VerifyMFA(ctx, authservice.MFAVerifyInput{ChallengeToken: leftover, RecoveryCode: fresh.Codes[1]}, metadata); !errors.Is(err, authservice.ErrMFAChallengeInvalid) {
			t.Fatalf("challenge from before disable error=%v", err)
		}
		if result, err := auth.Login(ctx, authservice.LoginInput{Email: mfaUser.Email, Password: password}, metadata); err != nil || result.MFARequired || result.TokenPair == nil {
			t.Fatalf("login after disable=%+v err=%v", result, err)
		}
	})

	// Every RLS policy used to call cloud_current_workspace_id(), which read
	// workspaces with the caller's privileges; ant_worker has none there, so
	// it could not claim or finish any task. The fencing test in
	// integration_test.go runs as the table owner and never saw that.
	t.Run("worker task lifecycle", func(t *testing.T) {
		queued := time.Now().UTC()
		task, err := store.CreateTask(user, taskservice.Task{
			ID: uuid.NewString(), WorkspaceID: workspace.ID, TaskType: "system.healthcheck", IdempotencyKey: uuid.NewString(),
			Status: "queued", RetryLimit: 1, AvailableAt: queued, CreatedAt: queued, UpdatedAt: queued,
		})
		if err != nil {
			t.Fatal(err)
		}
		lease, err := worker.ClaimNextTask(ctx, "integration-worker", []string{"system.healthcheck"}, time.Minute, time.Now().UTC())
		if err != nil {
			t.Fatalf("task claim as ant_worker: %v", err)
		}
		if lease.Task.ID != task.ID {
			t.Fatalf("claimed %+v, want task %s", lease.Task, task.ID)
		}
		if err := worker.StartTask(ctx, lease, time.Now().UTC()); err != nil {
			t.Fatalf("task start as ant_worker: %v", err)
		}
		if err := worker.CompleteTask(ctx, lease, map[string]interface{}{"ok": true}, time.Now().UTC()); err != nil {
			t.Fatalf("task completion as ant_worker: %v", err)
		}
		finished, err := store.FindTask(user, workspace.ID, task.ID)
		if err != nil || finished.Status != "succeeded" {
			t.Fatalf("finished task=%+v err=%v", finished, err)
		}
	})
}

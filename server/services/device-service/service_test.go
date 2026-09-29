package deviceservice_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
	billingservice "github.com/zerlinpi/Ant-Browser/server/services/billing-service"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

// testSecret issues predictable credentials: the stored hash is "hash:" + raw.
func testSecret() (string, string, error) {
	raw := uuid.NewString()
	return raw, testHash(raw), nil
}

func testHash(raw string) string { return "hash:" + raw }

type deviceFixture struct {
	ctx        context.Context
	store      *memory.Store
	workspaces *workspaceservice.Service
	devices    *deviceservice.Service
	workspace  workspaceservice.Workspace
	users      map[memberservice.Role]string
}

func newDeviceFixture(t *testing.T) deviceFixture {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	f := deviceFixture{ctx: ctx, store: store, workspaces: workspaceservice.New(store), users: map[memberservice.Role]string{}}
	f.devices = deviceservice.New(store, testSecret, f.workspaces)
	for _, role := range []memberservice.Role{memberservice.RoleOwner, memberservice.RoleManager, memberservice.RoleOperator, memberservice.RoleViewer} {
		id := uuid.NewString()
		if err := store.CreateUser(ctx, authservice.User{ID: id, Email: string(role) + "@example.com", Status: "active"}); err != nil {
			t.Fatal(err)
		}
		f.users[role] = id
	}
	workspace, err := f.workspaces.Create(ctx, f.users[memberservice.RoleOwner], workspaceservice.CreateInput{Name: "Devices"})
	if err != nil {
		t.Fatal(err)
	}
	f.workspace = workspace
	if err := store.UpsertEntitlement(ctx, billingservice.Entitlement{
		OrganizationID: workspace.OrganizationID, Code: billingservice.EntitlementTeamMembers,
		FeatureEnabled: true, ValidFrom: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	for _, role := range []memberservice.Role{memberservice.RoleManager, memberservice.RoleOperator, memberservice.RoleViewer} {
		if _, err := f.workspaces.AddMember(ctx, f.users[memberservice.RoleOwner], workspace.ID, f.users[role], role); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f deviceFixture) register(role memberservice.Role) (deviceservice.Registration, error) {
	return f.devices.Register(f.ctx, f.users[role], deviceservice.RegisterInput{
		WorkspaceID: f.workspace.ID, Name: "Agent " + string(role), Platform: "windows",
	})
}

func TestRegisterRequiresInstanceOperate(t *testing.T) {
	f := newDeviceFixture(t)
	if _, err := f.register(memberservice.RoleViewer); !errors.Is(err, workspaceservice.ErrForbidden) {
		t.Fatalf("viewer enrolled an execution agent: %v", err)
	}
	outsider := uuid.NewString()
	if _, err := f.devices.Register(f.ctx, outsider, deviceservice.RegisterInput{WorkspaceID: f.workspace.ID, Name: "Agent", Platform: "linux"}); !errors.Is(err, workspaceservice.ErrForbidden) {
		t.Fatalf("non-member enrolled a device: %v", err)
	}
	for _, role := range []memberservice.Role{memberservice.RoleOperator, memberservice.RoleManager, memberservice.RoleOwner} {
		registration, err := f.register(role)
		if err != nil {
			t.Fatalf("%s could not register a device: %v", role, err)
		}
		if registration.Credential == "" || registration.Device.WorkspaceID != f.workspace.ID || registration.Device.UserID != f.users[role] {
			t.Fatalf("unexpected registration for %s: %+v", role, registration)
		}
	}
}

func TestRotateCredentialInvalidatesPreviousCredential(t *testing.T) {
	f := newDeviceFixture(t)
	operator := f.users[memberservice.RoleOperator]
	registration, err := f.register(memberservice.RoleOperator)
	if err != nil {
		t.Fatal(err)
	}
	deviceID := registration.Device.ID
	if _, err := f.devices.Authenticate(f.ctx, deviceID, testHash(registration.Credential)); err != nil {
		t.Fatalf("original credential rejected: %v", err)
	}

	if _, err := f.devices.RotateCredential(f.ctx, f.users[memberservice.RoleOwner], deviceID); !errors.Is(err, deviceservice.ErrNotFound) {
		t.Fatalf("non-owner rotated another user's device: %v", err)
	}
	rotated, err := f.devices.RotateCredential(f.ctx, operator, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Device.ID != deviceID || rotated.Credential == "" || rotated.Credential == registration.Credential {
		t.Fatalf("unexpected rotation result: %+v", rotated)
	}
	if _, err := f.devices.Authenticate(f.ctx, deviceID, testHash(registration.Credential)); !errors.Is(err, deviceservice.ErrRevoked) {
		t.Fatalf("previous credential still authenticates: %v", err)
	}
	if device, err := f.devices.Authenticate(f.ctx, deviceID, testHash(rotated.Credential)); err != nil || device.ID != deviceID {
		t.Fatalf("rotated credential rejected: %+v %v", device, err)
	}

	if _, err := f.devices.RotateCredential(f.ctx, operator, "not-a-device-id"); !errors.Is(err, deviceservice.ErrNotFound) {
		t.Fatalf("malformed device ID: %v", err)
	}
	if err := f.devices.Revoke(f.ctx, operator, deviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.devices.RotateCredential(f.ctx, operator, deviceID); !errors.Is(err, deviceservice.ErrRevoked) {
		t.Fatalf("revoked device was rotated: %v", err)
	}
	if _, err := f.devices.Authenticate(f.ctx, deviceID, testHash(rotated.Credential)); !errors.Is(err, deviceservice.ErrRevoked) {
		t.Fatalf("revoked device authenticates: %v", err)
	}
}

// stubRepository answers AuthenticateDevice with a fixed result.
type stubRepository struct {
	deviceservice.Repository
	device deviceservice.Device
	err    error
	calls  int
}

func (s *stubRepository) AuthenticateDevice(context.Context, string, string) (deviceservice.Device, error) {
	s.calls++
	return s.device, s.err
}

func TestAuthenticateSeparatesRejectionsFromInfrastructureErrors(t *testing.T) {
	deviceID := uuid.NewString()
	revokedAt := time.Now().UTC()
	outage := errors.New("connection refused")
	for name, scenario := range map[string]struct {
		repository *stubRepository
		deviceID   string
		hash       string
		wantErr    error
		wantCalls  int
	}{
		"not found":         {repository: &stubRepository{err: deviceservice.ErrNotFound}, deviceID: deviceID, hash: "h", wantErr: deviceservice.ErrRevoked, wantCalls: 1},
		"revoked":           {repository: &stubRepository{err: deviceservice.ErrRevoked}, deviceID: deviceID, hash: "h", wantErr: deviceservice.ErrRevoked, wantCalls: 1},
		"revoked device":    {repository: &stubRepository{device: deviceservice.Device{ID: deviceID, RevokedAt: &revokedAt}}, deviceID: deviceID, hash: "h", wantErr: deviceservice.ErrRevoked, wantCalls: 1},
		"database outage":   {repository: &stubRepository{err: outage}, deviceID: deviceID, hash: "h", wantErr: outage, wantCalls: 1},
		"deadline":          {repository: &stubRepository{err: context.DeadlineExceeded}, deviceID: deviceID, hash: "h", wantErr: context.DeadlineExceeded, wantCalls: 1},
		"malformed id":      {repository: &stubRepository{}, deviceID: "not-a-uuid", hash: "h", wantErr: deviceservice.ErrRevoked},
		"empty credential":  {repository: &stubRepository{}, deviceID: deviceID, hash: " ", wantErr: deviceservice.ErrRevoked},
		"active credential": {repository: &stubRepository{device: deviceservice.Device{ID: deviceID}}, deviceID: deviceID, hash: "h", wantCalls: 1},
	} {
		t.Run(name, func(t *testing.T) {
			service := deviceservice.New(scenario.repository, testSecret, nil)
			device, err := service.Authenticate(context.Background(), scenario.deviceID, scenario.hash)
			if scenario.wantErr == nil {
				if err != nil || device.ID != deviceID {
					t.Fatalf("Authenticate = %+v, %v", device, err)
				}
			} else if !errors.Is(err, scenario.wantErr) {
				t.Fatalf("Authenticate error = %v, want %v", err, scenario.wantErr)
			}
			if scenario.wantErr == outage && errors.Is(err, deviceservice.ErrRevoked) {
				t.Fatal("infrastructure failure was reported as a revoked credential")
			}
			if scenario.repository.calls != scenario.wantCalls {
				t.Fatalf("repository calls = %d, want %d", scenario.repository.calls, scenario.wantCalls)
			}
		})
	}
}

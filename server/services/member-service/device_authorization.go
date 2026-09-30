package memberservice

import (
	"context"
	"strings"
)

type deviceAuthorizationKey struct{}

type deviceAuthorization struct {
	workspaceID string
	deviceID    string
	permissions map[Permission]struct{}
}

// WithDeviceAuthorization grants a narrowly scoped in-process identity after
// the gateway has authenticated a device credential. It is intentionally
// separate from user membership and should contain only the permissions
// required by the agent endpoint handling the request.
func WithDeviceAuthorization(ctx context.Context, workspaceID, deviceID string, permissions ...Permission) context.Context {
	grant := deviceAuthorization{
		workspaceID: strings.TrimSpace(workspaceID),
		deviceID:    strings.TrimSpace(deviceID),
		permissions: permissionSet(permissions...),
	}
	return context.WithValue(ctx, deviceAuthorizationKey{}, grant)
}

func DeviceAuthorized(ctx context.Context, workspaceID string, permission Permission) bool {
	grant, ok := ctx.Value(deviceAuthorizationKey{}).(deviceAuthorization)
	if !ok || grant.workspaceID == "" || grant.workspaceID != strings.TrimSpace(workspaceID) {
		return false
	}
	_, ok = grant.permissions[permission]
	return ok
}

func AuthorizedDeviceID(ctx context.Context) string {
	grant, _ := ctx.Value(deviceAuthorizationKey{}).(deviceAuthorization)
	return grant.deviceID
}

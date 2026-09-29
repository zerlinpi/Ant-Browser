package gatewayservice

import (
	"context"
	"errors"
	"net/http"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

// deviceProfile authenticates the device credential, verifies that the
// requested cloud profile is attached to an instance currently assigned to
// that device, and then reuses the normal profile handler with only read/sync
// permissions. It never grants profile management or another workspace role.
func (g *Gateway) deviceProfile(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, err := g.authenticateAgent(r)
		if err != nil {
			httpx.WriteError(w, r, httpx.Problem{
				Status: http.StatusUnauthorized, Code: "invalid_device_credential",
				Message: "Device credential is invalid or revoked",
			})
			return
		}
		profileID := r.PathValue("profileID")
		requestContext := postgres.WithTenantScope(r.Context(), postgres.TenantScope{
			WorkspaceID: device.WorkspaceID,
		})
		if err := g.instances.RequireDeviceProfileAccess(requestContext, device.WorkspaceID, device.ID, profileID); err != nil {
			if errors.Is(err, browserinstanceservice.ErrNotFound) {
				httpx.WriteError(w, r, httpx.Problem{Status: http.StatusNotFound, Code: "not_found", Message: "Cloud profile resource was not found"})
				return
			}
			g.writeServiceError(w, r, err)
			return
		}
		requestContext = memberservice.WithDeviceAuthorization(
			requestContext, device.WorkspaceID, device.ID,
			memberservice.PermissionProfileRead, memberservice.PermissionProfileSync,
		)
		requestContext = context.WithValue(requestContext, principalKey, principal{})
		r.SetPathValue("workspaceID", device.WorkspaceID)
		next(w, r.WithContext(requestContext))
	}
}

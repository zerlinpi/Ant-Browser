package gatewayservice

import (
	"errors"
	"net/http"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	fingerprintservice "github.com/zerlinpi/Ant-Browser/server/services/fingerprint-service"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

type agentInstanceRuntimeConfig struct {
	InstanceID  string                       `json:"instanceId"`
	Fingerprint *fingerprintservice.Template `json:"fingerprint,omitempty"`
}

// getAgentInstanceRuntimeConfig exposes the current version of the
// fingerprint template attached to an instance assigned to the authenticated
// device, with runtime arguments derived from that version at read time. The
// desktop verifies the arguments against the configuration and compares the
// content-addressed extension, so template edits apply on the next start. A
// device credential is never promoted to a workspace member and cross-device
// lookups intentionally return the same 404 as a missing instance.
func (g *Gateway) getAgentInstanceRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	device, err := g.authenticateAgent(r)
	if err != nil {
		httpx.WriteError(w, r, httpx.Problem{
			Status: http.StatusUnauthorized, Code: "invalid_device_credential",
			Message: "Device credential is invalid or revoked",
		})
		return
	}
	ctx := postgres.WithTenantScope(r.Context(), postgres.TenantScope{WorkspaceID: device.WorkspaceID})
	ctx = memberservice.WithDeviceAuthorization(
		ctx, device.WorkspaceID, device.ID,
		memberservice.PermissionInstanceRead, memberservice.PermissionFingerprintRead,
	)
	instance, err := g.instances.Get(ctx, "", device.WorkspaceID, r.PathValue("instanceID"))
	if err != nil || instance.AssignedDeviceID != device.ID || instance.DeletedAt != nil {
		if err == nil || errors.Is(err, browserinstanceservice.ErrNotFound) {
			httpx.WriteError(w, r, httpx.Problem{Status: http.StatusNotFound, Code: "not_found", Message: "Runtime configuration was not found"})
			return
		}
		g.writeServiceError(w, r, err)
		return
	}
	result := agentInstanceRuntimeConfig{InstanceID: instance.ID}
	if instance.FingerprintTemplateID != "" {
		template, templateErr := g.fingerprints.Get(ctx, "", device.WorkspaceID, instance.FingerprintTemplateID)
		if templateErr != nil {
			if errors.Is(templateErr, fingerprintservice.ErrNotFound) {
				httpx.WriteError(w, r, httpx.Problem{Status: http.StatusNotFound, Code: "not_found", Message: "Runtime configuration was not found"})
				return
			}
			g.writeServiceError(w, r, templateErr)
			return
		}
		result.Fingerprint = &template
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": result})
}

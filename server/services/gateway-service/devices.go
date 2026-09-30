package gatewayservice

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
	"github.com/zerlinpi/Ant-Browser/server/platform/realtime"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
)

// rotateDeviceCredential issues a new credential for a device registered by
// the caller. The previous credential stops authenticating when the rotation
// commits, and any live agent socket authenticated with it is closed.
func (g *Gateway) rotateDeviceCredential(w http.ResponseWriter, r *http.Request) {
	registration, err := g.devices.RotateCredential(r.Context(), mustPrincipal(r.Context()).UserID, r.PathValue("deviceID"))
	if err != nil {
		g.writeServiceError(w, r, err)
		return
	}
	g.disconnectAgentDevices(r.Context(), registration.Device.ID)
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": registration})
}

// disconnectAgentDevices ends live agent sessions of devices whose credentials
// were revoked or rotated. An established WebSocket is not re-authenticated,
// so it must be closed explicitly: the local socket is closed immediately,
// and the shared presence entry is removed so the node holding the socket
// closes it on its next presence refresh (within agentPingPeriod). Failures
// are logged; the credential change has already been committed.
func (g *Gateway) disconnectAgentDevices(ctx context.Context, deviceIDs ...string) {
	if len(deviceIDs) == 0 {
		return
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	for _, deviceID := range deviceIDs {
		g.agentHub.disconnect(deviceID)
		presence, err := g.realtime.Presence(cleanup, deviceID)
		if err != nil {
			if !errors.Is(err, realtime.ErrPresenceNotFound) {
				g.logger.WarnContext(ctx, "agent_presence_lookup_failed", "device_id", deviceID, "error", err)
			}
			continue
		}
		if err := g.realtime.RemovePresence(cleanup, deviceID, presence.ConnectionID); err != nil {
			g.logger.WarnContext(ctx, "agent_presence_remove_failed", "device_id", deviceID, "error", err)
		}
	}
}

// disconnect closes this node's live socket for deviceID, if any. The socket
// handler's own cleanup unregisters the client and removes its presence.
func (h *agentHub) disconnect(deviceID string) bool {
	h.mu.RLock()
	client := h.clients[deviceID]
	h.mu.RUnlock()
	if client == nil {
		return false
	}
	client.close()
	return true
}

type agentAuthenticationKey struct{}

type agentAuthentication struct {
	device deviceservice.Device
	err    error
}

// withAgentAuthentication authenticates the device credential of an agent
// route once, before the handler runs. Infrastructure failures (for example a
// database outage) are answered with 503 dependency_unavailable instead of
// being reported to agents as an invalid credential, which would make them
// discard a valid one. Credential rejections are left to the handler, which
// receives the cached result from authenticateAgent, so each route keeps its
// own 401 and protocol checks.
func (g *Gateway) withAgentAuthentication(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		device, err := g.authenticateAgent(r)
		if err != nil && !deviceCredentialRejected(err) {
			g.logger.ErrorContext(r.Context(), "agent_authentication_unavailable", "request_id", httpx.RequestID(r.Context()), "error", err)
			httpx.WriteError(w, r, httpx.Problem{
				Status: http.StatusServiceUnavailable, Code: "dependency_unavailable",
				Message: "Device authentication is temporarily unavailable",
			})
			return
		}
		ctx := context.WithValue(r.Context(), agentAuthenticationKey{}, agentAuthentication{device: device, err: err})
		next(w, r.WithContext(ctx))
	}
}

// cachedAgentAuthentication returns the result recorded by
// withAgentAuthentication for this request, if any.
func cachedAgentAuthentication(r *http.Request) (agentAuthentication, bool) {
	result, ok := r.Context().Value(agentAuthenticationKey{}).(agentAuthentication)
	return result, ok
}

func deviceCredentialRejected(err error) bool {
	return errors.Is(err, deviceservice.ErrRevoked) || errors.Is(err, deviceservice.ErrNotFound)
}

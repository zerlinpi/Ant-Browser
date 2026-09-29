package gatewayservice

import (
	"net/http"

	"github.com/zerlinpi/Ant-Browser/server/platform/httpx"
)

// TrustedProxies lists the reverse-proxy addresses (CIDR ranges or single
// IPs) whose X-Forwarded-For / X-Real-IP headers are honored when resolving a
// client IP. Pass it to NewWithInfrastructure as an option. Without it, or
// when any entry is invalid, no proxy is trusted and the TCP peer address is
// the client IP.
type TrustedProxies []string

// clientIP returns the resolved client IP used for session metadata, login
// security events, and rate limiting.
func (g *Gateway) clientIP(r *http.Request) string {
	return g.clientIPs.ClientIP(r)
}

func (g *Gateway) newClientIPResolver(trusted TrustedProxies) *httpx.ClientIPResolver {
	prefixes, err := httpx.ParseTrustedProxies(trusted)
	if err != nil {
		// Fail closed: forwarding headers are only honored for a fully valid
		// configuration (config validation already rejects this at startup).
		if g.logger != nil {
			g.logger.Error("trusted_proxies_rejected", "error", err)
		}
		return httpx.NewClientIPResolver(nil)
	}
	return httpx.NewClientIPResolver(prefixes)
}

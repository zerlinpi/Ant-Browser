package httpx

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// maxForwardedHops bounds how many X-Forwarded-For entries are examined, so a
// long (possibly attacker-padded) header cannot make resolution expensive.
const maxForwardedHops = 32

var errInvalidTrustedProxy = errors.New("must be a CIDR range or a single IP address")

// ClientIPResolver derives the client address of a request. Forwarding
// headers are honored only when the TCP peer is a configured trusted proxy;
// from any other peer they are ignored, because every client can set them.
// A nil resolver trusts no proxy.
type ClientIPResolver struct {
	trusted []netip.Prefix
}

// ParseTrustedProxies parses CIDR ranges (for example 10.0.0.0/8) and single
// IP addresses (treated as /32 or /128). Ranges that cover every address are
// rejected: they would let any client choose its own address.
func ParseTrustedProxies(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		prefix, err := parseTrustedProxy(value)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", value, err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func parseTrustedProxy(value string) (netip.Prefix, error) {
	var prefix netip.Prefix
	if strings.Contains(value, "/") {
		parsed, err := netip.ParsePrefix(value)
		if err != nil {
			return netip.Prefix{}, errInvalidTrustedProxy
		}
		prefix = parsed
		// Addresses are unmapped before matching, so express IPv4-mapped
		// IPv6 ranges as the equivalent IPv4 range.
		if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
	} else {
		addr, err := netip.ParseAddr(value)
		if err != nil || addr.Zone() != "" {
			return netip.Prefix{}, errInvalidTrustedProxy
		}
		addr = addr.Unmap()
		prefix = netip.PrefixFrom(addr, addr.BitLen())
	}
	if prefix.Bits() == 0 {
		return netip.Prefix{}, errors.New("must not trust every address")
	}
	return prefix.Masked(), nil
}

// NewClientIPResolver returns a resolver trusting the given proxy ranges.
func NewClientIPResolver(trusted []netip.Prefix) *ClientIPResolver {
	return &ClientIPResolver{trusted: append([]netip.Prefix(nil), trusted...)}
}

// ClientIP returns the client address of r in canonical form, or "" when the
// peer address cannot be parsed (for example on a non-TCP transport).
//
// When the TCP peer (RemoteAddr) is a trusted proxy, X-Forwarded-For is walked
// right to left, skipping trusted proxies, and the first untrusted address is
// the client. Entries left of it were supplied by the client and are ignored.
// If the header yields no untrusted address (absent, malformed, or only
// trusted hops), X-Real-IP is used when valid, and otherwise the peer itself.
func (c *ClientIPResolver) ClientIP(r *http.Request) string {
	peer, ok := parseAddress(r.RemoteAddr)
	if !ok {
		return ""
	}
	if !c.isTrusted(peer) {
		return peer.String()
	}
	if client, ok := c.forwardedClient(r.Header.Values("X-Forwarded-For")); ok {
		return client.String()
	}
	if realIP, ok := parseAddress(r.Header.Get("X-Real-IP")); ok {
		return realIP.String()
	}
	return peer.String()
}

func (c *ClientIPResolver) isTrusted(addr netip.Addr) bool {
	if c == nil {
		return false
	}
	for _, prefix := range c.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// forwardedClient scans X-Forwarded-For entries from the right (the entry
// appended by the nearest proxy) without splitting the whole header. Multiple
// header lines are treated as one comma-separated list in received order.
func (c *ClientIPResolver) forwardedClient(values []string) (netip.Addr, bool) {
	examined := 0
	for index := len(values) - 1; index >= 0; index-- {
		remaining := values[index]
		for {
			entry := remaining
			separator := strings.LastIndexByte(remaining, ',')
			if separator >= 0 {
				entry = remaining[separator+1:]
				remaining = remaining[:separator]
			}
			if entry = strings.TrimSpace(entry); entry != "" {
				examined++
				if examined > maxForwardedHops {
					return netip.Addr{}, false
				}
				addr, ok := parseAddress(entry)
				if !ok {
					// A malformed hop means the chain cannot be trusted past
					// this point.
					return netip.Addr{}, false
				}
				if !c.isTrusted(addr) {
					return addr, true
				}
			}
			if separator < 0 {
				break
			}
		}
	}
	return netip.Addr{}, false
}

// parseAddress accepts an IP address with an optional port, in the forms
// found in RemoteAddr and forwarding headers ("192.0.2.1", "192.0.2.1:443",
// "2001:db8::1", "[2001:db8::1]:443", "[2001:db8::1]"). Zones are dropped and
// IPv4-mapped IPv6 addresses are unmapped so results are storable as inet
// values and comparable with IPv4 ranges.
func parseAddress(value string) (netip.Addr, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Addr{}, false
	}
	if addr, err := netip.ParseAddr(value); err == nil {
		return addr.WithZone("").Unmap(), true
	}
	if addrPort, err := netip.ParseAddrPort(value); err == nil {
		return addrPort.Addr().WithZone("").Unmap(), true
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		if addr, err := netip.ParseAddr(value[1 : len(value)-1]); err == nil {
			return addr.WithZone("").Unmap(), true
		}
	}
	return netip.Addr{}, false
}

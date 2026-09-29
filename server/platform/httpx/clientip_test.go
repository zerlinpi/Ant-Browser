package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mustResolver(t *testing.T, trusted ...string) *ClientIPResolver {
	t.Helper()
	prefixes, err := ParseTrustedProxies(trusted)
	if err != nil {
		t.Fatal(err)
	}
	return NewClientIPResolver(prefixes)
}

func forwardedRequest(remote string, forwarded []string, realIP string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	request.RemoteAddr = remote
	for _, value := range forwarded {
		request.Header.Add("X-Forwarded-For", value)
	}
	if realIP != "" {
		request.Header.Set("X-Real-IP", realIP)
	}
	return request
}

func TestClientIPHonorsForwardingOnlyFromTrustedProxies(t *testing.T) {
	resolver := mustResolver(t, "10.0.0.0/8", "192.0.2.10", "2001:db8:aaaa::/48")
	for name, scenario := range map[string]struct {
		remote     string
		forwarded  []string
		realIP     string
		wantClient string
	}{
		"direct client": {
			remote: "203.0.113.5:4711", wantClient: "203.0.113.5",
		},
		"spoofed headers from untrusted peer are ignored": {
			remote: "203.0.113.5:4711", forwarded: []string{"198.51.100.1"}, realIP: "198.51.100.2", wantClient: "203.0.113.5",
		},
		"single trusted proxy": {
			remote: "10.0.0.2:80", forwarded: []string{"198.51.100.7"}, wantClient: "198.51.100.7",
		},
		"client-supplied entries left of the client are ignored": {
			remote: "10.0.0.2:80", forwarded: []string{"1.1.1.1, 198.51.100.7"}, wantClient: "198.51.100.7",
		},
		"trusted hops are skipped right to left": {
			remote: "10.0.0.2:80", forwarded: []string{"6.6.6.6, 198.51.100.7, 10.1.2.3, 192.0.2.10"}, wantClient: "198.51.100.7",
		},
		"multiple header lines form one list": {
			remote: "10.0.0.2:80", forwarded: []string{"6.6.6.6, 198.51.100.7", "10.1.2.3"}, wantClient: "198.51.100.7",
		},
		"empty list elements are skipped": {
			remote: "10.0.0.2:80", forwarded: []string{"198.51.100.7, , 10.1.2.3,"}, wantClient: "198.51.100.7",
		},
		"single-address trusted proxy": {
			remote: "192.0.2.10:443", forwarded: []string{"198.51.100.8"}, wantClient: "198.51.100.8",
		},
		"forwarded entry with port": {
			remote: "10.0.0.2:80", forwarded: []string{"198.51.100.9:5555"}, wantClient: "198.51.100.9",
		},
		"ipv6 client behind ipv6 proxy": {
			remote: "[2001:db8:aaaa::1]:443", forwarded: []string{"[2001:db8:cafe::7]:1234"}, wantClient: "2001:db8:cafe::7",
		},
		"ipv4-mapped peer matches ipv4 range": {
			remote: "[::ffff:10.0.0.2]:80", forwarded: []string{"198.51.100.10"}, wantClient: "198.51.100.10",
		},
		"only trusted hops falls back to X-Real-IP": {
			remote: "10.0.0.2:80", forwarded: []string{"10.9.9.9"}, realIP: "198.51.100.11", wantClient: "198.51.100.11",
		},
		"missing header falls back to X-Real-IP": {
			remote: "10.0.0.2:80", realIP: "198.51.100.12", wantClient: "198.51.100.12",
		},
		"malformed hop falls back to X-Real-IP": {
			remote: "10.0.0.2:80", forwarded: []string{"198.51.100.1, not-an-ip"}, realIP: "198.51.100.13", wantClient: "198.51.100.13",
		},
		"invalid X-Real-IP falls back to the peer": {
			remote: "10.0.0.2:80", forwarded: []string{"unknown"}, realIP: "also-bad", wantClient: "10.0.0.2",
		},
		"X-Real-IP from untrusted peer is ignored": {
			remote: "203.0.113.5:4711", realIP: "198.51.100.14", wantClient: "203.0.113.5",
		},
		"unparsable peer": {
			remote: "pipe", forwarded: []string{"198.51.100.1"}, wantClient: "",
		},
		"peer without port": {
			remote: "203.0.113.6", wantClient: "203.0.113.6",
		},
		"zoned peer": {
			remote: "[fe80::1%eth0]:80", wantClient: "fe80::1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := forwardedRequest(scenario.remote, scenario.forwarded, scenario.realIP)
			if got := resolver.ClientIP(request); got != scenario.wantClient {
				t.Fatalf("ClientIP = %q, want %q", got, scenario.wantClient)
			}
		})
	}
}

func TestClientIPWithoutTrustedProxiesUsesPeer(t *testing.T) {
	request := forwardedRequest("10.0.0.2:80", []string{"198.51.100.1"}, "198.51.100.2")
	for name, resolver := range map[string]*ClientIPResolver{
		"empty": NewClientIPResolver(nil),
		"nil":   nil,
	} {
		if got := resolver.ClientIP(request); got != "10.0.0.2" {
			t.Fatalf("%s resolver: ClientIP = %q, want the peer", name, got)
		}
	}
}

func TestClientIPBoundsForwardedHops(t *testing.T) {
	resolver := mustResolver(t, "10.0.0.0/8")
	hops := []string{"198.51.100.1"}
	for len(hops) <= maxForwardedHops {
		hops = append(hops, "10.0.0.9")
	}
	request := forwardedRequest("10.0.0.2:80", []string{strings.Join(hops, ",")}, "")
	if got := resolver.ClientIP(request); got != "10.0.0.2" {
		t.Fatalf("over-long trusted chain resolved to %q, want the peer", got)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	prefixes, err := ParseTrustedProxies([]string{" 10.0.0.0/8 ", "", "192.0.2.10", "2001:db8::/32", "::ffff:172.16.0.0/108", "10.1.2.3/8"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "192.0.2.10/32", "2001:db8::/32", "172.16.0.0/12", "10.0.0.0/8"}
	if len(prefixes) != len(want) {
		t.Fatalf("prefixes = %v", prefixes)
	}
	for index, prefix := range prefixes {
		if prefix.String() != want[index] {
			t.Fatalf("prefix %d = %s, want %s", index, prefix, want[index])
		}
	}
	for _, invalid := range []string{"10.0.0.0/33", "proxy.internal", "fe80::1%eth0", "0.0.0.0/0", "::/0", "::ffff:0.0.0.0/96", "10.0.0.1:80"} {
		if _, err := ParseTrustedProxies([]string{invalid}); err == nil {
			t.Fatalf("%q was accepted", invalid)
		}
	}
}

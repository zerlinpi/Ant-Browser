package gatewayservice

import (
	"net/http/httptest"
	"testing"
)

func TestGatewayOriginPolicyAllowsConfiguredDesktopAndSameOrigin(t *testing.T) {
	gateway := &Gateway{allowedOrigins: []string{"http://wails.localhost", "https://app.example.test"}}
	for name, values := range map[string]struct {
		origin string
		host   string
		want   bool
	}{
		"non browser agent": {origin: "", host: "api.example.test", want: true},
		"same origin":       {origin: "https://api.example.test", host: "api.example.test", want: true},
		"desktop":           {origin: "http://wails.localhost", host: "api.example.test", want: true},
		"hosted UI":         {origin: "https://app.example.test", host: "api.example.test", want: true},
		"untrusted":         {origin: "https://evil.example.test", host: "api.example.test", want: false},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "https://"+values.host+"/api/v1/notifications/ws", nil)
			request.Host = values.host
			if values.origin != "" {
				request.Header.Set("Origin", values.origin)
			}
			if got := gateway.originAllowed(request); got != values.want {
				t.Fatalf("originAllowed(%q, %q) = %v, want %v", values.origin, values.host, got, values.want)
			}
		})
	}
}

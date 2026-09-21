package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExitProbeResponseValidation(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"plain", "203.0.113.8", 200, true}, {"json", `{"ip":"2001:db8::1"}`, 200, true},
		{"not-ip", `{"ip":"private-secret"}`, 200, false}, {"too-large", strings.Repeat("a", 4097), 200, false},
		{"redirect", "203.0.113.8", 302, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			result, err := probeExitHTTP(context.Background(), server.Client(), server.URL)
			if (err == nil) != test.valid {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if err != nil && strings.Contains(err.Error(), "private-secret") {
				t.Fatal("response leaked into error")
			}
		})
	}
}
func TestExitProbeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("cancelled probe reached server") }))
	defer server.Close()
	if _, err := probeExitHTTP(ctx, server.Client(), server.URL); err == nil {
		t.Fatal("cancelled probe succeeded")
	}
}

package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSAllowsConfiguredOriginAndPreflight(t *testing.T) {
	called := false
	handler := CORS([]string{"http://wails.localhost", "https://app.example.test"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	request := httptest.NewRequest(http.MethodOptions, "/api/v1/workspaces", nil)
	request.Header.Set("Origin", "http://wails.localhost")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "authorization, content-type, idempotency-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || called {
		t.Fatalf("preflight status/call = %d/%v", response.Code, called)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "http://wails.localhost" ||
		!strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "Idempotency-Key") {
		t.Fatalf("missing scoped CORS response headers: %v", response.Header())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	request.Header.Set("Origin", "https://app.example.test")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !called || response.Header().Get("Access-Control-Allow-Origin") != "https://app.example.test" {
		t.Fatalf("allowed request status/call/headers = %d/%v/%v", response.Code, called, response.Header())
	}
}

func TestCORSFailsClosedForUnknownOriginAndHeader(t *testing.T) {
	handler := CORS([]string{"https://app.example.test"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for name, values := range map[string][2]string{
		"unknown origin": {"https://evil.example.test", "content-type"},
		"unknown header": {"https://app.example.test", "x-forwarded-for"},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/register", nil)
			request.Header.Set("Origin", values[0])
			request.Header.Set("Access-Control-Request-Method", http.MethodPost)
			request.Header.Set("Access-Control-Request-Headers", values[1])
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || response.Header().Get("Access-Control-Allow-Origin") == "https://evil.example.test" {
				t.Fatalf("unsafe preflight accepted: status=%d headers=%v", response.Code, response.Header())
			}
		})
	}
}

func TestCORSExposesRetryAfterForRateLimitedClients(t *testing.T) {
	handler := CORS([]string{"https://app.example.test"}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	request.Header.Set("Origin", "https://app.example.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if exposed := response.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(exposed, "Retry-After") || !strings.Contains(exposed, "X-Request-ID") {
		t.Fatalf("exposed headers = %q", exposed)
	}
}

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeProbeDirectRoundTrip(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "runtime.yaml")
	if err := os.WriteFile(cfg, []byte("browser: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"ip":"203.0.113.42"}`)) }))
	defer server.Close()
	result, err := run(context.Background(), strings.NewReader(`{"proxyConfig":"direct://","connectorType":"xray","kernel":"direct"}`), cfg, root, server.URL)
	if err != nil || result.Status != "succeeded" || result.IP != "203.0.113.42" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRuntimeProbeRejectsRouteMismatch(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "runtime.yaml")
	if err := os.WriteFile(cfg, []byte("browser: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := run(context.Background(), strings.NewReader(`{"proxyConfig":"direct://","connectorType":"mihomo","kernel":"xray"}`), cfg, root, "https://example.test")
	if err == nil {
		t.Fatal("mismatched stack accepted")
	}
}

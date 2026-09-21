package cdp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestConnectDiscoversLoopbackWebsocket(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/test"})
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()
	client := NewClient(server.URL)
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	if !client.Connected() {
		t.Fatal("client did not report connected")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEndpointMustBeLoopback(t *testing.T) {
	if _, err := validateEndpoint("http://example.com:80", false); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("non-loopback endpoint accepted: %v", err)
	}
}

func TestConnectContextCancelsDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := NewClient(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := client.ConnectContext(ctx); err == nil {
		t.Fatal("discovery unexpectedly completed")
	}
}

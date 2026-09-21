package backend

import (
	"ant-chrome/backend/internal/browser"
	"ant-chrome/backend/internal/config"
	"strings"
	"testing"
	"time"
)

func newConnectorTestApp(connector string) *App {
	cfg := config.DefaultConfig()
	cfg.Browser.DefaultConnectorType = connector
	app := NewApp(".")
	app.config = cfg
	app.browserMgr = browser.NewManager(cfg, app.appRoot)
	return app
}

func TestWarmupUsesConfiguredConnectorStack(t *testing.T) {
	app := newConnectorTestApp(config.BrowserConnectorMihomo)
	result := app.warmupProxyBridge("p1", "vless://00000000-0000-0000-0000-000000000000@example.com:443", nil)
	if result.Engine != "mihomo" {
		t.Fatalf("warmup engine = %q, want mihomo; result=%+v", result.Engine, result)
	}

	app = newConnectorTestApp(config.BrowserConnectorXray)
	result = app.warmupProxyBridge("p1", "hysteria2://pass@example.com:443", nil)
	if result.Engine != "sing-box" {
		t.Fatalf("xray-stack warmup engine = %q, want sing-box; result=%+v", result.Engine, result)
	}
}

func TestProxyCoreDownloadDoesNotCrossConnectorStacks(t *testing.T) {
	app := newConnectorTestApp(config.BrowserConnectorMihomo)
	_, label, err := app.buildProxyCoreDownloadHTTPClient(
		time.Second,
		"vless://00000000-0000-0000-0000-000000000000@example.com:443",
	)
	if err == nil || !strings.Contains(err.Error(), "installed mihomo connector") {
		t.Fatalf("expected explicit mihomo bootstrap error, label=%q err=%v", label, err)
	}

	client, label, err := app.buildProxyCoreDownloadHTTPClient(time.Second, "direct://")
	if err != nil || client == nil || label == "" {
		t.Fatalf("direct bootstrap client = %#v label=%q err=%v", client, label, err)
	}
}

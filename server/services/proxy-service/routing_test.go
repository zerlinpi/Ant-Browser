package proxyservice

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveProxyKernelForConnector(t *testing.T) {
	tests := []struct {
		connector ConnectorType
		protocol  string
		auth      bool
		want      Kernel
	}{
		{ConnectorXray, "vmess", false, KernelXray},
		{ConnectorXray, "hysteria2", false, KernelSingBox},
		{ConnectorXray, "http", false, KernelDirect},
		{ConnectorXray, "http", true, KernelXray},
		{ConnectorMihomo, "vless", false, KernelMihomo},
		{ConnectorMihomo, "hysteria2", false, KernelMihomo},
		{ConnectorMihomo, "socks5", true, KernelMihomo},
	}
	for _, test := range tests {
		got, err := ResolveProxyKernelForConnector(test.connector, test.protocol, test.auth)
		if err != nil || got != test.want {
			t.Errorf("route(%q,%q,%t) = %q, %v; want %q", test.connector, test.protocol, test.auth, got, err, test.want)
		}
	}
}

func TestResolveProxyKernelDoesNotFallback(t *testing.T) {
	if _, err := ResolveProxyKernelForConnector(ConnectorXray, "clash-meta-only", false); err == nil {
		t.Fatal("unsupported xray protocol should be a hard routing error")
	}
	if _, err := ResolveProxyKernelForConnector(ConnectorType("unknown"), "vmess", false); err == nil {
		t.Fatal("unknown connector should be a hard routing error")
	}
}

func TestSecretCannotBeMarshalled(t *testing.T) {
	encoded, err := json.Marshal(Secret{Password: "password", Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "password") || strings.Contains(string(encoded), "token") {
		t.Fatalf("secret payload leaked through JSON: %s", encoded)
	}
}

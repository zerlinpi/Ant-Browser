package runtimeprobe

import (
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	"net/url"
	"testing"
)

func TestNativeCredentialsAreURLEncoded(t *testing.T) {
	source, err := proxySource(proxyservice.Proxy{Protocol: "socks5", Host: "2001:db8::1", Port: 1080, Username: "user@tenant", HasCredentials: true}, proxyservice.Secret{Password: "p@ss:/?#"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := parsed.User.Password()
	if parsed.User.Username() != "user@tenant" || password != "p@ss:/?#" || parsed.Host != "[2001:db8::1]:1080" {
		t.Fatal("credential URI was not preserved")
	}
}
func TestAdvancedConfigurationPreserved(t *testing.T) {
	source := "vless://node@example.test:443?security=reality&pbk=key&type=grpc"
	actual, err := proxySource(proxyservice.Proxy{Protocol: "vless"}, proxyservice.Secret{Token: source})
	if err != nil || actual != source {
		t.Fatal("advanced configuration changed")
	}
	if _, err := proxySource(proxyservice.Proxy{Protocol: "vless"}, proxyservice.Secret{}); err == nil {
		t.Fatal("missing advanced configuration accepted")
	}
}
func TestOutputBound(t *testing.T) {
	output := &boundedOutput{limit: 4}
	if _, err := output.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("5")); err == nil {
		t.Fatal("output limit was not enforced")
	}
	if output.data.Len() != 4 {
		t.Fatal("oversized output retained")
	}
}

package smtpemail

import (
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestNewValidatesTLSConfiguration(t *testing.T) {
	if _, err := New(Config{Address: "smtp.example.test:587", From: "Alerts <alerts@example.test>", TLSMode: TLSStartTLS}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Address: "missing-port", From: "alerts@example.test"}); err == nil {
		t.Fatal("expected invalid address error")
	}
	if _, err := New(Config{Address: "smtp.example.test:587", From: "alerts@example.test", TLSMode: "optional"}); err == nil {
		t.Fatal("expected invalid TLS mode error")
	}
}

func TestBuildMessageEncodesUnicodeAndRejectsHeaderInjection(t *testing.T) {
	from, _ := mail.ParseAddress("Ant Browser <alerts@example.test>")
	to, _ := mail.ParseAddress("owner@example.test")
	message, err := buildMessage(from, to, "账号风险提醒", "代理不可用，请检查。", time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	text := string(message)
	if !strings.Contains(text, "Subject: =?utf-8?q?") || !strings.Contains(text, "Content-Transfer-Encoding: quoted-printable") {
		t.Fatalf("message was not MIME encoded:\n%s", text)
	}
	if _, err := buildMessage(from, to, "safe\r\nBcc: attacker@example.test", "body", time.Now()); err == nil {
		t.Fatal("expected header injection rejection")
	}
}

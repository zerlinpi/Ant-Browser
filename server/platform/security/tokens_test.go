package security

import (
	"errors"
	"testing"
	"time"
)

func TestNotificationTicketIsPurposeBound(t *testing.T) {
	tokens := NewTokens("issuer", "01234567890123456789012345678901", time.Minute)
	ticket, expiresAt, err := tokens.IssueNotificationTicket("user", "session", "workspace", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := tokens.ParseNotificationTicket(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != "user" || claims.SessionID != "session" || claims.WorkspaceID != "workspace" || claims.ID == "" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if expiresAt.IsZero() {
		t.Fatal("ticket expiration was not returned")
	}
	if _, err := tokens.ParseAccess(ticket); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("notification ticket parsed as access token: %v", err)
	}
	access, _, err := tokens.IssueAccess("user", "session")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.ParseNotificationTicket(access); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("access token parsed as notification ticket: %v", err)
	}
}

func TestNotificationTicketRejectsUnsafeLifetime(t *testing.T) {
	tokens := NewTokens("issuer", "01234567890123456789012345678901", time.Minute)
	if _, _, err := tokens.IssueNotificationTicket("user", "session", "workspace", time.Hour); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("long-lived ticket error=%v", err)
	}
}

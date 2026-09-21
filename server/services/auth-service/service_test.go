package authservice_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/memory"
	"github.com/zerlinpi/Ant-Browser/server/platform/security"
	authservice "github.com/zerlinpi/Ant-Browser/server/services/auth-service"
)

func TestRefreshRotationRejectsReplayAndLogoutRevokesAccess(t *testing.T) {
	t.Parallel()
	store := memory.New()
	tokens := security.NewTokens("test-issuer", "01234567890123456789012345678901", 5*time.Minute)
	service := authservice.New(store, security.NewPasswords(), tokens, security.NewOpaqueToken, 24*time.Hour)

	registered, err := service.Register(context.Background(), authservice.RegisterInput{
		Email: "owner@example.com", Password: "SecurePassword123", DisplayName: "Owner",
	}, authservice.SessionMetadata{IPAddress: "127.0.0.1", UserAgent: "test"})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	rotated, err := service.Refresh(context.Background(), registered.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if rotated.RefreshToken == registered.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if _, err := service.Refresh(context.Background(), registered.RefreshToken); !errors.Is(err, authservice.ErrInvalidCredentials) {
		t.Fatalf("replayed refresh token returned %v", err)
	}
	claims, err := tokens.ParseAccess(rotated.AccessToken)
	if err != nil {
		t.Fatalf("ParseAccess returned error: %v", err)
	}
	if err := service.Logout(context.Background(), claims.UserID, claims.SessionID, "test_logout"); err != nil {
		t.Fatalf("Logout returned error: %v", err)
	}
	if err := service.ValidateSession(context.Background(), claims.UserID, claims.SessionID); !errors.Is(err, authservice.ErrSessionRevoked) {
		t.Fatalf("revoked session validation returned %v", err)
	}
}

func TestRegisterRejectsWeakPassword(t *testing.T) {
	t.Parallel()
	store := memory.New()
	tokens := security.NewTokens("test-issuer", "01234567890123456789012345678901", 5*time.Minute)
	service := authservice.New(store, security.NewPasswords(), tokens, security.NewOpaqueToken, 24*time.Hour)
	_, err := service.Register(context.Background(), authservice.RegisterInput{
		Email: "owner@example.com", Password: "short", DisplayName: "Owner",
	}, authservice.SessionMetadata{})
	if !errors.Is(err, security.ErrWeakPassword) {
		t.Fatalf("Register returned %v, want ErrWeakPassword", err)
	}
}

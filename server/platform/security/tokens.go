package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("invalid token")

type AccessClaims struct {
	UserID    string `json:"uid"`
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

type NotificationTicketClaims struct {
	UserID      string `json:"uid"`
	SessionID   string `json:"sid"`
	WorkspaceID string `json:"wid"`
	jwt.RegisteredClaims
}

type Tokens struct {
	issuer    string
	secret    []byte
	accessTTL time.Duration
	now       func() time.Time
}

func NewTokens(issuer, secret string, accessTTL time.Duration) Tokens {
	return Tokens{
		issuer:    issuer,
		secret:    []byte(secret),
		accessTTL: accessTTL,
		now:       time.Now,
	}
}

func (t Tokens) IssueAccess(userID, sessionID string) (string, time.Time, error) {
	now := t.now().UTC()
	expiresAt := now.Add(t.accessTTL)
	claims := AccessClaims{
		UserID:    userID,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    t.issuer,
			Subject:   userID,
			Audience:  jwt.ClaimStrings{"ant-browser"},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(t.secret)
	return signed, expiresAt, err
}

func (t Tokens) ParseAccess(raw string) (AccessClaims, error) {
	claims := AccessClaims{}
	parsed, err := jwt.ParseWithClaims(
		raw,
		&claims,
		func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, ErrInvalidToken
			}
			return t.secret, nil
		},
		jwt.WithAudience("ant-browser"),
		jwt.WithIssuer(t.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil || !parsed.Valid || claims.UserID == "" || claims.SessionID == "" {
		return AccessClaims{}, ErrInvalidToken
	}
	return claims, nil
}

// IssueNotificationTicket creates a short-lived ticket suitable for a browser
// WebSocket subprotocol. Access tokens never need to be placed in a URL.
func (t Tokens) IssueNotificationTicket(userID, sessionID, workspaceID string, ttl time.Duration) (string, time.Time, error) {
	if userID == "" || sessionID == "" || workspaceID == "" || ttl < 10*time.Second || ttl > 5*time.Minute {
		return "", time.Time{}, ErrInvalidToken
	}
	now := t.now().UTC()
	expiresAt := now.Add(ttl)
	jti, _, err := NewOpaqueToken()
	if err != nil {
		return "", time.Time{}, err
	}
	claims := NotificationTicketClaims{
		UserID: userID, SessionID: sessionID, WorkspaceID: workspaceID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: t.issuer, Subject: userID,
			Audience:  jwt.ClaimStrings{"ant-browser-notifications"},
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(t.secret)
	return signed, expiresAt, err
}

func (t Tokens) ParseNotificationTicket(raw string) (NotificationTicketClaims, error) {
	claims := NotificationTicketClaims{}
	parsed, err := jwt.ParseWithClaims(
		raw,
		&claims,
		func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, ErrInvalidToken
			}
			return t.secret, nil
		},
		jwt.WithAudience("ant-browser-notifications"),
		jwt.WithIssuer(t.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil || !parsed.Valid || claims.UserID == "" || claims.SessionID == "" || claims.WorkspaceID == "" || claims.ID == "" {
		return NotificationTicketClaims{}, ErrInvalidToken
	}
	return claims, nil
}

func NewOpaqueToken() (raw, hash string, err error) {
	buffer := make([]byte, 32)
	if _, err = rand.Read(buffer); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buffer)
	hash = HashOpaqueToken(raw)
	return raw, hash, nil
}

func HashOpaqueToken(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

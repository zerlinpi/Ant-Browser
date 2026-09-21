package authservice

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrEmailExists        = errors.New("email already exists")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrSessionRevoked     = errors.New("session revoked")
	ErrUserDisabled       = errors.New("user disabled")
	ErrNotFound           = errors.New("not found")
)

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	DisplayName  string    `json:"displayName"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Session struct {
	ID           string     `json:"id"`
	UserID       string     `json:"userId"`
	DeviceID     string     `json:"deviceId,omitempty"`
	UserAgent    string     `json:"userAgent,omitempty"`
	IPAddress    string     `json:"ipAddress,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastSeenAt   time.Time  `json:"lastSeenAt"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
	RevokeReason string     `json:"revokeReason,omitempty"`
}

type RefreshToken struct {
	ID           string     `json:"id"`
	SessionID    string     `json:"sessionId"`
	TokenHash    string     `json:"-"`
	ParentID     string     `json:"parentId,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	ConsumedAt   *time.Time `json:"consumedAt,omitempty"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
	ReplacedByID string     `json:"replacedById,omitempty"`
}

type Repository interface {
	CreateUser(context.Context, User) error
	FindUserByEmail(context.Context, string) (User, error)
	FindUserByID(context.Context, string) (User, error)
	SaveSession(context.Context, Session, RefreshToken) error
	FindRefreshToken(context.Context, string) (RefreshToken, Session, User, error)
	RotateRefreshToken(context.Context, string, RefreshToken) (Session, User, error)
	RevokeSession(context.Context, string, string, string) error
	SessionActive(context.Context, string, string) (bool, error)
}

type PasswordManager interface {
	Hash(string) (string, error)
	Compare(string, string) error
}

type TokenManager interface {
	IssueAccess(string, string) (string, time.Time, error)
}

type OpaqueTokenFactory func() (raw, hash string, err error)

type Service struct {
	repository Repository
	passwords  PasswordManager
	tokens     TokenManager
	newOpaque  OpaqueTokenFactory
	refreshTTL time.Duration
	now        func() time.Time
}

type SessionMetadata struct {
	DeviceID  string
	UserAgent string
	IPAddress string
}

type RegisterInput struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
}

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	DeviceID string `json:"deviceId,omitempty"`
}

type TokenPair struct {
	AccessToken      string    `json:"accessToken"`
	AccessExpiresAt  time.Time `json:"accessExpiresAt"`
	RefreshToken     string    `json:"refreshToken"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
	SessionID        string    `json:"sessionId"`
	User             User      `json:"user"`
}

func New(repository Repository, passwords PasswordManager, tokens TokenManager, newOpaque OpaqueTokenFactory, refreshTTL time.Duration) *Service {
	return &Service{
		repository: repository,
		passwords:  passwords,
		tokens:     tokens,
		newOpaque:  newOpaque,
		refreshTTL: refreshTTL,
		now:        time.Now,
	}
}

func (s *Service) Register(ctx context.Context, input RegisterInput, metadata SessionMetadata) (TokenPair, error) {
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return TokenPair{}, err
	}
	passwordHash, err := s.passwords.Hash(input.Password)
	if err != nil {
		return TokenPair{}, err
	}
	now := s.now().UTC()
	user := User{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: passwordHash,
		DisplayName:  strings.TrimSpace(input.DisplayName),
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if user.DisplayName == "" {
		user.DisplayName = strings.Split(email, "@")[0]
	}
	if len([]rune(user.DisplayName)) > 100 {
		return TokenPair{}, errors.New("display name is too long")
	}
	if err := s.repository.CreateUser(ctx, user); err != nil {
		return TokenPair{}, err
	}
	return s.issueSession(ctx, user, metadata)
}

func (s *Service) Login(ctx context.Context, input LoginInput, metadata SessionMetadata) (TokenPair, error) {
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return TokenPair{}, ErrInvalidCredentials
	}
	user, err := s.repository.FindUserByEmail(ctx, email)
	if err != nil || s.passwords.Compare(user.PasswordHash, input.Password) != nil {
		return TokenPair{}, ErrInvalidCredentials
	}
	if user.Status != "active" {
		return TokenPair{}, ErrUserDisabled
	}
	if strings.TrimSpace(input.DeviceID) != "" {
		metadata.DeviceID = strings.TrimSpace(input.DeviceID)
	}
	return s.issueSession(ctx, user, metadata)
}

func (s *Service) Refresh(ctx context.Context, rawToken string) (TokenPair, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return TokenPair{}, ErrInvalidCredentials
	}
	currentHash := hashOpaqueToken(rawToken)
	current, session, user, err := s.repository.FindRefreshToken(ctx, currentHash)
	now := s.now().UTC()
	if err != nil || current.RevokedAt != nil || current.ConsumedAt != nil || !current.ExpiresAt.After(now) || session.RevokedAt != nil || !session.ExpiresAt.After(now) {
		return TokenPair{}, ErrInvalidCredentials
	}
	if user.Status != "active" {
		return TokenPair{}, ErrUserDisabled
	}
	nextRaw, nextHash, err := s.newOpaque()
	if err != nil {
		return TokenPair{}, err
	}
	next := RefreshToken{
		ID:        uuid.NewString(),
		SessionID: session.ID,
		TokenHash: nextHash,
		ParentID:  current.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(s.refreshTTL),
	}
	session, user, err = s.repository.RotateRefreshToken(ctx, currentHash, next)
	if err != nil {
		return TokenPair{}, ErrInvalidCredentials
	}
	access, accessExpiry, err := s.tokens.IssueAccess(user.ID, session.ID)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken: access, AccessExpiresAt: accessExpiry,
		RefreshToken: nextRaw, RefreshExpiresAt: next.ExpiresAt,
		SessionID: session.ID, User: user,
	}, nil
}

func (s *Service) Logout(ctx context.Context, userID, sessionID, reason string) error {
	if userID == "" || sessionID == "" {
		return ErrInvalidCredentials
	}
	if strings.TrimSpace(reason) == "" {
		reason = "user_logout"
	}
	return s.repository.RevokeSession(ctx, userID, sessionID, reason)
}

func (s *Service) User(ctx context.Context, userID string) (User, error) {
	user, err := s.repository.FindUserByID(ctx, userID)
	if err != nil {
		return User{}, err
	}
	user.PasswordHash = ""
	return user, nil
}

func (s *Service) ValidateSession(ctx context.Context, userID, sessionID string) error {
	active, err := s.repository.SessionActive(ctx, userID, sessionID)
	if err != nil || !active {
		return ErrSessionRevoked
	}
	return nil
}

func (s *Service) issueSession(ctx context.Context, user User, metadata SessionMetadata) (TokenPair, error) {
	now := s.now().UTC()
	session := Session{
		ID:         uuid.NewString(),
		UserID:     user.ID,
		DeviceID:   strings.TrimSpace(metadata.DeviceID),
		UserAgent:  limit(strings.TrimSpace(metadata.UserAgent), 512),
		IPAddress:  limit(strings.TrimSpace(metadata.IPAddress), 64),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(s.refreshTTL),
	}
	rawRefresh, refreshHash, err := s.newOpaque()
	if err != nil {
		return TokenPair{}, err
	}
	refresh := RefreshToken{
		ID: uuid.NewString(), SessionID: session.ID, TokenHash: refreshHash,
		CreatedAt: now, ExpiresAt: session.ExpiresAt,
	}
	if err := s.repository.SaveSession(ctx, session, refresh); err != nil {
		return TokenPair{}, err
	}
	access, accessExpiry, err := s.tokens.IssueAccess(user.ID, session.ID)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken: access, AccessExpiresAt: accessExpiry,
		RefreshToken: rawRefresh, RefreshExpiresAt: refresh.ExpiresAt,
		SessionID: session.ID, User: user,
	}, nil
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || len(value) > 254 {
		return "", errors.New("valid email is required")
	}
	return value, nil
}

func limit(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum])
}

// Reimplemented locally so auth-service does not depend on a concrete security package.
func hashOpaqueToken(raw string) string {
	return opaqueTokenHash(raw)
}

var opaqueTokenHash = func(raw string) string {
	// The control-plane wires this to the same SHA-256 representation produced by
	// security.NewOpaqueToken. It remains a variable to keep repository tests deterministic.
	return defaultOpaqueTokenHash(raw)
}

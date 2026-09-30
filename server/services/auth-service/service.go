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
	// ListActiveSessions returns up to limit of the user's sessions that are
	// neither revoked nor expired at now, most recently active first.
	ListActiveSessions(ctx context.Context, userID string, now time.Time, limit int) ([]Session, error)
	// RevokeOtherSessions revokes every active session of the user except
	// keepSessionID, with their refresh tokens, and returns their IDs.
	RevokeOtherSessions(ctx context.Context, userID, keepSessionID, reason string, now time.Time) ([]string, error)
}

// SessionSummary describes one active sign-in to the account owner.
type SessionSummary struct {
	ID         string    `json:"id"`
	DeviceID   string    `json:"deviceId,omitempty"`
	UserAgent  string    `json:"userAgent,omitempty"`
	IPAddress  string    `json:"ipAddress,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	// Current marks the session the request was made with.
	Current bool `json:"current"`
}

// maxListedSessions bounds the session list. Every sign-in creates a
// session that lives for the refresh TTL, so an account can accumulate many.
const maxListedSessions = 200

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
	// mfa is the repository's MFARepository side, when it has one.
	mfa       MFARepository
	sealer    SecretSealer
	mfaIssuer string
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
	service := &Service{
		repository: repository,
		passwords:  passwords,
		tokens:     tokens,
		newOpaque:  newOpaque,
		refreshTTL: refreshTTL,
		now:        time.Now,
		mfaIssuer:  defaultMFAIssuer,
	}
	if mfa, ok := repository.(MFARepository); ok {
		service.mfa = mfa
	}
	return service
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

// Login checks the password. Accounts with an active second factor get a
// challenge for VerifyMFA instead of a session.
func (s *Service) Login(ctx context.Context, input LoginInput, metadata SessionMetadata) (LoginResult, error) {
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return LoginResult{}, ErrInvalidCredentials
	}
	user, err := s.repository.FindUserByEmail(ctx, email)
	if err != nil || s.passwords.Compare(user.PasswordHash, input.Password) != nil {
		return LoginResult{}, ErrInvalidCredentials
	}
	if user.Status != "active" {
		return LoginResult{}, ErrUserDisabled
	}
	if strings.TrimSpace(input.DeviceID) != "" {
		metadata.DeviceID = strings.TrimSpace(input.DeviceID)
	}
	required, err := s.mfaRequired(ctx, user.ID)
	if err != nil {
		return LoginResult{}, err
	}
	if required {
		ticket, err := s.startMFAChallenge(ctx, user.ID, metadata.DeviceID)
		if err != nil {
			return LoginResult{}, err
		}
		return LoginResult{MFARequired: true, MFAChallenge: &ticket}, nil
	}
	pair, err := s.issueSession(ctx, user, metadata)
	if err != nil {
		return LoginResult{}, err
	}
	return LoginResult{TokenPair: &pair}, nil
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

// ListSessions returns the caller's active sessions, the current one first.
func (s *Service) ListSessions(ctx context.Context, userID, currentSessionID string) ([]SessionSummary, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, ErrInvalidCredentials
	}
	sessions, err := s.repository.ListActiveSessions(ctx, userID, s.now().UTC(), maxListedSessions)
	if err != nil {
		return nil, err
	}
	items := make([]SessionSummary, 0, len(sessions))
	for _, session := range sessions {
		item := SessionSummary{
			ID: session.ID, DeviceID: session.DeviceID, UserAgent: session.UserAgent, IPAddress: session.IPAddress,
			CreatedAt: session.CreatedAt, LastSeenAt: session.LastSeenAt, ExpiresAt: session.ExpiresAt,
			Current: session.ID == currentSessionID,
		}
		if item.Current {
			items = append([]SessionSummary{item}, items...)
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

// RevokeUserSession ends one of the caller's active sessions; ending the
// current one is a logout. Unknown, foreign, revoked and expired sessions are
// all ErrNotFound, so the endpoint cannot probe other users' session IDs.
func (s *Service) RevokeUserSession(ctx context.Context, userID, currentSessionID, sessionID string) error {
	userID, sessionID = strings.TrimSpace(userID), strings.TrimSpace(sessionID)
	if userID == "" {
		return ErrInvalidCredentials
	}
	if parsed, err := uuid.Parse(sessionID); err != nil || parsed.String() != sessionID {
		return ErrNotFound
	}
	active, err := s.repository.SessionActive(ctx, userID, sessionID)
	if err != nil {
		return err
	}
	if !active {
		return ErrNotFound
	}
	reason := "user_revoked"
	if sessionID == currentSessionID {
		reason = "user_logout"
	}
	return s.repository.RevokeSession(ctx, userID, sessionID, reason)
}

// RevokeOtherSessions signs the caller out everywhere except the current
// session and returns the IDs of the sessions it ended.
func (s *Service) RevokeOtherSessions(ctx context.Context, userID, currentSessionID string) ([]string, error) {
	userID, currentSessionID = strings.TrimSpace(userID), strings.TrimSpace(currentSessionID)
	if userID == "" || currentSessionID == "" {
		return nil, ErrInvalidCredentials
	}
	return s.repository.RevokeOtherSessions(ctx, userID, currentSessionID, "user_revoked_others", s.now().UTC())
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

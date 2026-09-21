package workspaceservice

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var (
	ErrNotFound          = errors.New("workspace not found")
	ErrForbidden         = errors.New("workspace permission denied")
	ErrMemberExists      = errors.New("workspace member already exists")
	ErrLastOwner         = errors.New("workspace must retain at least one owner")
	ErrVersionConflict   = errors.New("workspace version conflict")
	ErrInvalidWorkspace  = errors.New("invalid workspace")
	ErrInvitationInvalid = errors.New("invitation is invalid")
	ErrInvitationExpired = errors.New("invitation has expired")
	ErrInvitationRevoked = errors.New("invitation has been revoked")
	ErrInvitationUsed    = errors.New("invitation has already been used")
)

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Status    string    `json:"status"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Workspace struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organizationId"`
	Name           string     `json:"name"`
	Slug           string     `json:"slug"`
	Status         string     `json:"status"`
	Version        int64      `json:"version"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	DeletedAt      *time.Time `json:"deletedAt,omitempty"`
}

type Membership struct {
	ID          string             `json:"id"`
	WorkspaceID string             `json:"workspaceId"`
	UserID      string             `json:"userId"`
	Role        memberservice.Role `json:"role"`
	Status      string             `json:"status"`
	JoinedAt    time.Time          `json:"joinedAt"`
	UpdatedAt   time.Time          `json:"updatedAt"`
}

type Invitation struct {
	ID          string             `json:"id"`
	WorkspaceID string             `json:"workspaceId"`
	Email       string             `json:"email"`
	Role        memberservice.Role `json:"role"`
	Status      string             `json:"status"`
	ExpiresAt   time.Time          `json:"expiresAt"`
	CreatedAt   time.Time          `json:"createdAt"`
	RevokedAt   *time.Time         `json:"revokedAt,omitempty"`
	AcceptedAt  *time.Time         `json:"acceptedAt,omitempty"`
	Token       string             `json:"token,omitempty"`
}

type OrganizationMembership struct {
	OrganizationID string             `json:"organizationId"`
	UserID         string             `json:"userId"`
	Role           memberservice.Role `json:"role"`
	Status         string             `json:"status"`
}

type Repository interface {
	CreateOrganizationWorkspace(context.Context, Organization, Workspace, Membership) error
	ListWorkspaces(context.Context, string) ([]Workspace, error)
	FindWorkspace(context.Context, string) (Workspace, error)
	UpdateWorkspace(context.Context, Workspace, int64) (Workspace, error)
	FindMembership(context.Context, string, string) (Membership, error)
	FindOrganizationMembership(context.Context, string, string) (OrganizationMembership, error)
	ListMembers(context.Context, string) ([]Membership, error)
	AddMember(context.Context, Membership) error
	UpdateMemberRole(context.Context, string, string, memberservice.Role) (Membership, error)
	RemoveMember(context.Context, string, string) error
	CreateInvitation(context.Context, Invitation, string) error
	ListInvitations(context.Context, string) ([]Invitation, error)
	RevokeInvitation(context.Context, string, string, time.Time) error
	AcceptInvitation(context.Context, string, string, time.Time, Membership) (Membership, error)
}

type Service struct {
	repository Repository
	now        func() time.Time
}

type CreateInput struct {
	Name             string `json:"name"`
	OrganizationName string `json:"organizationName,omitempty"`
}

func New(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

func (s *Service) Create(ctx context.Context, ownerID string, input CreateInput) (Workspace, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 120 {
		return Workspace{}, ErrInvalidWorkspace
	}
	organizationName := strings.TrimSpace(input.OrganizationName)
	if organizationName == "" {
		organizationName = name
	}
	now := s.now().UTC()
	organization := Organization{
		ID: uuid.NewString(), Name: organizationName,
		Status:  "active",
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	organization.Slug = slugify(organizationName)
	if len(organization.Slug) < 2 {
		organization.Slug = "org-" + strings.ReplaceAll(organization.ID[:8], "-", "")
	}
	workspace := Workspace{
		ID: uuid.NewString(), OrganizationID: organization.ID, Name: name,
		Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	workspace.Slug = slugify(name)
	if len(workspace.Slug) < 2 {
		workspace.Slug = "ws-" + strings.ReplaceAll(workspace.ID[:8], "-", "")
	}
	membership := Membership{
		ID: uuid.NewString(), WorkspaceID: workspace.ID, UserID: ownerID,
		Role: memberservice.RoleOwner, Status: "active", JoinedAt: now, UpdatedAt: now,
	}
	if err := s.repository.CreateOrganizationWorkspace(ctx, organization, workspace, membership); err != nil {
		return Workspace{}, err
	}
	return workspace, nil
}

func (s *Service) List(ctx context.Context, userID string) ([]Workspace, error) {
	return s.repository.ListWorkspaces(ctx, userID)
}

func (s *Service) Get(ctx context.Context, userID, workspaceID string) (Workspace, error) {
	if err := s.Require(ctx, workspaceID, userID, memberservice.PermissionWorkspaceRead); err != nil {
		return Workspace{}, err
	}
	return s.repository.FindWorkspace(ctx, workspaceID)
}

func (s *Service) Update(ctx context.Context, userID, workspaceID, name string, expectedVersion int64) (Workspace, error) {
	if err := s.Require(ctx, workspaceID, userID, memberservice.PermissionWorkspaceUpdate); err != nil {
		return Workspace{}, err
	}
	workspace, err := s.repository.FindWorkspace(ctx, workspaceID)
	if err != nil {
		return Workspace{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 120 {
		return Workspace{}, ErrInvalidWorkspace
	}
	workspace.Name = name
	workspace.Slug = slugify(name)
	if len(workspace.Slug) < 2 {
		workspace.Slug = "ws-" + strings.ReplaceAll(workspace.ID[:8], "-", "")
	}
	workspace.UpdatedAt = s.now().UTC()
	return s.repository.UpdateWorkspace(ctx, workspace, expectedVersion)
}

func (s *Service) Require(ctx context.Context, workspaceID, userID string, permission memberservice.Permission) error {
	workspace, workspaceErr := s.repository.FindWorkspace(ctx, workspaceID)
	if workspaceErr != nil || workspace.Status != "active" {
		return ErrForbidden
	}
	membership, err := s.repository.FindMembership(ctx, workspaceID, userID)
	if err != nil || membership.Status != "active" || !memberservice.HasPermission(membership.Role, permission) {
		return ErrForbidden
	}
	return nil
}

// RequireOrganization authorizes organization-scoped surfaces such as
// billing. The repository derives the strongest active role held by the user
// in an active workspace of the organization; a path parameter alone is never
// considered proof of membership.
func (s *Service) RequireOrganization(ctx context.Context, organizationID, userID string, permission memberservice.Permission) error {
	membership, err := s.repository.FindOrganizationMembership(ctx, strings.TrimSpace(organizationID), strings.TrimSpace(userID))
	if err != nil || membership.Status != "active" || !memberservice.HasPermission(membership.Role, permission) {
		return ErrForbidden
	}
	return nil
}

func (s *Service) Members(ctx context.Context, actorID, workspaceID string) ([]Membership, error) {
	if err := s.Require(ctx, workspaceID, actorID, memberservice.PermissionMemberRead); err != nil {
		return nil, err
	}
	return s.repository.ListMembers(ctx, workspaceID)
}

func (s *Service) AddMember(ctx context.Context, actorID, workspaceID, userID string, role memberservice.Role) (Membership, error) {
	if err := s.Require(ctx, workspaceID, actorID, memberservice.PermissionMemberInvite); err != nil {
		return Membership{}, err
	}
	if _, ok := memberservice.ParseRole(string(role)); !ok || role == memberservice.RoleOwner {
		return Membership{}, errors.New("invalid member role")
	}
	now := s.now().UTC()
	membership := Membership{
		ID: uuid.NewString(), WorkspaceID: workspaceID, UserID: userID,
		Role: role, Status: "active", JoinedAt: now, UpdatedAt: now,
	}
	if err := s.repository.AddMember(ctx, membership); err != nil {
		return Membership{}, err
	}
	return membership, nil
}

func (s *Service) ChangeRole(ctx context.Context, actorID, workspaceID, userID string, role memberservice.Role) (Membership, error) {
	if err := s.Require(ctx, workspaceID, actorID, memberservice.PermissionMemberManage); err != nil {
		return Membership{}, err
	}
	if _, ok := memberservice.ParseRole(string(role)); !ok {
		return Membership{}, errors.New("invalid member role")
	}
	return s.repository.UpdateMemberRole(ctx, workspaceID, userID, role)
}

type InviteInput struct {
	Email string             `json:"email"`
	Role  memberservice.Role `json:"role"`
	TTL   time.Duration      `json:"-"`
}

func (s *Service) Invite(ctx context.Context, actorID, workspaceID string, input InviteInput) (Invitation, error) {
	if err := s.Require(ctx, workspaceID, actorID, memberservice.PermissionMemberInvite); err != nil {
		return Invitation{}, err
	}
	role, ok := memberservice.ParseRole(string(input.Role))
	if !ok || role == memberservice.RoleOwner {
		return Invitation{}, ErrInvitationInvalid
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 {
		return Invitation{}, ErrInvitationInvalid
	}
	ttl := input.TTL
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	if ttl > 30*24*time.Hour {
		ttl = 30 * 24 * time.Hour
	}
	raw, err := randomInvitationToken()
	if err != nil {
		return Invitation{}, err
	}
	now := s.now().UTC()
	invitation := Invitation{ID: uuid.NewString(), WorkspaceID: workspaceID, Email: email, Role: role, Status: "pending", ExpiresAt: now.Add(ttl), CreatedAt: now}
	if err := s.repository.CreateInvitation(ctx, invitation, hashInvitationToken(raw)); err != nil {
		return Invitation{}, err
	}
	// Only the creation response contains the bearer token; never persist it.
	invitation.Token = raw
	return invitation, nil
}

func (s *Service) Invitations(ctx context.Context, actorID, workspaceID string) ([]Invitation, error) {
	if err := s.Require(ctx, workspaceID, actorID, memberservice.PermissionMemberRead); err != nil {
		return nil, err
	}
	return s.repository.ListInvitations(ctx, workspaceID)
}
func (s *Service) RevokeInvitation(ctx context.Context, actorID, workspaceID, id string) error {
	if err := s.Require(ctx, workspaceID, actorID, memberservice.PermissionMemberInvite); err != nil {
		return err
	}
	return s.repository.RevokeInvitation(ctx, workspaceID, id, s.now().UTC())
}
func (s *Service) Accept(ctx context.Context, userID, workspaceID, token string) (Membership, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return Membership{}, ErrInvitationInvalid
	}
	now := s.now().UTC()
	membership := Membership{ID: uuid.NewString(), WorkspaceID: workspaceID, UserID: userID, Status: "active", JoinedAt: now, UpdatedAt: now}
	return s.repository.AcceptInvitation(ctx, workspaceID, hashInvitationToken(token), now, membership)
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var result []rune
	lastDash := false
	for _, char := range value {
		valid := (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')
		if valid {
			result = append(result, char)
			lastDash = false
		} else if !lastDash && len(result) > 0 {
			result = append(result, '-')
			lastDash = true
		}
	}
	return strings.Trim(string(result), "-")
}

func randomInvitationToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func hashInvitationToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

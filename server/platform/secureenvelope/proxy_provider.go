package secureenvelope

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
)

type ProxyCredentialRecord struct {
	ID          string
	WorkspaceID string
	ProxyID     string
	Envelope    accountservice.SecretEnvelope
	CreatedAt   time.Time
	RevokedAt   *time.Time
}

type ProxyCredentialRepository interface {
	PutProxyCredential(context.Context, ProxyCredentialRecord) error
	FindProxyCredential(context.Context, string) (ProxyCredentialRecord, error)
	DeleteProxyCredential(context.Context, string, time.Time) error
}

type ProxyProvider struct {
	repository ProxyCredentialRepository
	crypto     *Crypto
	now        func() time.Time
}

func NewProxyProvider(repository ProxyCredentialRepository, crypto *Crypto) (*ProxyProvider, error) {
	if repository == nil || crypto == nil {
		return nil, errors.New("proxy credential repository and envelope crypto are required")
	}
	return &ProxyProvider{repository: repository, crypto: crypto, now: time.Now}, nil
}

func (p *ProxyProvider) Put(ctx context.Context, workspaceID, proxyID string, secret proxyservice.Secret) (string, error) {
	workspaceID, proxyID = strings.TrimSpace(workspaceID), strings.TrimSpace(proxyID)
	if _, err := uuid.Parse(workspaceID); err != nil {
		return "", errors.New("valid workspaceId is required")
	}
	if _, err := uuid.Parse(proxyID); err != nil {
		return "", errors.New("valid proxyId is required")
	}
	if strings.TrimSpace(secret.Password) == "" && strings.TrimSpace(secret.Token) == "" {
		return "", errors.New("proxy secret value is required")
	}
	payload, err := json.Marshal(struct {
		Password string `json:"password,omitempty"`
		Token    string `json:"token,omitempty"`
	}{Password: secret.Password, Token: secret.Token})
	if err != nil {
		return "", err
	}
	defer clear(payload)
	id := uuid.NewString()
	envelope, err := p.crypto.Encrypt(ctx, payload, proxyCredentialAAD(workspaceID, proxyID))
	if err != nil {
		return "", err
	}
	record := ProxyCredentialRecord{ID: id, WorkspaceID: workspaceID, ProxyID: proxyID, Envelope: envelope, CreatedAt: p.now().UTC()}
	if err := p.repository.PutProxyCredential(ctx, record); err != nil {
		return "", err
	}
	return id, nil
}

func (p *ProxyProvider) Delete(ctx context.Context, reference string) error {
	if _, err := uuid.Parse(strings.TrimSpace(reference)); err != nil {
		return errors.New("invalid proxy secret reference")
	}
	return p.repository.DeleteProxyCredential(ctx, strings.TrimSpace(reference), p.now().UTC())
}

// Resolve is intentionally not part of proxyservice.SecretProvider: only the
// trusted local runtime/worker path should depend on this capability.
func (p *ProxyProvider) Resolve(ctx context.Context, workspaceID, proxyID, reference string) (proxyservice.Secret, error) {
	record, err := p.repository.FindProxyCredential(ctx, strings.TrimSpace(reference))
	if err != nil {
		return proxyservice.Secret{}, err
	}
	if record.WorkspaceID != strings.TrimSpace(workspaceID) || record.ProxyID != strings.TrimSpace(proxyID) || record.RevokedAt != nil {
		return proxyservice.Secret{}, errors.New("proxy credential scope mismatch")
	}
	payload, err := p.crypto.Decrypt(ctx, record.Envelope, proxyCredentialAAD(record.WorkspaceID, record.ProxyID))
	if err != nil {
		return proxyservice.Secret{}, err
	}
	defer clear(payload)
	var value struct {
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return proxyservice.Secret{}, errors.New("invalid proxy credential payload")
	}
	return proxyservice.Secret{Password: value.Password, Token: value.Token}, nil
}

func proxyCredentialAAD(workspaceID, proxyID string) []byte {
	return []byte("ant-browser/proxy-credentials/" + workspaceID + "/" + proxyID)
}

func clear(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

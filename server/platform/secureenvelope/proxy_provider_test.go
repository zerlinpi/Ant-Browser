package secureenvelope

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
)

type credentialMemory struct {
	values map[string]ProxyCredentialRecord
}

func (m *credentialMemory) PutProxyCredential(_ context.Context, value ProxyCredentialRecord) error {
	m.values[value.ID] = value
	return nil
}
func (m *credentialMemory) FindProxyCredential(_ context.Context, id string) (ProxyCredentialRecord, error) {
	value, ok := m.values[id]
	if !ok {
		return ProxyCredentialRecord{}, errors.New("not found")
	}
	return value, nil
}
func (m *credentialMemory) DeleteProxyCredential(_ context.Context, id string, now time.Time) error {
	value, ok := m.values[id]
	if !ok {
		return errors.New("not found")
	}
	value.RevokedAt = &now
	m.values[id] = value
	return nil
}

func TestProxyProviderStoresCiphertextAndChecksScope(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	crypto, err := New(key, "kms://test/proxy", "v1")
	if err != nil {
		t.Fatal(err)
	}
	repository := &credentialMemory{values: map[string]ProxyCredentialRecord{}}
	provider, err := NewProxyProvider(repository, crypto)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID, proxyID := uuid.NewString(), uuid.NewString()
	reference, err := provider.Put(context.Background(), workspaceID, proxyID, proxyservice.Secret{Password: "top-secret"})
	if err != nil {
		t.Fatal(err)
	}
	stored := repository.values[reference]
	if stored.Envelope.Ciphertext == "" || stored.Envelope.Ciphertext == "top-secret" {
		t.Fatalf("credential was not encrypted: %+v", stored.Envelope)
	}
	resolved, err := provider.Resolve(context.Background(), workspaceID, proxyID, reference)
	if err != nil || resolved.Password != "top-secret" {
		t.Fatalf("resolve = %+v, %v", resolved, err)
	}
	if _, err := provider.Resolve(context.Background(), uuid.NewString(), proxyID, reference); err == nil {
		t.Fatal("cross-workspace credential resolution succeeded")
	}
}

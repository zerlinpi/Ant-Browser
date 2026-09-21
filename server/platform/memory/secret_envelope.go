package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/secureenvelope"
)

type credentialEnvelopeState struct {
	mu     sync.RWMutex
	values map[string]secureenvelope.ProxyCredentialRecord
}

var credentialEnvelopeStates sync.Map // map[*Store]*credentialEnvelopeState

func (s *Store) credentialEnvelopes() *credentialEnvelopeState {
	if value, ok := credentialEnvelopeStates.Load(s); ok {
		return value.(*credentialEnvelopeState)
	}
	created := &credentialEnvelopeState{values: map[string]secureenvelope.ProxyCredentialRecord{}}
	actual, _ := credentialEnvelopeStates.LoadOrStore(s, created)
	return actual.(*credentialEnvelopeState)
}

func (s *Store) PutProxyCredential(_ context.Context, value secureenvelope.ProxyCredentialRecord) error {
	s.mu.RLock()
	_, workspaceExists := s.workspaces[value.WorkspaceID]
	s.mu.RUnlock()
	if !workspaceExists {
		return errors.New("proxy credential workspace was not found")
	}
	state := s.credentialEnvelopes()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.values[value.ID] = value
	return nil
}

func (s *Store) FindProxyCredential(_ context.Context, reference string) (secureenvelope.ProxyCredentialRecord, error) {
	state := s.credentialEnvelopes()
	state.mu.RLock()
	defer state.mu.RUnlock()
	value, ok := state.values[reference]
	if !ok {
		return secureenvelope.ProxyCredentialRecord{}, errors.New("proxy credential not found")
	}
	return value, nil
}

func (s *Store) DeleteProxyCredential(_ context.Context, reference string, now time.Time) error {
	state := s.credentialEnvelopes()
	state.mu.Lock()
	defer state.mu.Unlock()
	value, ok := state.values[reference]
	if !ok || value.RevokedAt != nil {
		return errors.New("proxy credential not found")
	}
	value.RevokedAt = &now
	state.values[reference] = value
	return nil
}

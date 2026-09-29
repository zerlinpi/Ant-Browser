package memory

import (
	"context"
	"strings"
	"sync"
	"time"

	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
)

// Account Center state is kept in a separate sidecar so the development store
// remains source-compatible with older callers that construct Store literals.
type accountCenterState struct {
	mu       sync.RWMutex
	accounts map[string]accountservice.Account
	secrets  map[string]accountservice.AccountSecret
	bindings map[string]accountservice.AccountBinding
	events   map[string]accountservice.RiskEvent
}

var accountCenterStates sync.Map // map[*Store]*accountCenterState

func (s *Store) accountCenter() *accountCenterState {
	if value, ok := accountCenterStates.Load(s); ok {
		return value.(*accountCenterState)
	}
	created := &accountCenterState{accounts: map[string]accountservice.Account{}, secrets: map[string]accountservice.AccountSecret{}, bindings: map[string]accountservice.AccountBinding{}, events: map[string]accountservice.RiskEvent{}}
	actual, _ := accountCenterStates.LoadOrStore(s, created)
	return actual.(*accountCenterState)
}

func (s *Store) CreateAccount(_ context.Context, account accountservice.Account) error {
	state := s.accountCenter()
	state.mu.Lock()
	defer state.mu.Unlock()
	if _, exists := state.accounts[account.ID]; exists {
		return accountservice.ErrVersionConflict
	}
	if state.identifierTakenLocked(account) {
		return accountservice.ErrIdentifierConflict
	}
	state.accounts[account.ID] = cloneAccount(account)
	return nil
}

// identifierTakenLocked mirrors accounts_live_identifier_uq: an identifier is
// unique per workspace and platform among live accounts, ignoring case.
func (state *accountCenterState) identifierTakenLocked(account accountservice.Account) bool {
	for id, existing := range state.accounts {
		if id != account.ID && existing.WorkspaceID == account.WorkspaceID && existing.DeletedAt == nil &&
			existing.Platform == account.Platform && strings.EqualFold(existing.Identifier, account.Identifier) {
			return true
		}
	}
	return false
}

func (s *Store) FindAccount(_ context.Context, workspaceID, accountID string) (accountservice.Account, error) {
	state := s.accountCenter()
	state.mu.RLock()
	defer state.mu.RUnlock()
	account, ok := state.accounts[accountID]
	if !ok || account.WorkspaceID != workspaceID || account.DeletedAt != nil {
		return accountservice.Account{}, accountservice.ErrNotFound
	}
	return cloneAccount(account), nil
}

func (s *Store) ListAccounts(_ context.Context, workspaceID string) ([]accountservice.Account, error) {
	state := s.accountCenter()
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]accountservice.Account, 0)
	for _, account := range state.accounts {
		if account.WorkspaceID == workspaceID && account.DeletedAt == nil {
			items = append(items, cloneAccount(account))
		}
	}
	return items, nil
}

func (s *Store) UpdateAccount(_ context.Context, account accountservice.Account, expectedVersion int64) (accountservice.Account, error) {
	state := s.accountCenter()
	state.mu.Lock()
	defer state.mu.Unlock()
	current, ok := state.accounts[account.ID]
	if !ok || current.WorkspaceID != account.WorkspaceID || current.DeletedAt != nil {
		return accountservice.Account{}, accountservice.ErrNotFound
	}
	if current.Version != expectedVersion {
		return accountservice.Account{}, accountservice.ErrVersionConflict
	}
	if state.identifierTakenLocked(account) {
		return accountservice.Account{}, accountservice.ErrIdentifierConflict
	}
	account.Version = current.Version + 1
	account.CreatedAt = current.CreatedAt
	account.UpdatedAt = account.UpdatedAt.UTC()
	state.accounts[account.ID] = cloneAccount(account)
	return cloneAccount(account), nil
}

func (s *Store) DeleteAccount(_ context.Context, workspaceID, accountID string, expectedVersion int64, now time.Time) error {
	state := s.accountCenter()
	state.mu.Lock()
	defer state.mu.Unlock()
	account, ok := state.accounts[accountID]
	if !ok || account.WorkspaceID != workspaceID || account.DeletedAt != nil {
		return accountservice.ErrNotFound
	}
	if account.Version != expectedVersion {
		return accountservice.ErrVersionConflict
	}
	account.DeletedAt, account.UpdatedAt, account.Status = &now, now, accountservice.StatusDeleted
	account.Version++
	state.accounts[account.ID] = account
	return nil
}

func (s *Store) PutAccountSecret(_ context.Context, secret accountservice.AccountSecret) error {
	state := s.accountCenter()
	state.mu.Lock()
	defer state.mu.Unlock()
	account, ok := state.accounts[secret.AccountID]
	if !ok || account.WorkspaceID != secret.WorkspaceID || account.DeletedAt != nil {
		return accountservice.ErrNotFound
	}
	state.secrets[secret.ID] = cloneSecret(secret)
	return nil
}

func (s *Store) ListAccountSecrets(_ context.Context, workspaceID, accountID string) ([]accountservice.AccountSecret, error) {
	state := s.accountCenter()
	state.mu.RLock()
	defer state.mu.RUnlock()
	account, ok := state.accounts[accountID]
	if !ok || account.WorkspaceID != workspaceID || account.DeletedAt != nil {
		return nil, accountservice.ErrNotFound
	}
	items := make([]accountservice.AccountSecret, 0)
	for _, secret := range state.secrets {
		if secret.AccountID == accountID && secret.WorkspaceID == workspaceID {
			items = append(items, cloneSecret(secret))
		}
	}
	return items, nil
}

func (s *Store) DeleteAccountSecret(_ context.Context, workspaceID, accountID, secretID string) error {
	state := s.accountCenter()
	state.mu.Lock()
	defer state.mu.Unlock()
	secret, ok := state.secrets[secretID]
	if !ok || secret.AccountID != accountID || secret.WorkspaceID != workspaceID {
		return accountservice.ErrNotFound
	}
	delete(state.secrets, secretID)
	return nil
}

func (s *Store) UpsertAccountBinding(_ context.Context, binding accountservice.AccountBinding) (accountservice.AccountBinding, error) {
	state := s.accountCenter()
	state.mu.Lock()
	defer state.mu.Unlock()
	account, ok := state.accounts[binding.AccountID]
	if !ok || account.WorkspaceID != binding.WorkspaceID || account.DeletedAt != nil {
		return accountservice.AccountBinding{}, accountservice.ErrNotFound
	}
	for id, current := range state.bindings {
		if current.AccountID == binding.AccountID && current.WorkspaceID == binding.WorkspaceID && current.BindingType == binding.BindingType && current.TargetID == binding.TargetID {
			binding.ID = id
			binding.CreatedAt = current.CreatedAt
		}
	}
	state.bindings[binding.ID] = binding
	return binding, nil
}

func (s *Store) ListAccountBindings(_ context.Context, workspaceID, accountID string) ([]accountservice.AccountBinding, error) {
	state := s.accountCenter()
	state.mu.RLock()
	defer state.mu.RUnlock()
	account, ok := state.accounts[accountID]
	if !ok || account.WorkspaceID != workspaceID || account.DeletedAt != nil {
		return nil, accountservice.ErrNotFound
	}
	items := make([]accountservice.AccountBinding, 0)
	for _, binding := range state.bindings {
		if binding.AccountID == accountID && binding.WorkspaceID == workspaceID {
			items = append(items, binding)
		}
	}
	return items, nil
}

func (s *Store) DeleteAccountBinding(_ context.Context, workspaceID, accountID, bindingID string) error {
	state := s.accountCenter()
	state.mu.Lock()
	defer state.mu.Unlock()
	binding, ok := state.bindings[bindingID]
	if !ok || binding.AccountID != accountID || binding.WorkspaceID != workspaceID {
		return accountservice.ErrNotFound
	}
	delete(state.bindings, bindingID)
	return nil
}

func (s *Store) CreateRiskEvent(_ context.Context, event accountservice.RiskEvent) error {
	state := s.accountCenter()
	state.mu.Lock()
	defer state.mu.Unlock()
	account, ok := state.accounts[event.AccountID]
	if !ok || account.WorkspaceID != event.WorkspaceID || account.DeletedAt != nil {
		return accountservice.ErrNotFound
	}
	state.events[event.ID] = event
	return nil
}

func (s *Store) ListRiskEvents(_ context.Context, workspaceID, accountID string) ([]accountservice.RiskEvent, error) {
	state := s.accountCenter()
	state.mu.RLock()
	defer state.mu.RUnlock()
	account, ok := state.accounts[accountID]
	if !ok || account.WorkspaceID != workspaceID || account.DeletedAt != nil {
		return nil, accountservice.ErrNotFound
	}
	items := make([]accountservice.RiskEvent, 0)
	for _, event := range state.events {
		if event.AccountID == accountID && event.WorkspaceID == workspaceID {
			items = append(items, event)
		}
	}
	return items, nil
}

func cloneAccount(value accountservice.Account) accountservice.Account {
	value.Metadata = cloneStringMap(value.Metadata)
	return value
}
func cloneSecret(value accountservice.AccountSecret) accountservice.AccountSecret { return value }
func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

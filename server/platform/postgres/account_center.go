package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	accountservice "github.com/zerlinpi/Ant-Browser/server/services/account-service"
)

// The account tables are defined by database/migrations/004_accounts_proxies.sql.
// Version/deleted_at are added by the account-center follow-up migration.
const accountColumns = `a.id::text, a.workspace_id::text, a.platform,
 a.display_name, a.external_identifier, a.external_id,
 a.username, a.email, a.region, a.status, a.risk_level, a.notes,
 a.metadata, a.version, a.created_at, a.updated_at, a.deleted_at,
 COALESCE(a.profile_id::text, ''), COALESCE(a.browser_instance_id::text, '')`

func (s *Store) CreateAccount(ctx context.Context, account accountservice.Account) error {
	metadata, err := json.Marshal(account.Metadata)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO accounts
		(id, workspace_id, platform, external_identifier, display_name, external_id, username, email, region,
		 profile_id, browser_instance_id, status, risk_level, notes, metadata, version, created_by, created_at, updated_at)
		VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,'')::uuid,NULLIF($11,'')::uuid,
		 $12,$13,$14,$15::jsonb,$16,NULLIF($17,'')::uuid,$18,$19)`,
		account.ID, account.WorkspaceID, account.Platform, account.Identifier, account.Name,
		account.ExternalID, account.Username, account.Email, account.Region, account.ProfileID,
		account.BrowserInstanceID, account.Status, account.RiskLevel, account.Notes, metadata,
		account.Version, account.CreatedBy, account.CreatedAt, account.UpdatedAt)
	if isUniqueViolation(err) {
		return accountservice.ErrVersionConflict
	}
	if isForeignKeyViolation(err) {
		return accountservice.ErrNotFound
	}
	return err
}

func (s *Store) FindAccount(ctx context.Context, workspaceID, accountID string) (accountservice.Account, error) {
	return scanAccount(s.pool.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts a WHERE a.workspace_id=$1::uuid AND a.id=$2::uuid AND a.deleted_at IS NULL`, workspaceID, accountID))
}

func (s *Store) ListAccounts(ctx context.Context, workspaceID string) ([]accountservice.Account, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+accountColumns+` FROM accounts a WHERE a.workspace_id=$1::uuid AND a.deleted_at IS NULL ORDER BY a.created_at DESC, a.id DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]accountservice.Account, 0)
	for rows.Next() {
		item, scanErr := scanAccount(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) UpdateAccount(ctx context.Context, account accountservice.Account, expectedVersion int64) (accountservice.Account, error) {
	metadata, err := json.Marshal(account.Metadata)
	if err != nil {
		return accountservice.Account{}, err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE accounts SET external_identifier=$4, display_name=$5,
		external_id=$6, username=$7, email=$8, region=$9,
		profile_id=NULLIF($10,'')::uuid, browser_instance_id=NULLIF($11,'')::uuid,
		status=$12, risk_level=$13, notes=$14, metadata=$15::jsonb,
		updated_at=$16, version=version+1
		WHERE id=$1::uuid AND workspace_id=$2::uuid AND version=$3 AND deleted_at IS NULL`,
		account.ID, account.WorkspaceID, expectedVersion, account.Identifier, account.Name,
		account.ExternalID, account.Username, account.Email, account.Region, account.ProfileID,
		account.BrowserInstanceID, account.Status, account.RiskLevel, account.Notes, metadata, account.UpdatedAt)
	if err != nil {
		return accountservice.Account{}, err
	}
	if tag.RowsAffected() != 1 {
		current, findErr := s.FindAccount(ctx, account.WorkspaceID, account.ID)
		if findErr == nil && current.Version != expectedVersion {
			return accountservice.Account{}, accountservice.ErrVersionConflict
		}
		return accountservice.Account{}, accountservice.ErrNotFound
	}
	return s.FindAccount(ctx, account.WorkspaceID, account.ID)
}

func (s *Store) DeleteAccount(ctx context.Context, workspaceID, accountID string, expectedVersion int64, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE accounts SET status='deleted', deleted_at=$4, updated_at=$4, version=version+1 WHERE workspace_id=$1::uuid AND id=$2::uuid AND version=$3 AND deleted_at IS NULL`, workspaceID, accountID, expectedVersion, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	current, findErr := s.FindAccount(ctx, workspaceID, accountID)
	if findErr == nil && current.Version != expectedVersion {
		return accountservice.ErrVersionConflict
	}
	return accountservice.ErrNotFound
}

func (s *Store) PutAccountSecret(ctx context.Context, secret accountservice.AccountSecret) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO account_secrets
		(id,workspace_id,account_id,secret_type,ciphertext,nonce,encrypted_dek,encryption_key_ref,
		 algorithm,key_version,secret_fingerprint,version,created_at)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::bytea,$6::bytea,NULLIF($7,'')::bytea,$8,$9,$10,NULLIF($11,'')::bytea,$12,$13)
		ON CONFLICT (workspace_id,account_id,secret_type) DO UPDATE SET
		 id=EXCLUDED.id,ciphertext=EXCLUDED.ciphertext,nonce=EXCLUDED.nonce,encrypted_dek=EXCLUDED.encrypted_dek,
		 encryption_key_ref=EXCLUDED.encryption_key_ref,algorithm=EXCLUDED.algorithm,key_version=EXCLUDED.key_version,
		 secret_fingerprint=EXCLUDED.secret_fingerprint,version=account_secrets.version+1,
		 rotated_at=EXCLUDED.created_at,revoked_at=NULL`,
		secret.ID, secret.WorkspaceID, secret.AccountID, secret.Kind, []byte(secret.Envelope.Ciphertext),
		[]byte(secret.Envelope.Nonce), []byte(secret.Envelope.EncryptedDEK), secret.Envelope.KeyReference,
		secret.Envelope.Algorithm, secret.Envelope.KeyVersion, []byte(secret.Envelope.Fingerprint),
		secret.Version, secret.CreatedAt)
	if isForeignKeyViolation(err) {
		return accountservice.ErrNotFound
	}
	return err
}

func (s *Store) ListAccountSecrets(ctx context.Context, workspaceID, accountID string) ([]accountservice.AccountSecret, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,secret_type,algorithm,ciphertext,nonce,
		COALESCE(encrypted_dek,''::bytea),encryption_key_ref,key_version,COALESCE(secret_fingerprint,''::bytea),
		version,created_at,COALESCE(rotated_at,created_at)
		FROM account_secrets WHERE workspace_id=$1::uuid AND account_id=$2::uuid AND revoked_at IS NULL
		ORDER BY created_at DESC`, workspaceID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]accountservice.AccountSecret, 0)
	for rows.Next() {
		var item accountservice.AccountSecret
		var ciphertext, nonce, encryptedDEK, fingerprint []byte
		item.WorkspaceID, item.AccountID = workspaceID, accountID
		if err := rows.Scan(&item.ID, &item.Kind, &item.Envelope.Algorithm, &ciphertext, &nonce,
			&encryptedDEK, &item.Envelope.KeyReference, &item.Envelope.KeyVersion, &fingerprint,
			&item.Version, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.SecretType = item.Kind
		item.Envelope.Ciphertext, item.Envelope.Nonce = string(ciphertext), string(nonce)
		item.Envelope.EncryptedDEK, item.Envelope.Fingerprint = string(encryptedDEK), string(fingerprint)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeleteAccountSecret(ctx context.Context, workspaceID, accountID, secretID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE account_secrets SET revoked_at=now() WHERE workspace_id=$1::uuid AND account_id=$2::uuid AND id=$3::uuid AND revoked_at IS NULL`, workspaceID, accountID, secretID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return accountservice.ErrNotFound
	}
	return nil
}

func (s *Store) UpsertAccountBinding(ctx context.Context, binding accountservice.AccountBinding) (accountservice.AccountBinding, error) {
	var profileID, instanceID, proxyID string
	switch binding.BindingType {
	case "profile":
		profileID = binding.TargetID
	case "browser_instance":
		instanceID = binding.TargetID
	case "proxy":
		proxyID = binding.TargetID
	default:
		return accountservice.AccountBinding{}, accountservice.ErrUnsupported
	}
	item, err := scanAccountBinding(s.pool.QueryRow(ctx, `INSERT INTO account_bindings
		(id,workspace_id,account_id,binding_type,profile_id,browser_instance_id,proxy_id,status,version,created_at,updated_at)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,$8,1,$9,$10)
		ON CONFLICT (workspace_id,account_id,binding_type) DO UPDATE SET
		 profile_id=EXCLUDED.profile_id,browser_instance_id=EXCLUDED.browser_instance_id,proxy_id=EXCLUDED.proxy_id,
		 status='active',version=account_bindings.version+1,updated_at=EXCLUDED.updated_at
		RETURNING id::text,workspace_id::text,account_id::text,binding_type,
		 COALESCE(profile_id,browser_instance_id,proxy_id)::text,status,created_at,updated_at`,
		binding.ID, binding.WorkspaceID, binding.AccountID, binding.BindingType,
		profileID, instanceID, proxyID, binding.Status, binding.CreatedAt, binding.UpdatedAt))
	if isForeignKeyViolation(err) {
		return accountservice.AccountBinding{}, accountservice.ErrNotFound
	}
	return item, err
}

func (s *Store) ListAccountBindings(ctx context.Context, workspaceID, accountID string) ([]accountservice.AccountBinding, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,workspace_id::text,account_id::text,binding_type,
		COALESCE(profile_id,browser_instance_id,proxy_id)::text,status,created_at,updated_at
		FROM account_bindings WHERE workspace_id=$1::uuid AND account_id=$2::uuid AND status='active'
		ORDER BY binding_type,id`, workspaceID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]accountservice.AccountBinding, 0)
	for rows.Next() {
		item, scanErr := scanAccountBinding(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeleteAccountBinding(ctx context.Context, workspaceID, accountID, bindingID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE account_bindings SET status='inactive',version=version+1,updated_at=now()
		WHERE workspace_id=$1::uuid AND account_id=$2::uuid AND id=$3::uuid AND status='active'`, workspaceID, accountID, bindingID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return accountservice.ErrNotFound
	}
	return nil
}

func (s *Store) CreateRiskEvent(ctx context.Context, event accountservice.RiskEvent) error {
	details, _ := json.Marshal(map[string]string{"code": event.Code, "description": event.Description, "createdBy": event.CreatedBy})
	_, err := s.pool.Exec(ctx, `INSERT INTO risk_events (id,workspace_id,account_id,severity,event_type,details,created_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::jsonb,$7)`, event.ID, event.WorkspaceID, event.AccountID, event.Level, "account."+event.Code, details, event.CreatedAt)
	if isForeignKeyViolation(err) {
		return accountservice.ErrNotFound
	}
	return err
}
func (s *Store) ListRiskEvents(ctx context.Context, workspaceID, accountID string) ([]accountservice.RiskEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,severity,event_type,details,created_at FROM risk_events WHERE workspace_id=$1::uuid AND account_id=$2::uuid ORDER BY created_at DESC`, workspaceID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]accountservice.RiskEvent, 0)
	for rows.Next() {
		var item accountservice.RiskEvent
		var details []byte
		item.WorkspaceID, item.AccountID = workspaceID, accountID
		if err := rows.Scan(&item.ID, &item.Level, &item.Code, &details, &item.CreatedAt); err != nil {
			return nil, err
		}
		var values map[string]string
		_ = json.Unmarshal(details, &values)
		item.Description = values["description"]
		item.CreatedBy = values["createdBy"]
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanAccount(row scanner) (accountservice.Account, error) {
	var account accountservice.Account
	var metadata []byte
	if err := row.Scan(&account.ID, &account.WorkspaceID, &account.Platform, &account.Name, &account.Identifier, &account.ExternalID, &account.Username, &account.Email, &account.Region, &account.Status, &account.RiskLevel, &account.Notes, &metadata, &account.Version, &account.CreatedAt, &account.UpdatedAt, &account.DeletedAt, &account.ProfileID, &account.BrowserInstanceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return accountservice.Account{}, accountservice.ErrNotFound
		}
		return accountservice.Account{}, err
	}
	account.DisplayName, account.ExternalIdentifier = account.Name, account.Identifier
	if len(metadata) > 0 {
		_ = json.Unmarshal(metadata, &account.Metadata)
	}
	return account, nil
}

func scanAccountBinding(row scanner) (accountservice.AccountBinding, error) {
	var binding accountservice.AccountBinding
	if err := row.Scan(&binding.ID, &binding.WorkspaceID, &binding.AccountID, &binding.BindingType,
		&binding.TargetID, &binding.Status, &binding.CreatedAt, &binding.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return accountservice.AccountBinding{}, accountservice.ErrNotFound
		}
		return accountservice.AccountBinding{}, err
	}
	return binding, nil
}

package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/zerlinpi/Ant-Browser/server/platform/secureenvelope"
)

func (s *Store) PutProxyCredential(ctx context.Context, value secureenvelope.ProxyCredentialRecord) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO secret_envelopes
		(id,workspace_id,purpose,subject_id,algorithm,ciphertext,nonce,encrypted_dek,
		 encryption_key_ref,key_version,secret_fingerprint,created_at)
		VALUES ($1::uuid,$2::uuid,'proxy_credentials',$3::uuid,$4,$5::bytea,$6::bytea,$7::bytea,$8,$9,NULLIF($10,'')::bytea,$11)`,
		value.ID, value.WorkspaceID, value.ProxyID, value.Envelope.Algorithm,
		[]byte(value.Envelope.Ciphertext), []byte(value.Envelope.Nonce), []byte(value.Envelope.EncryptedDEK),
		value.Envelope.KeyReference, value.Envelope.KeyVersion, []byte(value.Envelope.Fingerprint), value.CreatedAt)
	if isForeignKeyViolation(err) {
		return errors.New("proxy credential workspace was not found")
	}
	return err
}

func (s *Store) FindProxyCredential(ctx context.Context, reference string) (secureenvelope.ProxyCredentialRecord, error) {
	var value secureenvelope.ProxyCredentialRecord
	var ciphertext, nonce, encryptedDEK, fingerprint []byte
	err := s.pool.QueryRow(ctx, `SELECT id::text,workspace_id::text,subject_id::text,algorithm,ciphertext,nonce,
		encrypted_dek,encryption_key_ref,key_version,COALESCE(secret_fingerprint,''::bytea),created_at,revoked_at
		FROM secret_envelopes WHERE id=$1::uuid AND purpose='proxy_credentials'`, reference).Scan(
		&value.ID, &value.WorkspaceID, &value.ProxyID, &value.Envelope.Algorithm, &ciphertext, &nonce,
		&encryptedDEK, &value.Envelope.KeyReference, &value.Envelope.KeyVersion, &fingerprint,
		&value.CreatedAt, &value.RevokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return secureenvelope.ProxyCredentialRecord{}, errors.New("proxy credential not found")
	}
	value.Envelope.Ciphertext, value.Envelope.Nonce = string(ciphertext), string(nonce)
	value.Envelope.EncryptedDEK, value.Envelope.Fingerprint = string(encryptedDEK), string(fingerprint)
	return value, err
}

func (s *Store) DeleteProxyCredential(ctx context.Context, reference string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE secret_envelopes SET revoked_at=$2
		WHERE id=$1::uuid AND purpose='proxy_credentials' AND revoked_at IS NULL`, reference, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("proxy credential not found")
	}
	return nil
}

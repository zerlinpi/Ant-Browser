package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	deviceservice "github.com/zerlinpi/Ant-Browser/server/services/device-service"
)

const deviceColumns = `
	d.id::text, d.workspace_id::text, COALESCE(d.user_id::text, ''), d.name,
	d.platform, d.agent_version, d.capabilities, d.status, d.last_seen_at,
	d.created_at, d.updated_at, d.revoked_at`

func (s *Store) CreateDevice(ctx context.Context, device deviceservice.Device, credential deviceservice.Credential) error {
	capabilities, err := json.Marshal(device.Capabilities)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	_, err = tx.Exec(ctx, `
		INSERT INTO devices (
			id, workspace_id, user_id, device_key, name, platform, agent_version,
			capabilities, status, last_seen_at, created_at, updated_at, revoked_at
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $1, $4, $5, $6,
			$7::jsonb, $8, $9, $10, $11, $12
		)
	`, device.ID, device.WorkspaceID, device.UserID, device.Name, device.Platform, device.AgentVersion, capabilities, device.Status, device.LastSeenAt, device.CreatedAt, device.UpdatedAt, device.RevokedAt)
	if err != nil {
		if isForeignKeyViolation(err) {
			return deviceservice.ErrNotFound
		}
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO device_credentials (
			id, device_id, credential_hash, key_version, created_at, expires_at, revoked_at
		) VALUES ($1::uuid, $2::uuid, decode($3, 'hex'), 'v1', $4, $5, $6)
	`, credential.ID, credential.DeviceID, credential.SecretHash, credential.CreatedAt, credential.ExpiresAt, credential.RevokedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ListDevices(ctx context.Context, userID string) ([]deviceservice.Device, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+deviceColumns+` FROM devices d
		WHERE d.user_id = $1::uuid
		ORDER BY d.created_at, d.id
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]deviceservice.Device, 0)
	for rows.Next() {
		device, scanErr := scanDevice(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, device)
	}
	return items, rows.Err()
}

func (s *Store) FindDevice(ctx context.Context, id string) (deviceservice.Device, error) {
	return scanDevice(s.pool.QueryRow(ctx, `
		SELECT `+deviceColumns+` FROM devices d WHERE d.id = $1::uuid
	`, id))
}

func (s *Store) Heartbeat(ctx context.Context, deviceID, version string, capabilities map[string]interface{}, now time.Time) (deviceservice.Device, error) {
	capabilitiesJSON, err := json.Marshal(capabilities)
	if err != nil {
		return deviceservice.Device{}, err
	}
	return scanDevice(s.pool.QueryRow(ctx, `
		UPDATE devices AS d
		SET status = 'online', last_seen_at = $2,
		    agent_version = CASE WHEN $3 = '' THEN agent_version ELSE $3 END,
		    capabilities = CASE WHEN $4::jsonb = 'null'::jsonb THEN capabilities ELSE $4::jsonb END,
		    updated_at = $2
		WHERE id = $1::uuid AND revoked_at IS NULL
		RETURNING `+deviceColumns+`
	`, deviceID, now, version, capabilitiesJSON))
}

func (s *Store) RevokeDevice(ctx context.Context, userID, deviceID string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	tag, err := tx.Exec(ctx, `
		UPDATE devices
		SET status = 'revoked', revoked_at = COALESCE(revoked_at, $3), updated_at = $3
		WHERE id = $1::uuid AND user_id = $2::uuid
	`, deviceID, userID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return deviceservice.ErrNotFound
	}
	_, err = tx.Exec(ctx, `
		UPDATE device_credentials SET revoked_at = COALESCE(revoked_at, $2)
		WHERE device_id = $1::uuid
	`, deviceID, now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RotateDeviceCredential locks the user's device row, revokes every active
// credential of the device, and inserts the replacement in one transaction.
// The row lock serializes rotation with revocation: a concurrent RevokeDevice
// either waits and then revokes the new credential too, or commits first and
// makes this rotation fail with ErrRevoked.
func (s *Store) RotateDeviceCredential(ctx context.Context, userID, deviceID string, credential deviceservice.Credential, now time.Time) (deviceservice.Device, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return deviceservice.Device{}, err
	}
	defer rollback(ctx, tx)
	device, err := scanDevice(tx.QueryRow(ctx, `
		SELECT `+deviceColumns+` FROM devices d
		WHERE d.id = $1::uuid AND d.user_id = $2::uuid
		FOR UPDATE OF d
	`, deviceID, userID))
	if err != nil {
		return deviceservice.Device{}, err
	}
	if device.RevokedAt != nil {
		return deviceservice.Device{}, deviceservice.ErrRevoked
	}
	if _, err := tx.Exec(ctx, `
		UPDATE device_credentials SET revoked_at = $2
		WHERE device_id = $1::uuid AND revoked_at IS NULL
	`, deviceID, now); err != nil {
		return deviceservice.Device{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO device_credentials (
			id, device_id, credential_hash, key_version, created_at, expires_at, revoked_at
		) VALUES ($1::uuid, $2::uuid, decode($3, 'hex'), 'v1', $4, $5, NULL)
	`, credential.ID, deviceID, credential.SecretHash, credential.CreatedAt, credential.ExpiresAt); err != nil {
		return deviceservice.Device{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return deviceservice.Device{}, err
	}
	return device, nil
}

func (s *Store) AuthenticateDevice(ctx context.Context, deviceID, credentialHash string) (deviceservice.Device, error) {
	return scanDevice(s.pool.QueryRow(ctx, `
		SELECT `+deviceColumns+`
		FROM authenticate_device($1::uuid, $2) d
	`, deviceID, credentialHash))
}

func scanDevice(row scanner) (deviceservice.Device, error) {
	var device deviceservice.Device
	var capabilities []byte
	if err := row.Scan(
		&device.ID, &device.WorkspaceID, &device.UserID, &device.Name,
		&device.Platform, &device.AgentVersion, &capabilities, &device.Status,
		&device.LastSeenAt, &device.CreatedAt, &device.UpdatedAt, &device.RevokedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return deviceservice.Device{}, deviceservice.ErrNotFound
		}
		return deviceservice.Device{}, err
	}
	if len(capabilities) > 0 {
		if err := json.Unmarshal(capabilities, &device.Capabilities); err != nil {
			return deviceservice.Device{}, err
		}
	}
	if device.Capabilities == nil {
		device.Capabilities = map[string]interface{}{}
	}
	return device, nil
}

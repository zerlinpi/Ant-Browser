package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	billingservice "github.com/zerlinpi/Ant-Browser/server/services/billing-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

const instanceColumns = `
	b.id::text, b.workspace_id::text, b.name, b.platform,
	COALESCE(b.fingerprint_template_id::text, ''), COALESCE(b.proxy_assignment_id::text, ''),
	COALESCE(b.profile_id::text, ''), b.desired_state, b.observed_state,
	COALESCE(b.assigned_device_id::text, ''), b.current_revision, b.version,
	b.tags, b.last_seen_at, b.created_at, b.updated_at, b.deleted_at`

const commandColumns = `
	c.id::text, c.workspace_id::text, c.instance_id::text,
	COALESCE(c.device_id::text, ''), c.action, c.idempotency_key,
	COALESCE(c.expected_version, 0), c.status, c.payload,
	COALESCE(c.deadline, c.created_at), COALESCE(c.created_by::text, ''),
	c.created_at, c.acknowledged_at, c.completed_at,
	c.error_code, c.failure_message`

func (s *Store) CreateInstance(ctx context.Context, instance browserinstanceservice.BrowserInstance) error {
	tags, err := json.Marshal(instance.Tags)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO browser_instances (
			id, workspace_id, name, platform, fingerprint_template_id,
			proxy_assignment_id, profile_id, desired_state, observed_state,
			assigned_device_id, current_revision, version, tags, last_seen_at,
			created_at, updated_at, deleted_at
		) VALUES (
			$1::uuid, $2::uuid, $3, $4, NULLIF($5, '')::uuid,
			NULLIF($6, '')::uuid, NULLIF($7, '')::uuid, $8, $9,
			NULLIF($10, '')::uuid, $11, $12, $13::jsonb, $14, $15, $16, $17
		)
	`, instance.ID, instance.WorkspaceID, instance.Name, instance.Platform,
		instance.FingerprintTemplateID, instance.ProxyAssignmentID, instance.ProfileID,
		instance.DesiredState, instance.ObservedState, instance.AssignedDeviceID,
		instance.CurrentRevision, instance.Version, tags, instance.LastSeenAt,
		instance.CreatedAt, instance.UpdatedAt, instance.DeletedAt)
	if isForeignKeyViolation(err) {
		return workspaceservice.ErrNotFound
	}
	if isUniqueViolation(err) {
		return errors.New("browser instance name already exists")
	}
	if isBillingQuotaViolation(err) {
		return billingservice.ErrQuotaExceeded
	}
	return err
}

func (s *Store) FindInstance(ctx context.Context, workspaceID, instanceID string) (browserinstanceservice.BrowserInstance, error) {
	return scanInstance(s.pool.QueryRow(ctx, `
		SELECT `+instanceColumns+`
		FROM browser_instances b
		WHERE b.workspace_id = $1::uuid AND b.id = $2::uuid AND b.deleted_at IS NULL
	`, workspaceID, instanceID))
}

func (s *Store) ListInstances(ctx context.Context, workspaceID string) ([]browserinstanceservice.BrowserInstance, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+instanceColumns+`
		FROM browser_instances b
		WHERE b.workspace_id = $1::uuid AND b.deleted_at IS NULL
		ORDER BY b.created_at, b.id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]browserinstanceservice.BrowserInstance, 0)
	for rows.Next() {
		instance, scanErr := scanInstance(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, instance)
	}
	return items, rows.Err()
}

func scanInstance(row scanner) (browserinstanceservice.BrowserInstance, error) {
	var instance browserinstanceservice.BrowserInstance
	var tags []byte
	if err := row.Scan(
		&instance.ID, &instance.WorkspaceID, &instance.Name, &instance.Platform,
		&instance.FingerprintTemplateID, &instance.ProxyAssignmentID, &instance.ProfileID,
		&instance.DesiredState, &instance.ObservedState, &instance.AssignedDeviceID,
		&instance.CurrentRevision, &instance.Version, &tags, &instance.LastSeenAt,
		&instance.CreatedAt, &instance.UpdatedAt, &instance.DeletedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrNotFound
		}
		return browserinstanceservice.BrowserInstance{}, err
	}
	if len(tags) > 0 {
		if err := json.Unmarshal(tags, &instance.Tags); err != nil {
			return browserinstanceservice.BrowserInstance{}, err
		}
	}
	if instance.Tags == nil {
		instance.Tags = []string{}
	}
	return instance, nil
}

func (s *Store) UpdateInstance(ctx context.Context, instance browserinstanceservice.BrowserInstance, expectedVersion int64) (browserinstanceservice.BrowserInstance, error) {
	tags, err := json.Marshal(instance.Tags)
	if err != nil {
		return browserinstanceservice.BrowserInstance{}, err
	}
	updated, err := scanInstance(s.pool.QueryRow(ctx, `
		UPDATE browser_instances AS b
		SET name = $4, platform = $5,
		    fingerprint_template_id = NULLIF($6, '')::uuid,
		    proxy_assignment_id = NULLIF($7, '')::uuid,
		    profile_id = NULLIF($8, '')::uuid,
		    desired_state = $9, observed_state = $10,
		    assigned_device_id = NULLIF($11, '')::uuid,
		    current_revision = $12, tags = $13::jsonb,
		    last_seen_at = $14, updated_at = $15, version = version + 1
		WHERE b.workspace_id = $1::uuid AND b.id = $2::uuid
		  AND b.version = $3 AND b.deleted_at IS NULL
		RETURNING `+instanceColumns+`
	`, instance.WorkspaceID, instance.ID, expectedVersion, instance.Name, instance.Platform,
		instance.FingerprintTemplateID, instance.ProxyAssignmentID, instance.ProfileID,
		instance.DesiredState, instance.ObservedState, instance.AssignedDeviceID,
		instance.CurrentRevision, tags, instance.LastSeenAt, instance.UpdatedAt))
	if isForeignKeyViolation(err) {
		return browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrInvalidInput
	}
	if errors.Is(err, browserinstanceservice.ErrNotFound) {
		if _, findErr := s.FindInstance(ctx, instance.WorkspaceID, instance.ID); findErr == nil {
			return browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrVersionConflict
		}
	}
	return updated, err
}

func (s *Store) SoftDeleteInstance(ctx context.Context, workspaceID, instanceID string, expectedVersion int64, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE browser_instances
		SET desired_state = 'deleted', deleted_at = $4, updated_at = $4, version = version + 1
		WHERE workspace_id = $1::uuid AND id = $2::uuid AND version = $3 AND deleted_at IS NULL
	`, workspaceID, instanceID, expectedVersion, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	if _, findErr := s.FindInstance(ctx, workspaceID, instanceID); findErr == nil {
		return browserinstanceservice.ErrVersionConflict
	}
	return browserinstanceservice.ErrNotFound
}

func (s *Store) CreateCommand(ctx context.Context, command browserinstanceservice.Command, desiredState string) (browserinstanceservice.Command, browserinstanceservice.BrowserInstance, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, err
	}
	defer rollback(ctx, tx)
	if existing, findErr := scanCommand(tx.QueryRow(ctx, `
		SELECT `+commandColumns+` FROM instance_commands c
		WHERE c.workspace_id = $1::uuid AND c.idempotency_key = $2
	`, command.WorkspaceID, command.IdempotencyKey)); findErr == nil {
		if !existing.MatchesRequest(command.InstanceID, command.Action, command.ExpectedVersion, command.Payload) {
			return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrIdempotencyConflict
		}
		instance, instanceErr := scanInstance(tx.QueryRow(ctx, `
			SELECT `+instanceColumns+` FROM browser_instances b
			WHERE b.workspace_id = $1::uuid AND b.id = $2::uuid AND b.deleted_at IS NULL
		`, existing.WorkspaceID, existing.InstanceID))
		if instanceErr != nil {
			return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, instanceErr
		}
		if err := tx.Commit(ctx); err != nil {
			return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, err
		}
		return existing, instance, nil
	} else if !errors.Is(findErr, browserinstanceservice.ErrNotFound) {
		return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, findErr
	}
	instance, err := scanInstance(tx.QueryRow(ctx, `
		SELECT `+instanceColumns+` FROM browser_instances b
		WHERE b.workspace_id = $1::uuid AND b.id = $2::uuid AND b.deleted_at IS NULL
		FOR UPDATE OF b
	`, command.WorkspaceID, command.InstanceID))
	if err != nil {
		return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, err
	}
	if command.ExpectedVersion <= 0 || instance.Version != command.ExpectedVersion {
		return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, browserinstanceservice.ErrVersionConflict
	}
	payload, err := json.Marshal(command.Payload)
	if err != nil {
		return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO instance_commands (
			id, command_id, workspace_id, instance_id, device_id, action,
			idempotency_key, expected_version, status, payload, deadline,
			created_by, created_at, acknowledged_at, completed_at,
			error_code, failure_message
		) VALUES (
			$1::uuid, $1::uuid, $2::uuid, $3::uuid, NULLIF($4, '')::uuid, $5,
			$6, $7, $8, $9::jsonb, $10,
			NULLIF($11, '')::uuid, $12, $13, $14, $15, $16
		)
	`, command.ID, command.WorkspaceID, command.InstanceID, command.DeviceID,
		command.Action, command.IdempotencyKey, command.ExpectedVersion, command.Status,
		payload, command.Deadline, command.CreatedBy, command.CreatedAt,
		command.AcknowledgedAt, command.CompletedAt, command.FailureCode, command.FailureMessage)
	if err != nil {
		return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, err
	}
	instance, err = scanInstance(tx.QueryRow(ctx, `
		UPDATE browser_instances AS b
		SET desired_state = $3, version = version + 1, updated_at = $4
		WHERE b.workspace_id = $1::uuid AND b.id = $2::uuid
		RETURNING `+instanceColumns+`
	`, command.WorkspaceID, command.InstanceID, desiredState, command.CreatedAt))
	if err != nil {
		return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return browserinstanceservice.Command{}, browserinstanceservice.BrowserInstance{}, err
	}
	return command, instance, nil
}

func (s *Store) FindCommandByIdempotencyKey(ctx context.Context, workspaceID, idempotencyKey string) (browserinstanceservice.Command, error) {
	return scanCommand(s.pool.QueryRow(ctx, `
		SELECT `+commandColumns+` FROM instance_commands c
		WHERE c.workspace_id = $1::uuid AND c.idempotency_key = $2
	`, workspaceID, idempotencyKey))
}

func (s *Store) ExpireCommands(ctx context.Context, workspaceID, deviceID string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE instance_commands
		SET status='expired', completed_at=$3, error_code='command_expired',
		    failure_message='Command was not completed before its deadline'
		WHERE workspace_id=$1::uuid AND device_id=$2::uuid
		  AND status IN ('pending','queued','accepted','running')
		  AND deadline IS NOT NULL AND deadline <= $3
	`, workspaceID, deviceID, now)
	return err
}

func (s *Store) ListPendingCommands(ctx context.Context, workspaceID, deviceID string) ([]browserinstanceservice.Command, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+commandColumns+`
		FROM instance_commands c
		WHERE c.workspace_id = $1::uuid
		  AND c.device_id = $2::uuid
		  AND c.status IN ('pending', 'queued', 'accepted', 'running')
		  AND (c.deadline IS NULL OR c.deadline > now())
		ORDER BY c.created_at, c.id
		LIMIT 100
	`, workspaceID, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]browserinstanceservice.Command, 0)
	for rows.Next() {
		command, scanErr := scanCommand(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, command)
	}
	return items, rows.Err()
}

func (s *Store) TransitionCommand(
	ctx context.Context,
	workspaceID, deviceID, commandID, status, failureCode, failureMessage string,
	now time.Time,
) (browserinstanceservice.Command, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return browserinstanceservice.Command{}, err
	}
	defer rollback(ctx, tx)

	command, err := scanCommand(tx.QueryRow(ctx, `
		SELECT `+commandColumns+`
		FROM instance_commands c
		WHERE c.workspace_id = $1::uuid
		  AND c.device_id = $2::uuid
		  AND c.id = $3::uuid
		FOR UPDATE OF c
	`, workspaceID, deviceID, commandID))
	if err != nil {
		return browserinstanceservice.Command{}, err
	}
	if !browserinstanceservice.CanTransitionCommand(command.Status, status) {
		return browserinstanceservice.Command{}, browserinstanceservice.ErrInvalidCommandTransition
	}
	if command.Status == status {
		if err := tx.Commit(ctx); err != nil {
			return browserinstanceservice.Command{}, err
		}
		return command, nil
	}

	if _, err := tx.Exec(ctx, `
		SELECT 1 FROM browser_instances
		WHERE workspace_id = $1::uuid AND id = $2::uuid
		FOR UPDATE
	`, workspaceID, command.InstanceID); err != nil {
		return browserinstanceservice.Command{}, err
	}

	updated, err := scanCommand(tx.QueryRow(ctx, `
		UPDATE instance_commands AS c
		SET status = $4,
		    acknowledged_at = CASE
		        WHEN $4 IN ('accepted', 'running') THEN COALESCE(c.acknowledged_at, $5)
		        ELSE c.acknowledged_at
		    END,
		    completed_at = CASE WHEN $4 IN ('completed', 'failed') THEN $5 ELSE NULL END,
		    error_code = CASE WHEN $4 = 'failed' THEN $6 ELSE '' END,
		    failure_message = CASE WHEN $4 = 'failed' THEN $7 ELSE '' END
		WHERE c.workspace_id = $1::uuid AND c.device_id = $2::uuid AND c.id = $3::uuid
		RETURNING `+commandColumns+`
	`, workspaceID, deviceID, commandID, status, now, failureCode, failureMessage))
	if err != nil {
		return browserinstanceservice.Command{}, err
	}
	if status == "completed" && updated.Action == "instance.migrate" {
		tag, err := tx.Exec(ctx, `
			UPDATE browser_instances AS b
			SET assigned_device_id = (c.payload->>'targetDeviceId')::uuid,
			    desired_state = 'running', version = b.version + 1, updated_at = $3
			FROM instance_commands AS c, devices AS d
			WHERE c.id = $1::uuid AND b.workspace_id = $2::uuid
			  AND b.id = c.instance_id AND d.id = (c.payload->>'targetDeviceId')::uuid
			  AND d.workspace_id = b.workspace_id AND d.revoked_at IS NULL
			  AND b.deleted_at IS NULL AND b.desired_state = 'migrating'
			  AND b.assigned_device_id IS NOT DISTINCT FROM c.device_id
		`, updated.ID, workspaceID, now)
		if err != nil {
			return browserinstanceservice.Command{}, err
		}
		if tag.RowsAffected() != 1 {
			return browserinstanceservice.Command{}, browserinstanceservice.ErrStateConflict
		}
	}
	eventPayload, err := json.Marshal(map[string]interface{}{
		"commandId":      updated.ID,
		"action":         updated.Action,
		"status":         updated.Status,
		"failureCode":    updated.FailureCode,
		"failureMessage": updated.FailureMessage,
	})
	if err != nil {
		return browserinstanceservice.Command{}, err
	}
	if err := insertInstanceEvent(ctx, tx, workspaceID, updated.InstanceID, "command."+status, eventPayload, now); err != nil {
		return browserinstanceservice.Command{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return browserinstanceservice.Command{}, err
	}
	return updated, nil
}

func (s *Store) UpdateObservedState(
	ctx context.Context,
	workspaceID, deviceID, instanceID, state string,
	payload map[string]interface{},
	now time.Time,
) (browserinstanceservice.BrowserInstance, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return browserinstanceservice.BrowserInstance{}, err
	}
	defer rollback(ctx, tx)

	if payload == nil {
		payload = map[string]interface{}{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return browserinstanceservice.BrowserInstance{}, err
	}
	instance, err := scanInstance(tx.QueryRow(ctx, `
		UPDATE browser_instances AS b
		SET observed_state = $4,
		    observed_payload = $5::jsonb,
		    last_seen_at = $6,
		    updated_at = $6,
		    version = b.version + 1
		WHERE b.workspace_id = $1::uuid
		  AND b.assigned_device_id = $2::uuid
		  AND b.id = $3::uuid
		  AND b.deleted_at IS NULL
		RETURNING `+instanceColumns+`
	`, workspaceID, deviceID, instanceID, state, payloadJSON, now))
	if err != nil {
		return browserinstanceservice.BrowserInstance{}, err
	}
	eventPayload := make(map[string]interface{}, len(payload)+1)
	for key, value := range payload {
		eventPayload[key] = value
	}
	eventPayload["state"] = state
	eventJSON, err := json.Marshal(eventPayload)
	if err != nil {
		return browserinstanceservice.BrowserInstance{}, err
	}
	if err := insertInstanceEvent(ctx, tx, workspaceID, instanceID, "instance.observed_state", eventJSON, now); err != nil {
		return browserinstanceservice.BrowserInstance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return browserinstanceservice.BrowserInstance{}, err
	}
	return instance, nil
}

func insertInstanceEvent(ctx context.Context, tx pgx.Tx, workspaceID, instanceID, eventType string, payload []byte, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO instance_events (workspace_id, instance_id, sequence, event_type, payload, created_at)
		SELECT $1::uuid, $2::uuid, COALESCE(MAX(sequence), 0) + 1, $3, $4::jsonb, $5
		FROM instance_events
		WHERE workspace_id = $1::uuid AND instance_id = $2::uuid
	`, workspaceID, instanceID, eventType, payload, now)
	return err
}

func scanCommand(row scanner) (browserinstanceservice.Command, error) {
	var command browserinstanceservice.Command
	var payload []byte
	if err := row.Scan(
		&command.ID, &command.WorkspaceID, &command.InstanceID, &command.DeviceID,
		&command.Action, &command.IdempotencyKey, &command.ExpectedVersion,
		&command.Status, &payload, &command.Deadline, &command.CreatedBy,
		&command.CreatedAt, &command.AcknowledgedAt, &command.CompletedAt,
		&command.FailureCode, &command.FailureMessage,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return browserinstanceservice.Command{}, browserinstanceservice.ErrNotFound
		}
		return browserinstanceservice.Command{}, err
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &command.Payload); err != nil {
			return browserinstanceservice.Command{}, err
		}
	}
	if command.Payload == nil {
		command.Payload = map[string]interface{}{}
	}
	return command, nil
}

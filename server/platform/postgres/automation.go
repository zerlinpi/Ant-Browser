package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
)

const workflowColumns = `
	w.id::text, w.workspace_id::text, w.name, w.status, w.latest_version,
	COALESCE(w.published_version_id::text, ''), w.version,
	COALESCE(w.created_by::text, ''), w.created_at, w.updated_at, w.archived_at`

const workflowVersionColumns = `
	v.id::text, v.workspace_id::text, v.workflow_id::text, v.version,
	v.dsl_schema_version, v.definition, v.content_hash,
	COALESCE(v.created_by::text, ''), v.created_at`

func (s *Store) CreateWorkflow(ctx context.Context, workflow automationservice.Workflow, version automationservice.WorkflowVersion) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	_, err = tx.Exec(ctx, `
		INSERT INTO workflows (
			id, workspace_id, name, status, latest_version, published_version_id,
			version, created_by, created_at, updated_at, archived_at
		) VALUES (
			$1::uuid, $2::uuid, $3, $4, $5, NULLIF($6, '')::uuid,
			$7, NULLIF($8, '')::uuid, $9, $10, $11
		)
	`, workflow.ID, workflow.WorkspaceID, workflow.Name, workflow.Status,
		workflow.LatestVersion, workflow.PublishedVersionID, workflow.Version,
		workflow.CreatedBy, workflow.CreatedAt, workflow.UpdatedAt, workflow.ArchivedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return errors.New("workflow name already exists")
		}
		if isForeignKeyViolation(err) {
			return automationservice.ErrNotFound
		}
		return err
	}
	if err := insertWorkflowVersion(ctx, tx, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FindWorkflow(ctx context.Context, workspaceID, workflowID string) (automationservice.Workflow, error) {
	return scanWorkflow(s.pool.QueryRow(ctx, `
		SELECT `+workflowColumns+` FROM workflows w
		WHERE w.workspace_id = $1::uuid AND w.id = $2::uuid
	`, workspaceID, workflowID))
}

func (s *Store) ListWorkflows(ctx context.Context, workspaceID string) ([]automationservice.Workflow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+workflowColumns+` FROM workflows w
		WHERE w.workspace_id = $1::uuid
		ORDER BY w.updated_at DESC, w.id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]automationservice.Workflow, 0)
	for rows.Next() {
		workflow, scanErr := scanWorkflow(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, workflow)
	}
	return items, rows.Err()
}

func (s *Store) AddWorkflowVersion(ctx context.Context, version automationservice.WorkflowVersion, expectedVersion int64, now time.Time) (automationservice.Workflow, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return automationservice.Workflow{}, err
	}
	defer rollback(ctx, tx)
	workflow, err := scanWorkflow(tx.QueryRow(ctx, `
		SELECT `+workflowColumns+` FROM workflows w
		WHERE w.workspace_id = $1::uuid AND w.id = $2::uuid
		FOR UPDATE OF w
	`, version.WorkspaceID, version.WorkflowID))
	if err != nil {
		return automationservice.Workflow{}, err
	}
	if workflow.Version != expectedVersion {
		return automationservice.Workflow{}, automationservice.ErrVersionConflict
	}
	if workflow.Status == "archived" {
		return automationservice.Workflow{}, automationservice.ErrStateConflict
	}
	if workflow.LatestVersion+1 != version.Version {
		return automationservice.Workflow{}, automationservice.ErrVersionConflict
	}
	if err := insertWorkflowVersion(ctx, tx, version); err != nil {
		return automationservice.Workflow{}, err
	}
	workflow, err = scanWorkflow(tx.QueryRow(ctx, `
		UPDATE workflows AS w
		SET latest_version = $3, version = w.version + 1, updated_at = $4
		WHERE w.workspace_id = $1::uuid AND w.id = $2::uuid AND w.version = $5
		RETURNING `+workflowColumns+`
	`, version.WorkspaceID, version.WorkflowID, version.Version, now, expectedVersion))
	if err != nil {
		if errors.Is(err, automationservice.ErrNotFound) {
			return automationservice.Workflow{}, automationservice.ErrVersionConflict
		}
		return automationservice.Workflow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return automationservice.Workflow{}, err
	}
	return workflow, nil
}

func (s *Store) FindWorkflowVersion(ctx context.Context, workspaceID, workflowID string, version int) (automationservice.WorkflowVersion, error) {
	return scanWorkflowVersion(s.pool.QueryRow(ctx, `
		SELECT `+workflowVersionColumns+` FROM workflow_versions v
		WHERE v.workspace_id = $1::uuid AND v.workflow_id = $2::uuid AND v.version = $3
	`, workspaceID, workflowID, version))
}

func (s *Store) FindWorkflowVersionByID(ctx context.Context, workspaceID, workflowID, versionID string) (automationservice.WorkflowVersion, error) {
	return scanWorkflowVersion(s.pool.QueryRow(ctx, `SELECT `+workflowVersionColumns+` FROM workflow_versions v
	 WHERE v.workspace_id=$1::uuid AND v.workflow_id=$2::uuid AND v.id=$3::uuid`, workspaceID, workflowID, versionID))
}

func (s *Store) TransitionWorkflow(ctx context.Context, workspaceID, workflowID, status string, workflowVersion int, expectedVersion int64, now time.Time) (automationservice.Workflow, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return automationservice.Workflow{}, err
	}
	defer rollback(ctx, tx)
	workflow, err := scanWorkflow(tx.QueryRow(ctx, `
		SELECT `+workflowColumns+` FROM workflows w
		WHERE w.workspace_id = $1::uuid AND w.id = $2::uuid
		FOR UPDATE OF w
	`, workspaceID, workflowID))
	if err != nil {
		return automationservice.Workflow{}, err
	}
	if workflow.Version != expectedVersion {
		return automationservice.Workflow{}, automationservice.ErrVersionConflict
	}
	if workflow.Status == "archived" {
		return automationservice.Workflow{}, automationservice.ErrStateConflict
	}
	publishedID := workflow.PublishedVersionID
	var archivedAt *time.Time
	switch status {
	case "published":
		selected, findErr := scanWorkflowVersion(tx.QueryRow(ctx, `
			SELECT `+workflowVersionColumns+` FROM workflow_versions v
			WHERE v.workspace_id = $1::uuid AND v.workflow_id = $2::uuid AND v.version = $3
		`, workspaceID, workflowID, workflowVersion))
		if findErr != nil {
			return automationservice.Workflow{}, findErr
		}
		publishedID = selected.ID
	case "archived":
		archivedAt = &now
	default:
		return automationservice.Workflow{}, automationservice.ErrStateConflict
	}
	workflow, err = scanWorkflow(tx.QueryRow(ctx, `
		UPDATE workflows AS w
		SET status = $3, published_version_id = NULLIF($4, '')::uuid,
		    archived_at = $5, updated_at = $6, version = w.version + 1
		WHERE w.workspace_id = $1::uuid AND w.id = $2::uuid AND w.version = $7
		RETURNING `+workflowColumns+`
	`, workspaceID, workflowID, status, publishedID, archivedAt, now, expectedVersion))
	if err != nil {
		if errors.Is(err, automationservice.ErrNotFound) {
			return automationservice.Workflow{}, automationservice.ErrVersionConflict
		}
		return automationservice.Workflow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return automationservice.Workflow{}, err
	}
	return workflow, nil
}

func insertWorkflowVersion(ctx context.Context, tx pgx.Tx, version automationservice.WorkflowVersion) error {
	definition, err := json.Marshal(version.Definition)
	if err != nil {
		return err
	}
	contentHash, err := hex.DecodeString(version.ContentHash)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO workflow_versions (
			id, workspace_id, workflow_id, version, dsl_schema_version,
			definition, content_hash, created_by, created_at
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4, $5,
			$6::jsonb, $7, NULLIF($8, '')::uuid, $9
		)
	`, version.ID, version.WorkspaceID, version.WorkflowID, version.Version,
		version.SchemaVersion, definition, contentHash, version.CreatedBy, version.CreatedAt)
	if isUniqueViolation(err) {
		return automationservice.ErrVersionConflict
	}
	if isForeignKeyViolation(err) {
		return automationservice.ErrNotFound
	}
	return err
}

func scanWorkflow(row scanner) (automationservice.Workflow, error) {
	var workflow automationservice.Workflow
	if err := row.Scan(
		&workflow.ID, &workflow.WorkspaceID, &workflow.Name, &workflow.Status,
		&workflow.LatestVersion, &workflow.PublishedVersionID, &workflow.Version,
		&workflow.CreatedBy, &workflow.CreatedAt, &workflow.UpdatedAt, &workflow.ArchivedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return automationservice.Workflow{}, automationservice.ErrNotFound
		}
		return automationservice.Workflow{}, err
	}
	return workflow, nil
}

func scanWorkflowVersion(row scanner) (automationservice.WorkflowVersion, error) {
	var version automationservice.WorkflowVersion
	var definition []byte
	var contentHash []byte
	if err := row.Scan(
		&version.ID, &version.WorkspaceID, &version.WorkflowID, &version.Version,
		&version.SchemaVersion, &definition, &contentHash, &version.CreatedBy, &version.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return automationservice.WorkflowVersion{}, automationservice.ErrNotFound
		}
		return automationservice.WorkflowVersion{}, err
	}
	if err := json.Unmarshal(definition, &version.Definition); err != nil {
		return automationservice.WorkflowVersion{}, err
	}
	version.ContentHash = hex.EncodeToString(contentHash)
	return version, nil
}

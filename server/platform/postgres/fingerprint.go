package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	fingerprintservice "github.com/zerlinpi/Ant-Browser/server/services/fingerprint-service"
)

const fingerprintColumns = `
	f.id::text, f.workspace_id::text, f.name, f.mode, f.browser_family,
	f.browser_major, f.platform, f.seed, f.locale, f.timezone,
	f.runtime_args, f.configuration, f.version, COALESCE(f.created_by::text, ''),
	f.created_at, f.updated_at, f.deleted_at`

func (s *Store) CreateFingerprintTemplate(ctx context.Context, template fingerprintservice.Template) error {
	return s.CreateFingerprintTemplates(ctx, []fingerprintservice.Template{template})
}

func (s *Store) CreateFingerprintTemplates(ctx context.Context, templates []fingerprintservice.Template) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	for _, template := range templates {
		runtimeArgs, configuration, encodeErr := encodeFingerprintTemplate(template)
		if encodeErr != nil {
			return encodeErr
		}
		_, insertErr := tx.Exec(ctx, `
			INSERT INTO fingerprint_templates (
				id, workspace_id, name, mode, browser_family, browser_major,
				platform, seed, locale, timezone, runtime_args, configuration,
				version, created_by, created_at, updated_at, deleted_at
			) VALUES (
				$1::uuid, $2::uuid, $3, $4, $5, $6,
				$7, $8, $9, $10, $11::jsonb, $12::jsonb,
				$13, NULLIF($14, '')::uuid, $15, $16, $17
			)
		`, template.ID, template.WorkspaceID, template.Name, template.Mode,
			template.BrowserFamily, template.BrowserMajor, template.Platform,
			template.Seed, template.Locale, template.Timezone, runtimeArgs, configuration,
			template.Version, template.CreatedBy, template.CreatedAt, template.UpdatedAt, template.DeletedAt)
		if isUniqueViolation(insertErr) {
			return fingerprintservice.ErrNameConflict
		}
		if isForeignKeyViolation(insertErr) {
			return fingerprintservice.ErrNotFound
		}
		if insertErr != nil {
			return insertErr
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) FindFingerprintTemplate(ctx context.Context, workspaceID, templateID string) (fingerprintservice.Template, error) {
	return scanFingerprintTemplate(s.pool.QueryRow(ctx, `
		SELECT `+fingerprintColumns+` FROM fingerprint_templates f
		WHERE f.workspace_id = $1::uuid AND f.id = $2::uuid AND f.deleted_at IS NULL
	`, workspaceID, templateID))
}

func (s *Store) ListFingerprintTemplates(ctx context.Context, workspaceID string) ([]fingerprintservice.Template, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+fingerprintColumns+` FROM fingerprint_templates f
		WHERE f.workspace_id = $1::uuid AND f.deleted_at IS NULL
		ORDER BY f.created_at, f.id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]fingerprintservice.Template, 0)
	for rows.Next() {
		template, scanErr := scanFingerprintTemplate(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, template)
	}
	return items, rows.Err()
}

func (s *Store) UpdateFingerprintTemplate(ctx context.Context, template fingerprintservice.Template, expectedVersion int64) (fingerprintservice.Template, error) {
	runtimeArgs, configuration, err := encodeFingerprintTemplate(template)
	if err != nil {
		return fingerprintservice.Template{}, err
	}
	updated, err := scanFingerprintTemplate(s.pool.QueryRow(ctx, `
		UPDATE fingerprint_templates AS f
		SET name = $4, mode = $5, browser_family = $6, browser_major = $7,
		    platform = $8, seed = $9, locale = $10, timezone = $11,
		    runtime_args = $12::jsonb, configuration = $13::jsonb,
		    updated_at = $14, version = f.version + 1
		WHERE f.workspace_id = $1::uuid AND f.id = $2::uuid
		  AND f.version = $3 AND f.deleted_at IS NULL
		RETURNING `+fingerprintColumns+`
	`, template.WorkspaceID, template.ID, expectedVersion, template.Name, template.Mode,
		template.BrowserFamily, template.BrowserMajor, template.Platform, template.Seed,
		template.Locale, template.Timezone, runtimeArgs, configuration, template.UpdatedAt))
	if isUniqueViolation(err) {
		return fingerprintservice.Template{}, fingerprintservice.ErrNameConflict
	}
	if errors.Is(err, fingerprintservice.ErrNotFound) {
		if _, findErr := s.FindFingerprintTemplate(ctx, template.WorkspaceID, template.ID); findErr == nil {
			return fingerprintservice.Template{}, fingerprintservice.ErrVersionConflict
		}
	}
	return updated, err
}

func (s *Store) DeleteFingerprintTemplate(ctx context.Context, workspaceID, templateID string, expectedVersion int64, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	template, err := scanFingerprintTemplate(tx.QueryRow(ctx, `
		SELECT `+fingerprintColumns+` FROM fingerprint_templates f
		WHERE f.workspace_id = $1::uuid AND f.id = $2::uuid AND f.deleted_at IS NULL
		FOR UPDATE OF f
	`, workspaceID, templateID))
	if err != nil {
		return err
	}
	if expectedVersion <= 0 || template.Version != expectedVersion {
		return fingerprintservice.ErrVersionConflict
	}
	var inUse bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM browser_instances
			WHERE workspace_id = $1::uuid AND fingerprint_template_id = $2::uuid AND deleted_at IS NULL
			UNION ALL
			SELECT 1 FROM browser_profiles
			WHERE workspace_id = $1::uuid AND fingerprint_template_id = $2::uuid AND deleted_at IS NULL
		)
	`, workspaceID, templateID).Scan(&inUse); err != nil {
		return err
	}
	if inUse {
		return fingerprintservice.ErrInUse
	}
	_, err = tx.Exec(ctx, `
		UPDATE fingerprint_templates
		SET deleted_at = $3, updated_at = $3, version = version + 1
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, workspaceID, templateID, now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func encodeFingerprintTemplate(template fingerprintservice.Template) ([]byte, []byte, error) {
	runtimeArgs, err := json.Marshal(template.RuntimeArgs)
	if err != nil {
		return nil, nil, err
	}
	configuration, err := json.Marshal(template.Configuration)
	if err != nil {
		return nil, nil, err
	}
	return runtimeArgs, configuration, nil
}

func scanFingerprintTemplate(row scanner) (fingerprintservice.Template, error) {
	var template fingerprintservice.Template
	var runtimeArgs []byte
	var configuration []byte
	if err := row.Scan(
		&template.ID, &template.WorkspaceID, &template.Name, &template.Mode,
		&template.BrowserFamily, &template.BrowserMajor, &template.Platform,
		&template.Seed, &template.Locale, &template.Timezone,
		&runtimeArgs, &configuration, &template.Version, &template.CreatedBy,
		&template.CreatedAt, &template.UpdatedAt, &template.DeletedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fingerprintservice.Template{}, fingerprintservice.ErrNotFound
		}
		return fingerprintservice.Template{}, err
	}
	if err := json.Unmarshal(runtimeArgs, &template.RuntimeArgs); err != nil {
		return fingerprintservice.Template{}, err
	}
	if err := json.Unmarshal(configuration, &template.Configuration); err != nil {
		return fingerprintservice.Template{}, err
	}
	if template.RuntimeArgs == nil {
		template.RuntimeArgs = []string{}
	}
	return template, nil
}

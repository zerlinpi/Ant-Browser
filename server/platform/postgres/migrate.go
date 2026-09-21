package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

var migrationName = regexp.MustCompile(`^[0-9]{3}_[a-z0-9_]+\.sql$`)

type migrationFile struct {
	Version  string
	Path     string
	SQL      string
	Checksum string
}

// Migrate applies immutable forward-only migrations under a session advisory
// lock. A checksum mismatch stops startup instead of rewriting history.
func Migrate(ctx context.Context, databaseURL, migrationsPath string) error {
	migrations, err := loadMigrations(migrationsPath)
	if err != nil {
		return err
	}
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect for migrations: %w", err)
	}
	defer connection.Close(ctx)
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock(hashtext('ant-browser-schema-migrations'))`); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = connection.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('ant-browser-schema-migrations'))`)
	}()
	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("create migration metadata: %w", err)
	}
	for _, migration := range migrations {
		var checksum string
		err := connection.QueryRow(ctx, `
			SELECT checksum FROM schema_migrations WHERE version = $1
		`, migration.Version).Scan(&checksum)
		switch {
		case err == nil:
			if checksum != migration.Checksum {
				return fmt.Errorf("migration %s checksum changed", migration.Version)
			}
			continue
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("read migration %s: %w", migration.Version, err)
		}
		tx, err := connection.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", migration.Version, err)
		}
		if _, err := tx.Exec(ctx, migration.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", migration.Version, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)
		`, migration.Version, migration.Checksum); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", migration.Version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", migration.Version, err)
		}
	}
	return nil
}

func loadMigrations(root string) ([]migrationFile, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("migrations path is required")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}
	files := make([]migrationFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !migrationName.MatchString(entry.Name()) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(contents)
		files = append(files, migrationFile{
			Version: entry.Name(), Path: path, SQL: string(contents),
			Checksum: hex.EncodeToString(digest[:]),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Version < files[j].Version })
	if len(files) == 0 {
		return nil, fmt.Errorf("no migrations found in %s", root)
	}
	return files, nil
}

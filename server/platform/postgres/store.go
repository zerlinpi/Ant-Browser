package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *tenantPool
}

type scanner interface {
	Scan(...interface{}) error
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	config.MaxConns = 20
	config.MinConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	store := &Store{pool: newTenantPool(pool)}
	if err := store.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return store, nil
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) Close() error {
	s.pool.Close()
	return nil
}

func isUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}

// Case-insensitive name indexes: live rows from migration 027, and every
// workflow, archived included, from migration 031. A violation of one of them
// is a user-facing name conflict; any other unique violation is not.
const (
	instanceNameIndex      = "browser_instances_live_name_uq"
	proxyNameIndex         = "proxies_live_name_uq"
	accountIdentifierIndex = "accounts_live_identifier_uq"
	profileNameIndex       = "browser_profiles_live_name_uq"
	workflowNameIndex      = "workflows_lower_name_uq"
)

// isUniqueViolationOn reports a unique violation of the named constraint or
// unique index (PostgreSQL reports the index name for index violations).
func isUniqueViolationOn(err error, name string) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505" && pgError.ConstraintName == name
}

func isForeignKeyViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23503"
}

func isBillingQuotaViolation(err error) bool {
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "P0001" {
		return false
	}
	return pgError.ConstraintName == "billing_instances_quota" ||
		pgError.ConstraintName == "billing_team_members_quota" ||
		pgError.ConstraintName == "billing_automation_runs_quota" ||
		pgError.ConstraintName == "billing_api_calls_quota"
}

func rollback(ctx context.Context, tx pgx.Tx) {
	_ = tx.Rollback(ctx)
}

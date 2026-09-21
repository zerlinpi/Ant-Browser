package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TenantScope identifies the tenant context required by PostgreSQL RLS.
// Either scope may be supplied, but at least one must be present. Workspace
// scoped operations should set WorkspaceID; organization scoped operations
// should set OrganizationID.
type TenantScope struct {
	WorkspaceID    string
	OrganizationID string
	UserID         string
	// SystemOperation is only valid for an explicitly constrained worker path.
	// It is never accepted from HTTP request input.
	SystemOperation string
	validationError string
}

type tenantContextKey struct{}

// WithTenantScope attaches the scope used by Store's pool facade. The scope is
// request-local; database settings are still transaction-local and never put
// on a pooled connection outside the transaction used for the operation.
func WithTenantScope(ctx context.Context, scope TenantScope) context.Context {
	if current, ok := tenantScopeFromContext(ctx); ok {
		scope = mergeTenantScopes(current, scope)
	}
	return context.WithValue(ctx, tenantContextKey{}, scope)
}

// WithWorkerScope marks a worker operation that has an explicit SQL policy.
// Worker code must use this only for the named operation and never pass a
// caller-controlled value here.
func WithWorkerScope(ctx context.Context, operation string) context.Context {
	return WithTenantScope(ctx, TenantScope{SystemOperation: strings.TrimSpace(operation)})
}

func tenantScopeFromContext(ctx context.Context) (TenantScope, bool) {
	scope, ok := ctx.Value(tenantContextKey{}).(TenantScope)
	return scope, ok
}

func mergeTenantScopes(current, next TenantScope) TenantScope {
	merged := current
	merge := func(name string, destination *string, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if strings.TrimSpace(*destination) != "" && !strings.EqualFold(strings.TrimSpace(*destination), value) {
			merged.validationError = "conflicting " + name + " tenant scopes"
			return
		}
		*destination = value
	}
	merge("workspace", &merged.WorkspaceID, next.WorkspaceID)
	merge("organization", &merged.OrganizationID, next.OrganizationID)
	merge("user", &merged.UserID, next.UserID)
	merge("system operation", &merged.SystemOperation, next.SystemOperation)
	if next.validationError != "" {
		merged.validationError = next.validationError
	}
	return merged
}

// tenantPool makes the safe transaction boundary the default for every
// operation performed by Store. Existing repository methods can therefore use
// Query/QueryRow/Exec without accidentally setting a session-wide tenant.
type tenantPool struct{ pool *pgxpool.Pool }

func newTenantPool(pool *pgxpool.Pool) *tenantPool { return &tenantPool{pool: pool} }

func (p *tenantPool) Begin(ctx context.Context) (pgx.Tx, error) {
	return p.begin(ctx, pgx.TxOptions{})
}

func (p *tenantPool) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	return p.begin(ctx, options)
}

func (p *tenantPool) begin(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if scope, ok := tenantScopeFromContext(ctx); ok {
		if err := validateTenantScope(scope); err != nil {
			return nil, err
		}
	}
	tx, err := p.pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	if scope, ok := tenantScopeFromContext(ctx); ok {
		if err := setTenantContext(ctx, tx, scope); err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}
	}
	return tx, nil
}

func (p *tenantPool) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if _, ok := tenantScopeFromContext(ctx); !ok {
		return p.pool.Exec(ctx, sql, args...)
	}
	tx, err := p.begin(ctx, pgx.TxOptions{})
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		_ = tx.Rollback(ctx)
		return tag, err
	}
	if err := tx.Commit(ctx); err != nil {
		return tag, err
	}
	return tag, nil
}

func (p *tenantPool) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	if _, ok := tenantScopeFromContext(ctx); !ok {
		return p.pool.QueryRow(ctx, sql, args...)
	}
	tx, err := p.begin(ctx, pgx.TxOptions{})
	if err != nil {
		return errorRow{err: err}
	}
	return &tenantRow{row: tx.QueryRow(ctx, sql, args...), tx: tx, ctx: ctx}
}

func (p *tenantPool) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	if _, ok := tenantScopeFromContext(ctx); !ok {
		return p.pool.Query(ctx, sql, args...)
	}
	tx, err := p.begin(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return &tenantRows{Rows: rows, tx: tx, ctx: ctx}, nil
}

func (p *tenantPool) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
func (p *tenantPool) Close()                         { p.pool.Close() }

type errorRow struct{ err error }

func (r errorRow) Scan(...interface{}) error { return r.err }

type tenantRow struct {
	row pgx.Row
	tx  pgx.Tx
	ctx context.Context
}

func (r *tenantRow) Scan(dest ...interface{}) error {
	err := r.row.Scan(dest...)
	if err != nil {
		_ = r.tx.Rollback(r.ctx)
		return err
	}
	return r.tx.Commit(r.ctx)
}

type tenantRows struct {
	pgx.Rows
	tx        pgx.Tx
	ctx       context.Context
	closed    bool
	finishErr error
}

func (r *tenantRows) Close() {
	r.finish(false)
}

func (r *tenantRows) Next() bool {
	if r.closed {
		return false
	}
	if r.Rows.Next() {
		return true
	}
	r.finish(false)
	return false
}

func (r *tenantRows) Scan(dest ...interface{}) error {
	if err := r.Rows.Scan(dest...); err != nil {
		r.finishErr = err
		r.finish(true)
		return err
	}
	return nil
}

func (r *tenantRows) Values() ([]interface{}, error) {
	values, err := r.Rows.Values()
	if err != nil {
		r.finishErr = err
		r.finish(true)
	}
	return values, err
}

func (r *tenantRows) Err() error {
	if r.finishErr != nil {
		return r.finishErr
	}
	return r.Rows.Err()
}

func (r *tenantRows) finish(forceRollback bool) {
	if r.closed {
		return
	}
	r.closed = true
	r.Rows.Close()
	if forceRollback || r.Rows.Err() != nil {
		if r.finishErr == nil {
			r.finishErr = r.Rows.Err()
		}
		_ = r.tx.Rollback(r.ctx)
		return
	}
	if err := r.tx.Commit(r.ctx); err != nil {
		r.finishErr = err
	}
}

// WithTenant runs fn in a transaction with RLS tenant settings scoped to that
// transaction. The callback must use tx for every database operation that
// relies on the settings. set_config(..., true) is transaction-local, so a
// pooled connection cannot retain another request's tenant identity.
func (s *Store) WithTenant(ctx context.Context, scope TenantScope, fn func(context.Context, pgx.Tx) error) error {
	if current, ok := tenantScopeFromContext(ctx); ok {
		scope = mergeTenantScopes(current, scope)
	}
	if err := validateTenantScope(scope); err != nil {
		return err
	}
	if fn == nil {
		return errors.New("tenant transaction callback is required")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)

	if err := setTenantContext(ctx, tx, scope); err != nil {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// setTenantContext is deliberately unexported so callers cannot set session
// state on a pooled connection outside WithTenant's transaction boundary.
func setTenantContext(ctx context.Context, tx pgx.Tx, scope TenantScope) error {
	_, err := tx.Exec(ctx, `
		SELECT set_config('app.current_workspace_id', $1, true),
		       set_config('app.current_organization_id', $2, true),
		       set_config('app.current_user_id', $3, true),
		       set_config('app.system_operation', $4, true)
	`, strings.TrimSpace(scope.WorkspaceID), strings.TrimSpace(scope.OrganizationID), strings.TrimSpace(scope.UserID), strings.TrimSpace(scope.SystemOperation))
	return err
}

func validateTenantScope(scope TenantScope) error {
	if scope.validationError != "" {
		return errors.New(scope.validationError)
	}
	workspaceID := strings.TrimSpace(scope.WorkspaceID)
	organizationID := strings.TrimSpace(scope.OrganizationID)
	userID := strings.TrimSpace(scope.UserID)
	operation := strings.TrimSpace(scope.SystemOperation)
	if workspaceID == "" && organizationID == "" && userID == "" && operation == "" {
		return errors.New("workspace, organization, user, or system tenant scope is required")
	}
	if workspaceID != "" {
		if _, err := uuid.Parse(workspaceID); err != nil {
			return errors.New("workspace tenant scope must be a UUID")
		}
	}
	if organizationID != "" {
		if _, err := uuid.Parse(organizationID); err != nil {
			return errors.New("organization tenant scope must be a UUID")
		}
	}
	if userID != "" {
		if _, err := uuid.Parse(userID); err != nil {
			return errors.New("tenant user scope must be a UUID")
		}
	}
	if operation != "" {
		switch operation {
		case "task_claim", "schedule_dispatch":
			if workspaceID != "" || organizationID != "" || userID != "" {
				return errors.New("worker system operation cannot be combined with user or tenant identity")
			}
		case "admin_read", "admin_mutation", "admin_audit":
			// Admin operations remain bound to the authenticated actor. They may
			// cross tenants, but cannot be combined with a caller-selected tenant
			// scope, and their fixed names are never accepted from HTTP input.
			if userID == "" {
				return errors.New("admin system operation requires an authenticated user")
			}
			if workspaceID != "" || organizationID != "" {
				return errors.New("admin system operation cannot be combined with tenant identity")
			}
		default:
			return errors.New("system tenant operation is not allowed")
		}
	}
	return nil
}

func (s *Store) withWorkspaceTx(ctx context.Context, workspaceID string, fn func(context.Context, pgx.Tx) error) error {
	return s.WithTenant(ctx, TenantScope{WorkspaceID: workspaceID}, fn)
}

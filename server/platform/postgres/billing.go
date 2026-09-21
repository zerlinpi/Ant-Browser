package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	billingservice "github.com/zerlinpi/Ant-Browser/server/services/billing-service"
)

func (s *Store) ListPlans(ctx context.Context) ([]billingservice.Plan, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id::text,p.code,p.name,p.currency,p.amount_minor,p.billing_interval,p.active,p.created_at,p.updated_at,
		       pe.plan_id::text,pe.entitlement_code,pe.limit_value,pe.feature_enabled,pe.metadata
		FROM plans p
		LEFT JOIN plan_entitlements pe ON pe.plan_id=p.id
		WHERE p.active=true
		ORDER BY CASE p.code WHEN 'free' THEN 1 WHEN 'professional' THEN 2 WHEN 'enterprise' THEN 3 ELSE 4 END,
		         p.code,pe.entitlement_code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]billingservice.Plan, 0)
	planIndex := make(map[string]int)
	for rows.Next() {
		var p billingservice.Plan
		var entPlanID *string
		var entCode *string
		var entLimit *int64
		var entEnabled *bool
		var metadata []byte
		if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.Currency, &p.AmountMinor, &p.BillingInterval, &p.Active, &p.CreatedAt, &p.UpdatedAt, &entPlanID, &entCode, &entLimit, &entEnabled, &metadata); err != nil {
			return nil, err
		}
		idx, exists := planIndex[p.ID]
		if !exists {
			idx = len(out)
			planIndex[p.ID] = idx
			out = append(out, p)
		}
		if entCode != nil {
			out[idx].Entitlements = append(out[idx].Entitlements, billingservice.PlanEntitlement{PlanID: *entPlanID, Code: *entCode, Limit: entLimit, FeatureEnabled: entEnabled != nil && *entEnabled, Metadata: decodeStringMap(metadata)})
		}
	}
	return out, rows.Err()
}
func (s *Store) FindPlanByCode(ctx context.Context, code string) (billingservice.Plan, error) {
	var p billingservice.Plan
	err := s.pool.QueryRow(ctx, `SELECT id::text,code,name,currency,amount_minor,billing_interval,active,created_at,updated_at FROM plans WHERE code=$1 AND active=true`, strings.ToLower(strings.TrimSpace(code))).Scan(&p.ID, &p.Code, &p.Name, &p.Currency, &p.AmountMinor, &p.BillingInterval, &p.Active, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, billingservice.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.Entitlements, err = s.listPlanEntitlements(ctx, p.ID)
	return p, err
}
func (s *Store) listPlanEntitlements(ctx context.Context, planID string) ([]billingservice.PlanEntitlement, error) {
	rows, err := s.pool.Query(ctx, `SELECT plan_id::text,entitlement_code,limit_value,feature_enabled,metadata FROM plan_entitlements WHERE plan_id=$1::uuid ORDER BY entitlement_code`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]billingservice.PlanEntitlement, 0)
	for rows.Next() {
		var e billingservice.PlanEntitlement
		var metadata []byte
		if err := rows.Scan(&e.PlanID, &e.Code, &e.Limit, &e.FeatureEnabled, &metadata); err != nil {
			return nil, err
		}
		e.Metadata = decodeStringMap(metadata)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) FindCurrentSubscription(ctx context.Context, org string) (billingservice.Subscription, error) {
	ctx = WithTenantScope(ctx, TenantScope{OrganizationID: strings.TrimSpace(org)})
	var sub billingservice.Subscription
	err := s.pool.QueryRow(ctx, `SELECT id::text,organization_id::text,plan_id::text,status,provider,provider_subscription_ref,current_period_start,current_period_end,cancel_at_period_end,created_at,updated_at FROM subscriptions WHERE organization_id=$1::uuid AND status IN ('trialing','active','past_due') ORDER BY updated_at DESC LIMIT 1`, org).Scan(&sub.ID, &sub.OrganizationID, &sub.PlanID, &sub.Status, &sub.Provider, &sub.ProviderSubscriptionRef, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.CancelAtPeriodEnd, &sub.CreatedAt, &sub.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return sub, billingservice.ErrNotFound
	}
	return sub, err
}
func (s *Store) ListEntitlements(ctx context.Context, org string) ([]billingservice.Entitlement, error) {
	ctx = WithTenantScope(ctx, TenantScope{OrganizationID: strings.TrimSpace(org)})
	rows, err := s.pool.Query(ctx, `SELECT id::text,organization_id::text,entitlement_code,COALESCE(source_subscription_id::text,''),value_limit,feature_enabled,consumed_value,valid_from,valid_until,metadata FROM entitlements WHERE organization_id=$1::uuid AND valid_from<=now() AND (valid_until IS NULL OR valid_until>now()) ORDER BY entitlement_code`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]billingservice.Entitlement, 0)
	for rows.Next() {
		e, err := scanEntitlement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) FindEntitlement(ctx context.Context, org, code string) (billingservice.Entitlement, error) {
	ctx = WithTenantScope(ctx, TenantScope{OrganizationID: strings.TrimSpace(org)})
	e, err := scanEntitlement(s.pool.QueryRow(ctx, `SELECT id::text,organization_id::text,entitlement_code,COALESCE(source_subscription_id::text,''),value_limit,feature_enabled,consumed_value,valid_from,valid_until,metadata FROM entitlements WHERE organization_id=$1::uuid AND entitlement_code=$2 AND valid_from<=now() AND (valid_until IS NULL OR valid_until>now())`, org, strings.ToLower(strings.TrimSpace(code))))
	if errors.Is(err, pgx.ErrNoRows) {
		return e, billingservice.ErrNotFound
	}
	return e, err
}
func scanEntitlement(row scanner) (billingservice.Entitlement, error) {
	var e billingservice.Entitlement
	var metadata []byte
	err := row.Scan(&e.ID, &e.OrganizationID, &e.Code, &e.SourceSubscriptionID, &e.Limit, &e.FeatureEnabled, &e.Consumed, &e.ValidFrom, &e.ValidUntil, &metadata)
	e.Metadata = decodeStringMap(metadata)
	return e, err
}

func (s *Store) ReserveUsage(ctx context.Context, input billingservice.ReserveUsageInput, limit *int64) (billingservice.UsageReservation, error) {
	ctx = WithTenantScope(ctx, TenantScope{OrganizationID: strings.TrimSpace(input.OrganizationID)})
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return billingservice.UsageReservation{}, err
	}
	defer rollback(ctx, tx)
	var existing billingservice.UsageReservation
	err = scanReservation(tx.QueryRow(ctx, `SELECT id::text,organization_id::text,COALESCE(workspace_id::text,''),metric_code,amount,status,idempotency_key,expires_at,created_at,date_trunc('month',created_at),date_trunc('month',created_at)+interval '1 month' FROM usage_reservations WHERE organization_id=$1::uuid AND idempotency_key=$2 FOR UPDATE`, input.OrganizationID, input.IdempotencyKey), &existing)
	if err == nil {
		if existing.WorkspaceID != input.WorkspaceID || existing.MetricCode != input.MetricCode || existing.Amount != input.Amount {
			return billingservice.UsageReservation{}, billingservice.ErrIdempotencyConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return billingservice.UsageReservation{}, err
	}
	start := time.Date(input.Now.UTC().Year(), input.Now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	var counterID string
	var limitValue interface{}
	if limit != nil {
		limitValue = *limit
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO usage_counters(organization_id,workspace_id,metric_code,period_start,period_end,consumed_value,reserved_value,version)
		SELECT $1::uuid,NULL,$2,$3,$4,0,$5,1
		WHERE $6::bigint IS NULL OR $5 <= $6
		ON CONFLICT (organization_id,metric_code,period_start) WHERE workspace_id IS NULL
		DO UPDATE SET reserved_value=usage_counters.reserved_value+$5,version=usage_counters.version+1
		WHERE $6::bigint IS NULL OR usage_counters.consumed_value+usage_counters.reserved_value+$5 <= $6
		RETURNING id::text
	`, input.OrganizationID, input.MetricCode, start, end, input.Amount, limitValue).Scan(&counterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return billingservice.UsageReservation{}, billingservice.ErrQuotaExceeded
	}
	if err != nil {
		return billingservice.UsageReservation{}, err
	}
	var r billingservice.UsageReservation
	r.ID = uuidString()
	r.OrganizationID = input.OrganizationID
	r.WorkspaceID = input.WorkspaceID
	r.MetricCode = input.MetricCode
	r.Amount = input.Amount
	r.Status = "reserved"
	r.IdempotencyKey = input.IdempotencyKey
	r.ExpiresAt = input.ExpiresAt
	r.CreatedAt = input.Now
	r.PeriodStart = start
	r.PeriodEnd = end
	_, err = tx.Exec(ctx, `INSERT INTO usage_reservations(id,organization_id,workspace_id,metric_code,amount,status,idempotency_key,expires_at,created_at) VALUES($1::uuid,$2::uuid,NULLIF($3,'')::uuid,$4,$5,'reserved',$6,$7,$8)`, r.ID, r.OrganizationID, r.WorkspaceID, r.MetricCode, r.Amount, r.IdempotencyKey, r.ExpiresAt, r.CreatedAt)
	if isUniqueViolation(err) {
		return billingservice.UsageReservation{}, billingservice.ErrIdempotencyConflict
	}
	if err != nil {
		return billingservice.UsageReservation{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return billingservice.UsageReservation{}, err
	}
	_ = counterID
	return r, nil
}

func (s *Store) CommitUsage(ctx context.Context, org, id string, now time.Time) (billingservice.UsageReservation, error) {
	return s.transitionUsage(ctx, org, id, now, true)
}
func (s *Store) ReleaseUsage(ctx context.Context, org, id string, now time.Time) (billingservice.UsageReservation, error) {
	return s.transitionUsage(ctx, org, id, now, false)
}
func (s *Store) transitionUsage(ctx context.Context, org, id string, now time.Time, commit bool) (billingservice.UsageReservation, error) {
	ctx = WithTenantScope(ctx, TenantScope{OrganizationID: strings.TrimSpace(org)})
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return billingservice.UsageReservation{}, err
	}
	defer rollback(ctx, tx)
	var r billingservice.UsageReservation
	err = scanReservation(tx.QueryRow(ctx, `SELECT id::text,organization_id::text,COALESCE(workspace_id::text,''),metric_code,amount,status,idempotency_key,expires_at,created_at,date_trunc('month',created_at),date_trunc('month',created_at)+interval '1 month' FROM usage_reservations WHERE id=$1::uuid AND organization_id=$2::uuid FOR UPDATE`, id, org), &r)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, billingservice.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if (commit && r.Status == "committed") || (!commit && (r.Status == "released" || r.Status == "expired")) {
		return r, nil
	}
	if r.Status != "reserved" {
		return r, billingservice.ErrStateConflict
	}
	if !r.ExpiresAt.After(now) {
		if _, err = tx.Exec(ctx, `UPDATE usage_reservations SET status='expired' WHERE id=$1::uuid AND status='reserved'`, id); err != nil {
			return r, err
		}
		tag, updateErr := tx.Exec(ctx, `UPDATE usage_counters SET reserved_value=reserved_value-$1,version=version+1 WHERE organization_id=$2::uuid AND workspace_id IS NULL AND metric_code=$3 AND period_start=$4 AND reserved_value >= $1`, r.Amount, org, r.MetricCode, r.PeriodStart)
		if updateErr != nil {
			return r, updateErr
		}
		if tag.RowsAffected() != 1 {
			return r, billingservice.ErrStateConflict
		}
		r.Status = "expired"
		if commit {
			if err = tx.Commit(ctx); err != nil {
				return r, err
			}
			return r, billingservice.ErrStateConflict
		}
		if err = tx.Commit(ctx); err != nil {
			return r, err
		}
		return r, nil
	}
	status := "released"
	if commit {
		status = "committed"
	}
	if _, err = tx.Exec(ctx, `UPDATE usage_reservations SET status=$2 WHERE id=$1::uuid`, id, status); err != nil {
		return r, err
	}
	consumed := int64(0)
	if commit {
		consumed = r.Amount
	}
	tag, updateErr := tx.Exec(ctx, `UPDATE usage_counters SET reserved_value=reserved_value-$1,consumed_value=consumed_value+$2,version=version+1 WHERE organization_id=$3::uuid AND workspace_id IS NULL AND metric_code=$4 AND period_start=$5 AND reserved_value >= $1`, r.Amount, consumed, org, r.MetricCode, r.PeriodStart)
	if updateErr != nil {
		return r, updateErr
	}
	if tag.RowsAffected() != 1 {
		return r, billingservice.ErrStateConflict
	}
	r.Status = status
	if err = tx.Commit(ctx); err != nil {
		return r, err
	}
	return r, nil
}
func scanReservation(row scanner, r *billingservice.UsageReservation) error {
	return row.Scan(&r.ID, &r.OrganizationID, &r.WorkspaceID, &r.MetricCode, &r.Amount, &r.Status, &r.IdempotencyKey, &r.ExpiresAt, &r.CreatedAt, &r.PeriodStart, &r.PeriodEnd)
}

func (s *Store) ActivateLicense(ctx context.Context, a billingservice.LicenseActivation, hash []byte) (billingservice.LicenseActivation, error) {
	ctx = WithTenantScope(ctx, TenantScope{OrganizationID: strings.TrimSpace(a.OrganizationID)})
	result := a
	err := s.WithTenant(ctx, TenantScope{OrganizationID: a.OrganizationID}, func(ctx context.Context, tx pgx.Tx) error {
		var existing billingservice.LicenseActivation
		err := scanActivation(tx.QueryRow(ctx, `SELECT id::text,organization_id::text,COALESCE(workspace_id::text,''),COALESCE(device_id::text,''),COALESCE(release_channel_id::text,''),status,activated_at,last_refresh_at,expires_at,offline_grace_until,metadata FROM license_activations WHERE organization_id=$1::uuid AND license_token_hash=$2 FOR UPDATE`, a.OrganizationID, hash), &existing)
		if err == nil {
			if existing.WorkspaceID != a.WorkspaceID || existing.DeviceID != a.DeviceID {
				return billingservice.ErrLicenseScope
			}
			if existing.Status == "revoked" {
				return billingservice.ErrLicenseRevoked
			}
			result = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO license_activations(id,organization_id,workspace_id,device_id,release_channel_id,license_token_hash,status,activated_at,expires_at,offline_grace_until,metadata) VALUES($1::uuid,$2::uuid,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,'active',$7,$8,$9,$10::jsonb)`, a.ID, a.OrganizationID, a.WorkspaceID, a.DeviceID, a.ReleaseChannelID, hash, a.ActivatedAt, a.ExpiresAt, a.OfflineGraceUntil, encodeJSON(a.Metadata))
		if isUniqueViolation(err) {
			return billingservice.ErrStateConflict
		}
		return err
	})
	return result, err
}
func (s *Store) FindLicenseActivation(ctx context.Context, org string, hash []byte) (billingservice.LicenseActivation, error) {
	ctx = WithTenantScope(ctx, TenantScope{OrganizationID: strings.TrimSpace(org)})
	var a billingservice.LicenseActivation
	err := scanActivation(s.pool.QueryRow(ctx, `SELECT id::text,organization_id::text,COALESCE(workspace_id::text,''),COALESCE(device_id::text,''),COALESCE(release_channel_id::text,''),status,activated_at,last_refresh_at,expires_at,offline_grace_until,metadata FROM license_activations WHERE organization_id=$1::uuid AND license_token_hash=$2`, org, hash), &a)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, billingservice.ErrNotFound
	}
	return a, err
}
func (s *Store) RevokeLicense(ctx context.Context, org, id string, now time.Time) error {
	ctx = WithTenantScope(ctx, TenantScope{OrganizationID: strings.TrimSpace(org)})
	tag, err := s.pool.Exec(ctx, `UPDATE license_activations SET status='revoked',last_refresh_at=$3 WHERE organization_id=$1::uuid AND id=$2::uuid AND status<>'revoked'`, org, id, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var status string
		err = s.pool.QueryRow(ctx, `SELECT status FROM license_activations WHERE organization_id=$1::uuid AND id=$2::uuid`, org, id).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return billingservice.ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func scanActivation(row scanner, a *billingservice.LicenseActivation) error {
	var metadata []byte
	err := row.Scan(&a.ID, &a.OrganizationID, &a.WorkspaceID, &a.DeviceID, &a.ReleaseChannelID, &a.Status, &a.ActivatedAt, &a.LastRefreshAt, &a.ExpiresAt, &a.OfflineGraceUntil, &metadata)
	a.Metadata = decodeStringMap(metadata)
	return err
}

func (s *Store) ListReleaseChannels(ctx context.Context) ([]billingservice.ReleaseChannel, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,code,version,manifest,published_at,revoked_at FROM release_channels WHERE revoked_at IS NULL ORDER BY published_at DESC,code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]billingservice.ReleaseChannel, 0)
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) FindReleaseChannel(ctx context.Context, code string) (billingservice.ReleaseChannel, error) {
	c, err := scanChannel(s.pool.QueryRow(ctx, `SELECT id::text,code,version,manifest,published_at,revoked_at FROM release_channels WHERE code=$1 AND revoked_at IS NULL`, strings.ToLower(strings.TrimSpace(code))))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, billingservice.ErrNotFound
	}
	return c, err
}
func scanChannel(row scanner) (billingservice.ReleaseChannel, error) {
	var c billingservice.ReleaseChannel
	var manifest []byte
	err := row.Scan(&c.ID, &c.Code, &c.Version, &manifest, &c.PublishedAt, &c.RevokedAt)
	if len(manifest) > 0 {
		_ = json.Unmarshal(manifest, &c.Manifest)
	}
	return c, err
}

func (s *Store) CreatePlan(ctx context.Context, p billingservice.Plan) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO plans(id,code,name,currency,amount_minor,billing_interval,active,created_at,updated_at) VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9)`, p.ID, p.Code, p.Name, p.Currency, p.AmountMinor, p.BillingInterval, p.Active, p.CreatedAt, p.UpdatedAt)
	if isUniqueViolation(err) {
		return billingservice.ErrStateConflict
	}
	return err
}
func (s *Store) UpsertPlanEntitlement(ctx context.Context, e billingservice.PlanEntitlement) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO plan_entitlements(plan_id,entitlement_code,limit_value,feature_enabled,metadata) VALUES($1::uuid,$2,$3,$4,$5::jsonb) ON CONFLICT(plan_id,entitlement_code) DO UPDATE SET limit_value=EXCLUDED.limit_value,feature_enabled=EXCLUDED.feature_enabled,metadata=EXCLUDED.metadata`, e.PlanID, e.Code, e.Limit, e.FeatureEnabled, encodeJSON(e.Metadata))
	return err
}
func (s *Store) CreateSubscription(ctx context.Context, sub billingservice.Subscription) error {
	ctx = WithTenantScope(ctx, TenantScope{OrganizationID: strings.TrimSpace(sub.OrganizationID)})
	_, err := s.pool.Exec(ctx, `INSERT INTO subscriptions(id,organization_id,plan_id,status,provider,provider_subscription_ref,current_period_start,current_period_end,cancel_at_period_end,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11)`, sub.ID, sub.OrganizationID, sub.PlanID, sub.Status, sub.Provider, sub.ProviderSubscriptionRef, sub.CurrentPeriodStart, sub.CurrentPeriodEnd, sub.CancelAtPeriodEnd, sub.CreatedAt, sub.UpdatedAt)
	if isUniqueViolation(err) {
		return billingservice.ErrStateConflict
	}
	return err
}
func (s *Store) UpsertEntitlement(ctx context.Context, e billingservice.Entitlement) error {
	ctx = WithTenantScope(ctx, TenantScope{OrganizationID: strings.TrimSpace(e.OrganizationID)})
	_, err := s.pool.Exec(ctx, `INSERT INTO entitlements(id,organization_id,entitlement_code,source_subscription_id,value_limit,feature_enabled,consumed_value,valid_from,valid_until,metadata) VALUES($1::uuid,$2::uuid,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9,$10::jsonb) ON CONFLICT(organization_id,entitlement_code) DO UPDATE SET source_subscription_id=EXCLUDED.source_subscription_id,value_limit=EXCLUDED.value_limit,feature_enabled=EXCLUDED.feature_enabled,valid_from=EXCLUDED.valid_from,valid_until=EXCLUDED.valid_until,metadata=EXCLUDED.metadata`, e.ID, e.OrganizationID, e.Code, e.SourceSubscriptionID, e.Limit, e.FeatureEnabled, e.Consumed, e.ValidFrom, e.ValidUntil, encodeJSON(e.Metadata))
	return err
}
func (s *Store) CreateReleaseChannel(ctx context.Context, c billingservice.ReleaseChannel) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO release_channels(id,code,version,manifest,published_at,revoked_at) VALUES($1::uuid,$2,$3,$4::jsonb,$5,$6)`, c.ID, c.Code, c.Version, encodeJSON(c.Manifest), c.PublishedAt, c.RevokedAt)
	if isUniqueViolation(err) {
		return billingservice.ErrStateConflict
	}
	return err
}

func decodeStringMap(data []byte) map[string]string {
	if len(data) == 0 {
		return nil
	}
	var out map[string]string
	_ = json.Unmarshal(data, &out)
	return out
}
func encodeJSON(v interface{}) string { data, _ := json.Marshal(v); return string(data) }
func uuidString() string              { return uuid.NewString() }

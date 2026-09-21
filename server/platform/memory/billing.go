package memory

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	billingservice "github.com/zerlinpi/Ant-Browser/server/services/billing-service"
)

func (s *Store) ListPlans(_ context.Context) ([]billingservice.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]billingservice.Plan, 0, len(s.plans))
	for _, p := range s.plans {
		if p.Active {
			items = append(items, clonePlan(p))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := planOrder(items[i].Code), planOrder(items[j].Code)
		if left == right {
			return items[i].Code < items[j].Code
		}
		return left < right
	})
	return items, nil
}
func (s *Store) FindPlanByCode(_ context.Context, code string) (billingservice.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.planByCode[strings.ToLower(strings.TrimSpace(code))]
	if !ok {
		return billingservice.Plan{}, billingservice.ErrNotFound
	}
	return clonePlan(s.plans[id]), nil
}
func (s *Store) FindCurrentSubscription(_ context.Context, org string) (billingservice.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	org = strings.TrimSpace(org)
	for _, sub := range s.subscriptions {
		if sub.OrganizationID == org && isCurrentSubscription(sub.Status) {
			return sub, nil
		}
	}
	return billingservice.Subscription{}, billingservice.ErrNotFound
}
func (s *Store) ListEntitlements(_ context.Context, org string) ([]billingservice.Entitlement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	org = strings.TrimSpace(org)
	out := make([]billingservice.Entitlement, 0)
	now := time.Now().UTC()
	for _, e := range s.entitlements {
		if e.OrganizationID == org && !e.ValidFrom.After(now) && (e.ValidUntil == nil || e.ValidUntil.After(now)) {
			out = append(out, cloneEntitlement(e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}
func (s *Store) FindEntitlement(_ context.Context, org, code string) (billingservice.Entitlement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entitlements[org+"|"+strings.ToLower(strings.TrimSpace(code))]
	if !ok {
		return billingservice.Entitlement{}, billingservice.ErrNotFound
	}
	now := time.Now().UTC()
	if e.ValidFrom.After(now) || (e.ValidUntil != nil && !e.ValidUntil.After(now)) {
		return billingservice.Entitlement{}, billingservice.ErrNotFound
	}
	return cloneEntitlement(e), nil
}

func (s *Store) ReserveUsage(_ context.Context, input billingservice.ReserveUsageInput, limit *int64) (billingservice.UsageReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.MetricCode = strings.ToLower(strings.TrimSpace(input.MetricCode))
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	idemKey := input.OrganizationID + "|" + input.IdempotencyKey
	if existingID, ok := s.usageByIdempotency[idemKey]; ok {
		existing := s.usageReservations[existingID]
		if existing.WorkspaceID != input.WorkspaceID || existing.MetricCode != input.MetricCode || existing.Amount != input.Amount {
			return billingservice.UsageReservation{}, billingservice.ErrIdempotencyConflict
		}
		return existing, nil
	}
	start, end := usagePeriod(input.Now)
	counterKey := usageCounterKey(input.OrganizationID, "", input.MetricCode, start)
	counter, ok := s.usageCounters[counterKey]
	if !ok {
		counter = billingservice.UsageCounter{ID: billingID(), OrganizationID: input.OrganizationID, MetricCode: input.MetricCode, PeriodStart: start, PeriodEnd: end, Version: 1}
	}
	if limit != nil && counter.Consumed+counter.Reserved+input.Amount > *limit {
		return billingservice.UsageReservation{}, billingservice.ErrQuotaExceeded
	}
	counter.Reserved += input.Amount
	counter.Version++
	s.usageCounters[counterKey] = counter
	reservation := billingservice.UsageReservation{ID: billingID(), OrganizationID: input.OrganizationID, WorkspaceID: input.WorkspaceID, MetricCode: input.MetricCode, Amount: input.Amount, Status: "reserved", IdempotencyKey: input.IdempotencyKey, ExpiresAt: input.ExpiresAt, CreatedAt: input.Now, PeriodStart: start, PeriodEnd: end}
	s.usageReservations[reservation.ID] = reservation
	s.usageByIdempotency[idemKey] = reservation.ID
	return reservation, nil
}
func (s *Store) CommitUsage(_ context.Context, org, reservationID string, now time.Time) (billingservice.UsageReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.usageReservations[reservationID]
	if !ok || r.OrganizationID != strings.TrimSpace(org) {
		return billingservice.UsageReservation{}, billingservice.ErrNotFound
	}
	if r.Status == "committed" {
		return r, nil
	}
	if r.Status != "reserved" {
		return r, billingservice.ErrStateConflict
	}
	if !r.ExpiresAt.After(now) {
		if !s.canReleaseUsageLocked(r) {
			return r, billingservice.ErrStateConflict
		}
		r.Status = "expired"
		s.usageReservations[reservationID] = r
		s.adjustUsageLocked(r, -r.Amount, 0)
		return r, billingservice.ErrStateConflict
	}
	r.Status = "committed"
	s.usageReservations[reservationID] = r
	s.adjustUsageLocked(r, -r.Amount, r.Amount)
	return r, nil
}
func (s *Store) ReleaseUsage(_ context.Context, org, reservationID string, now time.Time) (billingservice.UsageReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.usageReservations[reservationID]
	if !ok || r.OrganizationID != strings.TrimSpace(org) {
		return billingservice.UsageReservation{}, billingservice.ErrNotFound
	}
	if r.Status == "released" || r.Status == "expired" {
		return r, nil
	}
	if r.Status == "committed" {
		return r, billingservice.ErrStateConflict
	}
	if !s.canReleaseUsageLocked(r) {
		return r, billingservice.ErrStateConflict
	}
	if !r.ExpiresAt.After(now) {
		r.Status = "expired"
	} else {
		r.Status = "released"
	}
	s.usageReservations[reservationID] = r
	s.adjustUsageLocked(r, -r.Amount, 0)
	return r, nil
}
func (s *Store) adjustUsageLocked(r billingservice.UsageReservation, reservedDelta, consumedDelta int64) {
	key := usageCounterKey(r.OrganizationID, "", r.MetricCode, r.PeriodStart)
	c := s.usageCounters[key]
	c.Reserved += reservedDelta
	c.Consumed += consumedDelta
	c.Version++
	s.usageCounters[key] = c
}
func (s *Store) canReleaseUsageLocked(r billingservice.UsageReservation) bool {
	c, ok := s.usageCounters[usageCounterKey(r.OrganizationID, "", r.MetricCode, r.PeriodStart)]
	return ok && c.Reserved >= r.Amount
}

func (s *Store) ActivateLicense(_ context.Context, activation billingservice.LicenseActivation, hash []byte) (billingservice.LicenseActivation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := string(hash)
	if existingID, ok := s.licenseByHash[key]; ok {
		existing := s.licenses[existingID]
		if existing.OrganizationID != activation.OrganizationID || existing.WorkspaceID != activation.WorkspaceID || existing.DeviceID != activation.DeviceID {
			return billingservice.LicenseActivation{}, billingservice.ErrLicenseScope
		}
		if existing.Status == "revoked" {
			return existing, billingservice.ErrLicenseRevoked
		}
		return existing, nil
	}
	s.licenses[activation.ID] = cloneActivation(activation)
	s.licenseByHash[key] = activation.ID
	return activation, nil
}
func (s *Store) FindLicenseActivation(_ context.Context, org string, hash []byte) (billingservice.LicenseActivation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.licenseByHash[string(hash)]
	if !ok {
		return billingservice.LicenseActivation{}, billingservice.ErrNotFound
	}
	a := s.licenses[id]
	if a.OrganizationID != strings.TrimSpace(org) {
		return billingservice.LicenseActivation{}, billingservice.ErrNotFound
	}
	return cloneActivation(a), nil
}
func (s *Store) RevokeLicense(_ context.Context, org, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.licenses[id]
	if !ok || a.OrganizationID != strings.TrimSpace(org) {
		return billingservice.ErrNotFound
	}
	if a.Status == "revoked" {
		return nil
	}
	a.Status = "revoked"
	a.LastRefreshAt = &now
	s.licenses[id] = a
	return nil
}
func (s *Store) ListReleaseChannels(_ context.Context) ([]billingservice.ReleaseChannel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]billingservice.ReleaseChannel, 0, len(s.releaseChannels))
	for _, c := range s.releaseChannels {
		out = append(out, cloneChannel(c))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PublishedAt.Equal(out[j].PublishedAt) {
			return out[i].Code < out[j].Code
		}
		return out[i].PublishedAt.After(out[j].PublishedAt)
	})
	return out, nil
}
func (s *Store) FindReleaseChannel(_ context.Context, code string) (billingservice.ReleaseChannel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.releaseByCode[strings.ToLower(strings.TrimSpace(code))]
	if !ok {
		return billingservice.ReleaseChannel{}, billingservice.ErrNotFound
	}
	c := s.releaseChannels[id]
	if c.RevokedAt != nil {
		return billingservice.ReleaseChannel{}, billingservice.ErrNotFound
	}
	return cloneChannel(c), nil
}

func (s *Store) CreatePlan(_ context.Context, p billingservice.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Code = strings.ToLower(strings.TrimSpace(p.Code))
	if p.Code == "" {
		return billingservice.ErrInvalidInput
	}
	if _, ok := s.planByCode[p.Code]; ok {
		return billingservice.ErrStateConflict
	}
	if p.ID == "" {
		p.ID = billingID()
	}
	s.plans[p.ID] = clonePlan(p)
	s.planByCode[p.Code] = p.ID
	return nil
}
func (s *Store) UpsertPlanEntitlement(_ context.Context, e billingservice.PlanEntitlement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.plans[e.PlanID]
	if !ok {
		return billingservice.ErrNotFound
	}
	for i := range p.Entitlements {
		if p.Entitlements[i].Code == e.Code {
			p.Entitlements[i] = e
			s.plans[e.PlanID] = p
			return nil
		}
	}
	p.Entitlements = append(p.Entitlements, e)
	s.plans[e.PlanID] = p
	return nil
}
func (s *Store) CreateSubscription(_ context.Context, sub billingservice.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, old := range s.subscriptions {
		if old.OrganizationID == sub.OrganizationID && isCurrentSubscription(old.Status) && isCurrentSubscription(sub.Status) {
			return billingservice.ErrStateConflict
		}
	}
	if sub.ID == "" {
		sub.ID = billingID()
	}
	s.subscriptions[sub.ID] = sub
	return nil
}
func (s *Store) UpsertEntitlement(_ context.Context, e billingservice.Entitlement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.ID == "" {
		e.ID = billingID()
	}
	s.entitlements[e.OrganizationID+"|"+strings.ToLower(e.Code)] = cloneEntitlement(e)
	return nil
}
func (s *Store) CreateReleaseChannel(_ context.Context, c billingservice.ReleaseChannel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.Code = strings.ToLower(strings.TrimSpace(c.Code))
	if _, ok := s.releaseByCode[c.Code]; ok {
		return billingservice.ErrStateConflict
	}
	if c.ID == "" {
		c.ID = billingID()
	}
	s.releaseChannels[c.ID] = cloneChannel(c)
	s.releaseByCode[c.Code] = c.ID
	return nil
}

func isCurrentSubscription(status string) bool {
	switch strings.ToLower(status) {
	case "trialing", "active", "past_due":
		return true
	}
	return false
}
func planOrder(code string) int {
	switch code {
	case billingservice.PlanFree:
		return 1
	case billingservice.PlanProfessional:
		return 2
	case billingservice.PlanEnterprise:
		return 3
	default:
		return 4
	}
}
func usagePeriod(now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}
func usageCounterKey(org, workspace, metric string, start time.Time) string {
	return org + "|" + workspace + "|" + metric + "|" + start.Format(time.RFC3339)
}
func billingID() string { return uuid.NewString() }
func clonePlan(p billingservice.Plan) billingservice.Plan {
	p.Entitlements = append([]billingservice.PlanEntitlement(nil), p.Entitlements...)
	return p
}
func cloneEntitlement(e billingservice.Entitlement) billingservice.Entitlement {
	e.Metadata = cloneStrings(e.Metadata)
	return e
}
func cloneActivation(a billingservice.LicenseActivation) billingservice.LicenseActivation {
	a.Metadata = cloneStrings(a.Metadata)
	return a
}
func cloneChannel(c billingservice.ReleaseChannel) billingservice.ReleaseChannel {
	if c.Manifest != nil {
		m := make(map[string]interface{}, len(c.Manifest))
		for k, v := range c.Manifest {
			m[k] = v
		}
		c.Manifest = m
	}
	return c
}
func cloneStrings(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

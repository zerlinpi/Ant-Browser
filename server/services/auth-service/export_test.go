package authservice

import "time"

// SetClock lets the external tests drive TOTP time steps and lockouts.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

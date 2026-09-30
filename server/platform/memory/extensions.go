package memory

// Feature state that does not belong to the core maps in store.go lives in
// per-feature structs kept in Store.features, so features can add
// repositories in their own files without editing Store or New.

// featureState returns the state of type T, creating it with init on first
// use. It is safe to call with s.mu held for reading or writing; the
// returned state's fields are protected by s.mu like the rest of the store.
func featureState[T any](s *Store, init func() *T) *T {
	s.featuresMu.Lock()
	defer s.featuresMu.Unlock()
	key := featureKey[T]{}
	if value, ok := s.features[key]; ok {
		return value.(*T)
	}
	value := init()
	s.features[key] = value
	return value
}

type featureKey[T any] struct{}

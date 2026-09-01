package conflict

// Strategy defines how profile conflicts are resolved.
type Strategy string

const (
	LatestVersion Strategy = "latest"
	KeepLocal     Strategy = "local"
	Manual        Strategy = "manual"
)

// Resolver handles profile version conflicts.
type Resolver struct {
	Strategy Strategy
}

func NewResolver(strategy Strategy) *Resolver {
	return &Resolver{Strategy: strategy}
}

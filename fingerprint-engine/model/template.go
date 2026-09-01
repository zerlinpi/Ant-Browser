package model

// Template describes a browser fingerprint configuration.
// It can be random, fixed, or customized.
type Template struct {
	ID string
	Name string
	Mode string
	Browser string
	Platform string
	Timezone string
	Language string
	CPU int
	Memory int
	GPU string
}

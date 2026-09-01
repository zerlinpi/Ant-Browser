package proxy

// Proxy represents network environment attached to a profile.
type Proxy struct {
	ID string

	Name string
	Host string
	Port int

	Country string

	Username string
	Password string

	Status string
}

// ProxyManager manages proxy assignments.
type ProxyManager struct {
	items map[string]*Proxy
}

func NewProxyManager() *ProxyManager {
	return &ProxyManager{
		items: make(map[string]*Proxy),
	}
}

func (m *ProxyManager) Add(p *Proxy) {
	m.items[p.ID] = p
}

func (m *ProxyManager) Get(id string) *Proxy {
	return m.items[id]
}

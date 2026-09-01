package websocket

// Hub manages realtime communication between cloud and desktop clients.

type Hub struct {
	clients map[string]bool
}

func NewHub() *Hub {
	return &Hub{clients: make(map[string]bool)}
}

func (h *Hub) Register(deviceID string) {
	h.clients[deviceID] = true
}

func (h *Hub) Unregister(deviceID string) {
	delete(h.clients, deviceID)
}

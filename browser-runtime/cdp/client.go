package cdp

// Client manages communication with Chromium DevTools Protocol.
// It will provide browser automation and runtime inspection.
type Client struct {
	Endpoint string
}

func NewClient(endpoint string) *Client {
	return &Client{Endpoint: endpoint}
}

func (c *Client) Connect() error {
	// TODO:
	// connect websocket CDP endpoint
	return nil
}

func (c *Client) Close() error {
	return nil
}

// Package cdp provides a small, loopback-only Chrome DevTools Protocol client.
package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var (
	ErrInvalidEndpoint  = errors.New("invalid CDP endpoint")
	ErrNotConnected     = errors.New("CDP client is not connected")
	ErrResponseTooLarge = errors.New("CDP discovery response is too large")
)

const maxDiscoveryBytes = 64 * 1024

type Client struct {
	Endpoint   string
	Timeout    time.Duration
	HTTPClient *http.Client

	mu     sync.Mutex
	conn   *websocket.Conn
	nextID atomic.Uint64
}

func NewClient(endpoint string) *Client { return &Client{Endpoint: endpoint, Timeout: 5 * time.Second} }

// Connect accepts either a loopback HTTP DevTools base endpoint (and performs
// /json/version discovery) or a loopback ws endpoint. No DNS name other than
// localhost is accepted, and localhost is dialed only as loopback addresses.
func (c *Client) Connect() error {
	return c.ConnectContext(context.Background())
}

// ConnectContext is the cancelable form used by startup readiness checks. It
// keeps discovery and the websocket handshake bounded by the caller's
// deadline, instead of allowing a per-client timeout to outlive the startup
// flow.
func (c *Client) ConnectContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return nil
	}
	rawEndpoint := strings.TrimSpace(c.Endpoint)
	parsedEndpoint, parseErr := url.Parse(rawEndpoint)
	if parseErr != nil || parsedEndpoint == nil {
		return fmt.Errorf("%w: malformed endpoint", ErrInvalidEndpoint)
	}
	websocketInput := strings.EqualFold(parsedEndpoint.Scheme, "ws") || strings.EqualFold(parsedEndpoint.Scheme, "wss")
	u, err := validateEndpoint(rawEndpoint, websocketInput)
	if err != nil {
		return err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	var wsURL string
	if u.Scheme == "ws" || u.Scheme == "wss" {
		wsURL = u.String()
	} else {
		versionURL := *u
		if versionURL.Path == "" || versionURL.Path == "/" {
			versionURL.Path = "/json/version"
		}
		client := c.HTTPClient
		if client == nil {
			client = localHTTPClient(timeout)
		} else {
			// Preserve caller-provided transport/test hooks while making the
			// redirect policy non-negotiable; otherwise a local endpoint could
			// redirect discovery to an arbitrary host.
			clone := *client
			clone.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
				return errors.New("CDP discovery redirects are disabled")
			}
			client = &clone
		}
		discoveryCtx, cancel := context.WithTimeout(ctx, timeout)
		req, reqErr := http.NewRequestWithContext(discoveryCtx, http.MethodGet, versionURL.String(), nil)
		if reqErr != nil {
			cancel()
			return fmt.Errorf("discover CDP endpoint: %w", reqErr)
		}
		resp, reqErr := client.Do(req)
		if reqErr != nil {
			cancel()
			return fmt.Errorf("discover CDP endpoint: %w", reqErr)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxDiscoveryBytes+1))
		resp.Body.Close()
		cancel()
		if len(body) > maxDiscoveryBytes {
			return ErrResponseTooLarge
		}
		if readErr != nil {
			return fmt.Errorf("read CDP discovery response: %w", readErr)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("discover CDP endpoint: HTTP %s", resp.Status)
		}
		var version struct {
			WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
		}
		if err := json.Unmarshal(body, &version); err != nil || strings.TrimSpace(version.WebSocketDebuggerURL) == "" {
			return fmt.Errorf("%w: discovery response has no websocket URL", ErrInvalidEndpoint)
		}
		wsURL = strings.TrimSpace(version.WebSocketDebuggerURL)
	}
	ws, err := validateEndpoint(wsURL, true)
	if err != nil {
		return err
	}
	dialer := websocket.Dialer{HandshakeTimeout: timeout, NetDialContext: localDialContext}
	conn, _, err := dialer.DialContext(ctx, ws.String(), nil)
	if err != nil {
		return fmt.Errorf("connect CDP websocket: %w", err)
	}
	conn.SetReadLimit(1 << 20)
	c.conn = conn
	return nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

func (c *Client) Connected() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.conn != nil }

// Call executes one JSON-RPC CDP command. Calls are serialized because CDP
// events and responses share one websocket; reads are bounded by Timeout and
// the response body is bounded by the websocket read limit.
func (c *Client) Call(ctx context.Context, method string, params interface{}, result interface{}) error {
	if strings.TrimSpace(method) == "" || strings.ContainsAny(method, "\x00\r\n") {
		return errors.New("invalid CDP method")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return ErrNotConnected
	}
	id := c.nextID.Add(1)
	if id == 0 {
		id = c.nextID.Add(1)
	}
	payload := struct {
		ID     uint64      `json:"id"`
		Method string      `json:"method"`
		Params interface{} `json:"params,omitempty"`
	}{id, method, params}
	if err := c.setDeadline(ctx); err != nil {
		return err
	}
	if err := c.conn.WriteJSON(payload); err != nil {
		return fmt.Errorf("write CDP command: %w", err)
	}
	for {
		if err := c.setDeadline(ctx); err != nil {
			return err
		}
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read CDP response: %w", err)
		}
		var message struct {
			ID     uint64          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &message); err != nil {
			return fmt.Errorf("decode CDP response: %w", err)
		}
		if message.ID != id {
			continue
		}
		if message.Error != nil {
			return fmt.Errorf("CDP %s (%d): %s", method, message.Error.Code, message.Error.Message)
		}
		if result != nil && len(message.Result) != 0 {
			if err := json.Unmarshal(message.Result, result); err != nil {
				return fmt.Errorf("decode CDP result: %w", err)
			}
		}
		return nil
	}
}

func (c *Client) setDeadline(ctx context.Context) error {
	deadline := time.Now().Add(c.Timeout)
	if c.Timeout <= 0 {
		deadline = time.Now().Add(5 * time.Second)
	}
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.conn.SetReadDeadline(deadline); err != nil {
		return err
	}
	return c.conn.SetWriteDeadline(deadline)
}

func validateEndpoint(raw string, websocketOnly bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Hostname() == "" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("%w: endpoint must be an unauthenticated loopback URL", ErrInvalidEndpoint)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if websocketOnly {
			return nil, fmt.Errorf("%w: websocket URL required", ErrInvalidEndpoint)
		}
	case "ws", "wss":
		if !websocketOnly {
			return nil, fmt.Errorf("%w: HTTP discovery URL required", ErrInvalidEndpoint)
		}
	default:
		return nil, fmt.Errorf("%w: unsupported URL scheme", ErrInvalidEndpoint)
	}
	if !isLoopbackHost(u.Hostname()) {
		return nil, fmt.Errorf("%w: endpoint host is not loopback", ErrInvalidEndpoint)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("%w: endpoint port is invalid", ErrInvalidEndpoint)
	}
	return u, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func localHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("CDP discovery redirects are disabled")
	}, Transport: &http.Transport{DialContext: localDialContext}}
}

func localDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if !isLoopbackHost(host) {
		return nil, fmt.Errorf("%w: dial target is not loopback", ErrInvalidEndpoint)
	}
	if strings.EqualFold(host, "localhost") {
		host = "127.0.0.1"
	}
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(host, port))
}

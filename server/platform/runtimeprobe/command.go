// Package runtimeprobe connects cloud workers to the existing proxy runtime.
package runtimeprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Secrets interface {
	Resolve(context.Context, string, string, string) (proxyservice.Secret, error)
}
type Command struct {
	executable, config, root, target string
	secrets                          Secrets
}

func New(executable, config, root, target string, secrets Secrets) (*Command, error) {
	for _, path := range []string{executable, config, root} {
		if !filepath.IsAbs(path) {
			return nil, errors.New("runtime probe paths must be absolute")
		}
	}
	for _, path := range []string{executable, config} {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("runtime probe executable or configuration unavailable")
		}
	}
	endpoint, err := url.Parse(target)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil {
		return nil, errors.New("runtime probe target must be HTTPS")
	}
	if secrets == nil {
		return nil, errors.New("runtime probe secret resolver is required")
	}
	return &Command{executable: executable, config: config, root: root, target: target, secrets: secrets}, nil
}

func (c *Command) Probe(ctx context.Context, proxy proxyservice.Proxy) (proxyservice.HealthResult, error) {
	var secret proxyservice.Secret
	if proxy.SecretRef != "" {
		var err error
		secret, err = c.secrets.Resolve(ctx, proxy.WorkspaceID, proxy.ID, proxy.SecretRef)
		if err != nil {
			return proxyservice.HealthResult{}, errors.New("proxy credentials unavailable")
		}
	}
	source, err := proxySource(proxy, secret)
	if err != nil {
		return proxyservice.HealthResult{}, err
	}
	payload, err := json.Marshal(struct {
		ProxyConfig   string                     `json:"proxyConfig"`
		ConnectorType proxyservice.ConnectorType `json:"connectorType"`
		Kernel        proxyservice.Kernel        `json:"kernel"`
	}{source, proxy.ConnectorType, proxy.Kernel})
	if err != nil {
		return proxyservice.HealthResult{}, err
	}
	defer func() {
		for i := range payload {
			payload[i] = 0
		}
	}()
	if len(payload) > 65536 {
		return proxyservice.HealthResult{}, errors.New("proxy configuration exceeds runtime limit")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.executable, "-config", c.config, "-root", c.root, "-target", c.target)
	hideWindow(cmd)
	// Child processes do not need database passwords, JWT or master keys.
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Stdin = bytes.NewReader(payload)
	output := &boundedOutput{limit: 8192}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		return proxyservice.HealthResult{}, errors.New("runtime probe execution failed")
	}
	var result proxyservice.HealthResult
	if json.Unmarshal(output.data.Bytes(), &result) != nil {
		return proxyservice.HealthResult{}, errors.New("runtime probe returned invalid JSON")
	}
	return result, nil
}

func proxySource(proxy proxyservice.Proxy, secret proxyservice.Secret) (string, error) {
	switch proxy.Protocol {
	case "direct":
		return "direct://", nil
	case "http", "https", "socks5":
		address := &url.URL{Scheme: proxy.Protocol, Host: net.JoinHostPort(proxy.Host, strconv.Itoa(proxy.Port))}
		if proxy.HasCredentials || proxy.Username != "" {
			address.User = url.UserPassword(proxy.Username, secret.Password)
		}
		return address.String(), nil
	default:
		// Advanced nodes require their full encrypted URI/config, including TLS
		// and transport parameters, rather than lossy host/port reconstruction.
		if strings.TrimSpace(secret.Token) == "" {
			return "", errors.New("advanced proxy configuration is required")
		}
		return secret.Token, nil
	}
}

type boundedOutput struct {
	data  bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.data.Len()+len(p) > b.limit {
		return 0, errors.New("runtime output exceeds limit")
	}
	return b.data.Write(p)
}

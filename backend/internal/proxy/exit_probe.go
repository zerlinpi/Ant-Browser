package proxy

import (
	"ant-chrome/backend/internal/config"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type ExitProbeResult struct {
	IP        string `json:"ip"`
	LatencyMS int64  `json:"latencyMs"`
}

// ProbeExitIP shares the exact connector selection used for browser traffic.
// The target is operator configuration, not an arbitrary URL from a task.
func ProbeExitIP(ctx context.Context, src, proxyID string, proxies []config.BrowserProxy,
	xray *XrayManager, singbox *SingBoxManager, mihomo *ClashManager,
	connector, target string, timeout time.Duration) (ExitProbeResult, error) {
	if timeout <= 0 || timeout > time.Minute {
		return ExitProbeResult{}, errors.New("probe timeout must be between zero and one minute")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client, err := BuildProxyHTTPClient(src, proxyID, proxies, xray, singbox, mihomo, connector, timeout)
	if err != nil {
		return ExitProbeResult{}, errors.New("proxy connector could not start")
	}
	defer client.CloseIdleConnections()
	return probeExitHTTP(ctx, client, target)
}

func probeExitHTTP(ctx context.Context, client *http.Client, target string) (ExitProbeResult, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ExitProbeResult{}, errors.New("invalid exit probe target")
	}
	local := *client
	local.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return ExitProbeResult{}, errors.New("invalid exit probe request")
	}
	request.Header.Set("Accept", "application/json, text/plain")
	started := time.Now()
	response, err := local.Do(request)
	if err != nil {
		return ExitProbeResult{}, errors.New("exit probe request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ExitProbeResult{}, errors.New("exit probe returned unsuccessful status")
	}
	const maximum = 4096
	body, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil || len(body) > maximum {
		return ExitProbeResult{}, errors.New("invalid exit probe response size")
	}
	value := strings.TrimSpace(string(body))
	if strings.HasPrefix(value, "{") {
		var payload struct {
			IP string `json:"ip"`
		}
		if json.Unmarshal(body, &payload) != nil {
			return ExitProbeResult{}, errors.New("invalid exit probe JSON")
		}
		value = payload.IP
	}
	ip, err := netip.ParseAddr(value)
	if err != nil || ip.Zone() != "" {
		return ExitProbeResult{}, errors.New("exit probe did not return an IP address")
	}
	return ExitProbeResult{IP: ip.Unmap().String(), LatencyMS: time.Since(started).Milliseconds()}, nil
}

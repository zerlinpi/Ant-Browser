// proxy-probe is a one-shot bridge to the existing browser proxy runtime.
// Credentials travel only on stdin; stdout is reserved for the JSON result.
package main

import (
	"ant-chrome/backend/internal/config"
	"ant-chrome/backend/internal/proxy"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"time"
)

type request struct {
	ProxyConfig   string `json:"proxyConfig"`
	ConnectorType string `json:"connectorType"`
	Kernel        string `json:"kernel"`
}
type response struct {
	Status    string `json:"status"`
	IP        string `json:"ip,omitempty"`
	LatencyMS int64  `json:"latencyMs,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

func main() {
	output := os.Stdout
	os.Stdout = os.Stderr // legacy runtime diagnostics must not corrupt the protocol
	configPath := flag.String("config", "", "runtime configuration file")
	root := flag.String("root", "", "runtime root with installed connector binaries")
	target := flag.String("target", "", "operator-configured IP echo endpoint")
	flag.Parse()
	result, err := run(context.Background(), os.Stdin, *configPath, *root, *target)
	if err != nil {
		result = response{Status: "failed", ErrorCode: "runtime_probe_unavailable"}
	}
	_ = json.NewEncoder(output).Encode(result)
	if err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, input io.Reader, configPath, root, target string) (response, error) {
	var value request
	decoder := json.NewDecoder(io.LimitReader(input, 65537))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return response{}, errors.New("invalid probe input")
	}
	var trailing interface{}
	if decoder.Decode(&trailing) != io.EOF {
		return response{}, errors.New("invalid trailing probe input")
	}
	if value.ConnectorType != "xray" && value.ConnectorType != "mihomo" {
		return response{}, errors.New("invalid connector")
	}
	if !filepath.IsAbs(root) || configPath == "" || target == "" {
		return response{}, errors.New("runtime configuration is required")
	}
	if info, err := os.Stat(configPath); err != nil || !info.Mode().IsRegular() {
		return response{}, errors.New("runtime configuration unavailable")
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return response{}, errors.New("runtime configuration unavailable")
	}
	resolution, err := proxy.ResolveProxyKernelForConnector(value.ProxyConfig, nil, "", value.ConnectorType)
	if err != nil {
		return response{}, errors.New("unsupported runtime route")
	}
	kernel := resolution.Kernel
	if kernel == proxy.ProxyKernelNative {
		kernel = "direct"
	}
	if kernel != value.Kernel {
		return response{}, errors.New("runtime route mismatch")
	}
	workdir, err := os.MkdirTemp(root, "health-")
	if err != nil {
		return response{}, errors.New("runtime work directory unavailable")
	}
	defer os.RemoveAll(workdir)
	cfg.Browser.UserDataRoot = workdir
	xray := proxy.NewXrayManager(cfg, root)
	defer xray.StopAll()
	singbox := proxy.NewSingBoxManager(cfg, root)
	defer singbox.StopAll()
	mihomo := proxy.NewClashManager(cfg, root)
	defer mihomo.StopAll()
	result, err := proxy.ProbeExitIP(ctx, value.ProxyConfig, "", nil, xray, singbox, mihomo, value.ConnectorType, target, 20*time.Second)
	if err != nil {
		return response{}, err
	}
	return response{Status: "succeeded", IP: result.IP, LatencyMS: result.LatencyMS}, nil
}

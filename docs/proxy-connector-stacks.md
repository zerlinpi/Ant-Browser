# Proxy connector stacks

This is the authoritative connector-routing contract for browser launch,
proxy checks, warmup, diagnostics, and downloads.

## Two supported stacks

`browser.default_connector_type: xray` selects the Xray combination stack:

- Xray handles vmess, vless, trojan, shadowsocks, and chained proxies.
- sing-box handles hysteria, hysteria2, tuic, and anytls.

`browser.default_connector_type: mihomo` selects the independent Mihomo
stack. Protocols supported by Mihomo are routed to Mihomo, including the
Mihomo-only Clash node protocols. It must not silently fall back to Xray or
sing-box.

Direct proxies and unauthenticated native HTTP/SOCKS5 proxies do not require a
connector process. Authenticated native proxies are bridged by the selected
stack according to the resolver.

## Required call sites

The active connector type must be passed through
`ResolveProxyKernelForConnector` for instance start, speed tests, real
connectivity, IP health, warmup, and diagnostics. A resolver error is a hard
failure; callers must not retry through another stack.

## Bootstrap download exception

Downloading a proxy core is a bootstrap operation. Direct/http/https/socks5
downloads remain available without a connector. Advanced proxy URLs are sent
through the configured stack only when its connector binary is already
installed. If that binary is missing (for example, downloading Mihomo through
Mihomo), the operation fails with an explicit bootstrap error; it must not
silently use another stack. Chromium-core downloads use the configured stack
through the backend-injected download client. The legacy `__system__` mode is
platform transport behavior, not a connector stack, and remains isolated to
that downloader path.

## Compatibility rule

Keep the existing Wails APIs and Chromium launch arguments stable. New cloud or
desktop-agent layers must carry the connector type as part of desired state
and must leave protocol execution to the local runtime.

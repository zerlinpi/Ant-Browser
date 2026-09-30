import type { ProxyConnectorType, ProxyHealthCheck, ProxyKernel } from "@/types";

/**
 * Protocol routing per connector stack, mirroring ResolveProxyKernelForConnector
 * in server/services/proxy-service/service.go (the server stays authoritative
 * and returns the resolved `kernel` on every proxy).
 *
 * - xray: Xray + sing-box combination stack. Xray runs vmess/vless/trojan/
 *   shadowsocks/chained; sing-box runs hysteria/hysteria2/tuic/anytls.
 * - mihomo: independent Mihomo stack.
 *
 * The two stacks never fall back to each other, so the UI never switches a
 * proxy's stack on its own.
 */
export interface ProtocolGroup {
  label: string;
  /** Kernel for every protocol in the group; native protocols depend on credentials. */
  kernel?: Exclude<ProxyKernel, "direct">;
  protocols: string[];
}

const nativeGroup: ProtocolGroup = { label: "原生协议", protocols: ["direct", "http", "https", "socks5"] };

export const protocolGroups: Record<ProxyConnectorType, ProtocolGroup[]> = {
  xray: [
    nativeGroup,
    { label: "Xray", kernel: "xray", protocols: ["vmess", "vless", "trojan", "shadowsocks", "chained"] },
    { label: "sing-box", kernel: "sing-box", protocols: ["hysteria", "hysteria2", "tuic", "anytls"] },
  ],
  mihomo: [
    nativeGroup,
    {
      label: "Mihomo",
      kernel: "mihomo",
      protocols: ["vmess", "vless", "trojan", "shadowsocks", "hysteria", "hysteria2", "tuic", "anytls", "wireguard", "snell", "ssr", "shadowtls", "http3", "quic", "naive"],
    },
  ],
};

export const stackProtocols = (stack: ProxyConnectorType) => protocolGroups[stack].flatMap((group) => group.protocols);
export const allProtocols = [...new Set([...stackProtocols("xray"), ...stackProtocols("mihomo")])];
export const supportsProtocol = (stack: ProxyConnectorType, protocol: string) => stackProtocols(stack).includes(protocol);

/** Local preview of the server resolver; undefined means the stack rejects the protocol. */
export const resolveKernel = (stack: ProxyConnectorType, protocol: string, authenticated: boolean): ProxyKernel | undefined => {
  if (protocol === "direct") return "direct";
  if (nativeGroup.protocols.includes(protocol)) return authenticated ? (stack === "mihomo" ? "mihomo" : "xray") : "direct";
  return protocolGroups[stack].find((group) => group.protocols.includes(protocol))?.kernel;
};

const protocolNames: Record<string, string> = {
  direct: "直连",
  http: "HTTP",
  https: "HTTPS",
  socks5: "SOCKS5",
  vmess: "VMess",
  vless: "VLESS",
  trojan: "Trojan",
  shadowsocks: "Shadowsocks",
  chained: "链式代理",
  hysteria: "Hysteria",
  hysteria2: "Hysteria2",
  tuic: "TUIC",
  anytls: "AnyTLS",
  wireguard: "WireGuard",
  snell: "Snell",
  ssr: "ShadowsocksR",
  shadowtls: "ShadowTLS",
  http3: "HTTP/3",
  quic: "QUIC",
  naive: "NaïveProxy",
};

const kernelNames: Record<string, string> = { xray: "Xray", "sing-box": "sing-box", mihomo: "Mihomo", direct: "无需连接器" };

export const normalizeStack = (value?: string): ProxyConnectorType => (value === "mihomo" ? "mihomo" : "xray");
export const protocolLabel = (protocol: string) => protocolNames[protocol] ?? protocol;
export const stackLabel = (stack?: string) => (normalizeStack(stack) === "mihomo" ? "mihomo 独立栈" : "xray 组合栈");
export const executorLabel = (stack?: string, kernel?: string) =>
  kernel ? `${stackLabel(stack)} · ${kernelNames[kernel] ?? kernel}` : stackLabel(stack);

/** The task worker only probes proxies in these states; others end as `proxy_disabled`. */
export const isHealthCheckable = (status?: string) => status === "active" || status === "unhealthy";

// Stable codes from server/services/task-worker/proxy_health.go and the task
// terminal sync (error messages are never persisted). Only proxy_unreachable
// is a network measurement; the rest mean the probe did not run to completion.
const healthErrorNames: Record<string, string> = {
  proxy_unreachable: "代理不可达",
  proxy_disabled: "代理已停用，未检测",
  proxy_deleted: "代理已删除",
  proxy_route_changed: "排队后连接配置已变更",
  unsupported_proxy_route: "连接栈无法路由该协议",
  task_failed: "检测任务执行失败",
  task_cancelled: "检测任务已取消",
  retry_limit_exhausted: "检测重试次数已用尽",
};

/** StatusBadge input: tone key plus Chinese label. */
export const healthBadge = (check?: ProxyHealthCheck): { status: string; label: string } => {
  if (!check) return { status: "unknown", label: "未检测" };
  switch (check.status) {
    case "queued":
      return { status: "queued", label: "排队中" };
    case "succeeded":
      return { status: "succeeded", label: "可用" };
    case "failed":
      return check.errorCode === "proxy_unreachable" ? { status: "failed", label: "不可用" } : { status: "not_run", label: "未完成" };
    case "cancelled":
      return { status: "cancelled", label: "已取消" };
    default:
      return { status: check.status, label: check.status };
  }
};

export const healthReason = (check?: ProxyHealthCheck) => {
  if (!check || (check.status !== "failed" && check.status !== "cancelled")) return "";
  return (check.errorCode && healthErrorNames[check.errorCode]) || check.errorMessage || check.errorCode || "";
};

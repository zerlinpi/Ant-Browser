import type { DevicePlatform } from "@/types";

/** Platforms accepted by deviceservice.RegisterInput. */
export const devicePlatformOptions: ReadonlyArray<{ value: DevicePlatform; label: string }> = [
  { value: "windows", label: "Windows" },
  { value: "darwin", label: "macOS" },
  { value: "linux", label: "Linux" },
];

const platformLabels: Record<string, string> = { windows: "Windows", darwin: "macOS", linux: "Linux" };
const platformIcons: Record<string, string> = { windows: "lucide:monitor", darwin: "lucide:laptop", linux: "lucide:terminal" };
// devices.status CHECK constraint: pending | online | offline | revoked.
const statusLabels: Record<string, string> = { pending: "待激活", online: "在线", offline: "离线", revoked: "已吊销" };

export const devicePlatformLabel = (value?: string) => (value ? platformLabels[value.toLowerCase()] ?? value : "—");
export const devicePlatformIcon = (value?: string) => platformIcons[(value ?? "").toLowerCase()] ?? "lucide:hard-drive";
export const deviceStatusLabel = (value?: string) => (value ? statusLabels[value.toLowerCase()] ?? value : "未知");

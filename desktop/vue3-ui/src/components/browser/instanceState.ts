import type { AgentDevice, BrowserInstance } from "@/types";

/** Server command deadlines (browser-instance-service commandDeadline). */
export const COMMAND_WINDOW_MS = 2 * 60_000;
export const MIGRATION_WINDOW_MS = 30 * 60_000;

/**
 * Commands on an instance bound to a cloud profile restore or upload that
 * profile, so the server grants them the migration budget as well.
 */
export const commandWindowMs = (item: BrowserInstance, action?: string) =>
  action === "instance.migrate" || isMigrating(item) || Boolean(item.profileId?.trim()) ? MIGRATION_WINDOW_MS : COMMAND_WINDOW_MS;

/** batchservice.MaxBatchSize */
export const MAX_BATCH_ITEMS = 100;

/** Limits enforced by browser-instance-service (name) and normalizeTags (tags). */
export const MAX_NAME_LENGTH = 120;
export const MAX_TAGS = 20;
export const MAX_TAG_LENGTH = 40;

const normalize = (value?: string | null) => (value ?? "").trim().toLowerCase();
/** Go counts runes, so compare code points rather than UTF-16 units. */
const runeLength = (value: string) => [...value].length;

// desiredState: stopped | running | migrating | paused | deleted (migration 009).
// observedState: offline | starting | running | stopping | failed (migration 002).
// The agent reports observed state only after it finishes a command, so a
// pending start/stop shows up as a desired/observed mismatch.

export const isMigrating = (item: BrowserInstance) => normalize(item.desiredState) === "migrating";

/**
 * Mirrors BrowserInstance.isRuntimeActive: while true the server rejects
 * profile, fingerprint, proxy and device changes as well as deletion.
 */
export const isRuntimeActive = (item: BrowserInstance) => {
  const desired = normalize(item.desiredState);
  const observed = normalize(item.observedState);
  return desired === "running" || desired === "migrating" || ["running", "starting", "stopping", "migrating"].includes(observed);
};

/** Whether the row toggle should offer 停止 rather than 启动. */
export const isActive = (item: BrowserInstance) => {
  const desired = normalize(item.desiredState);
  const observed = normalize(item.observedState);
  return desired === "running" || desired === "migrating" || observed === "running" || observed === "starting";
};

/** Waiting for the agent: explicit transitional states or an unconverged desired state. */
export const isTransitional = (item: BrowserInstance) => {
  const desired = normalize(item.desiredState);
  const observed = normalize(item.observedState);
  if (desired === "migrating" || observed === "starting" || observed === "stopping") return true;
  if (desired === "running") return observed === "offline";
  if (desired === "stopped") return observed === "running";
  return false;
};

const observedLabels: Record<string, string> = {
  offline: "离线",
  starting: "启动中",
  running: "运行中",
  stopping: "停止中",
  failed: "异常",
};

export const instanceState = (item: BrowserInstance): { status: string; label: string } => {
  const desired = normalize(item.desiredState);
  const observed = normalize(item.observedState);
  if (desired === "migrating") return { status: "migrating", label: "迁移中" };
  if (desired === "running" && observed === "offline") return { status: "pending", label: "待启动" };
  if (desired === "stopped" && observed === "running") return { status: "pending", label: "待停止" };
  return { status: observed || "unknown", label: observedLabels[observed] ?? (item.observedState || "未知") };
};

const platformLabels: Record<string, string> = { windows: "Windows", linux: "Linux", macos: "macOS", darwin: "macOS" };
export const platformLabel = (value?: string) => platformLabels[normalize(value)] ?? (value || "—");

const deviceStatusLabels: Record<string, string> = { online: "在线", offline: "离线", revoked: "已撤销" };
export const deviceStatusLabel = (status?: string) => deviceStatusLabels[normalize(status)] ?? (status || "未知");

/** Devices the server accepts as assignment or migration targets in this workspace. */
export const isUsableDevice = (device: AgentDevice, workspaceId: string) =>
  device.workspaceId === workspaceId && !device.revokedAt && normalize(device.status) !== "revoked";

export const deviceOptionLabel = (device: AgentDevice) =>
  `${device.name} · ${platformLabel(device.platform)} · ${deviceStatusLabel(device.status)}`;

export const migrationTargets = (devices: readonly AgentDevice[], item: BrowserInstance | null) =>
  devices.filter((device) => device.id !== item?.assignedDeviceId);

/** Comma or newline separated input, trimmed and de-duplicated in order. */
export const parseTags = (value: string) => {
  const tags = new Set<string>();
  for (const part of value.split(/[,，\n]/)) {
    const tag = part.trim();
    if (tag) tags.add(tag);
  }
  return [...tags];
};

export const sameTags = (left: readonly string[], right: readonly string[]) =>
  left.length === right.length && left.every((tag, index) => tag === right[index]);

/** The server silently drops invalid tags, so report them before saving. */
export const tagsError = (tags: readonly string[]) => {
  if (tags.length > MAX_TAGS) return `最多 ${MAX_TAGS} 个标签`;
  const tooLong = tags.find((tag) => runeLength(tag) > MAX_TAG_LENGTH);
  return tooLong ? `标签「${tooLong}」超过 ${MAX_TAG_LENGTH} 个字符` : "";
};

/** Names are unique per workspace among live instances, ignoring letter case. */
const nameTaken = (name: string, takenNames: readonly string[]) => {
  const folded = name.toLowerCase();
  return takenNames.some((taken) => taken.toLowerCase() === folded);
};

export const nameError = (name: string, takenNames: readonly string[]) => {
  if (!name) return "请输入实例名称";
  if (runeLength(name) > MAX_NAME_LENGTH) return `名称不能超过 ${MAX_NAME_LENGTH} 个字符`;
  if (nameTaken(name, takenNames)) return "已有同名实例（不区分大小写）";
  return "";
};

export const suggestCopyName = (source: string, takenNames: readonly string[]) => {
  const base = [...source.trim()].slice(0, MAX_NAME_LENGTH - 6).join("");
  for (let index = 1; index < 100; index += 1) {
    const candidate = index === 1 ? `${base} 副本` : `${base} 副本 ${index}`;
    if (!nameTaken(candidate, takenNames)) return candidate;
  }
  return `${base} 副本`;
};

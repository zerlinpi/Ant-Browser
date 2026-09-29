const profileStatusLabels: Record<string, string> = { active: "正常", syncing: "同步中", conflict: "有冲突" };
const revisionStatusLabels: Record<string, string> = { uploading: "上传中", committed: "已提交", superseded: "已废弃" };
const revisionModeLabels: Record<string, string> = { snapshot: "全量快照", incremental: "增量" };
const conflictStatusLabels: Record<string, string> = { open: "待处理", resolved: "已解决" };
const resolutionLabels: Record<string, string> = { keep_local: "保留本地版本", keep_remote: "保留云端版本" };

const lookup = (labels: Record<string, string>, value: string | undefined, fallback: string) =>
  value ? labels[value.toLowerCase()] ?? value : fallback;

export const profileStatusLabel = (value?: string) => lookup(profileStatusLabels, value, "未知");
export const revisionStatusLabel = (value?: string) => lookup(revisionStatusLabels, value, "未知");
export const revisionModeLabel = (value?: string) => lookup(revisionModeLabels, value, "—");
export const conflictStatusLabel = (value?: string) => lookup(conflictStatusLabels, value, "未知");
export const conflictResolutionLabel = (value?: string) => lookup(resolutionLabels, value, "—");

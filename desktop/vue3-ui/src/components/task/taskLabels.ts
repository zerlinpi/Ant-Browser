/** Task statuses written by server/services/task-service and its repositories. */
export const taskStatuses = ["queued", "leased", "running", "succeeded", "failed", "dead_letter", "cancelled"] as const;

const statusNames: Record<string, string> = {
  queued: "排队中",
  leased: "已领取",
  running: "运行中",
  succeeded: "已成功",
  failed: "失败",
  dead_letter: "重试耗尽",
  // Paired with the "终止" action so it never reads like the dialog's "取消".
  cancelled: "已终止",
};

const typeNames: Record<string, string> = {
  "workflow.execute": "工作流执行",
  "profile.sync": "档案同步",
  "proxy.health_check": "代理健康检查",
  "notification.deliver": "通知投递",
  "analytics.rollup": "统计汇总",
  "system.healthcheck": "系统自检",
};

const typeIcons: Record<string, string> = {
  "workflow.execute": "lucide:workflow",
  "profile.sync": "lucide:refresh-ccw",
  "proxy.health_check": "lucide:gauge",
  "notification.deliver": "lucide:bell",
  "analytics.rollup": "lucide:chart-column",
  "system.healthcheck": "lucide:activity",
};

export const taskStatusLabel = (status?: string) => (status ? statusNames[status] ?? status : "未知");
export const taskTypeLabel = (type?: string) => (type ? typeNames[type] ?? type : "未知任务");
export const taskTypeIcon = (type?: string) => (type ? typeIcons[type] : undefined) ?? "lucide:list-checks";

/** The server rejects cancellation of succeeded, failed and dead_letter tasks (task_state_conflict). */
export const isTaskCancellable = (status?: string) => status === "queued" || status === "leased" || status === "running";

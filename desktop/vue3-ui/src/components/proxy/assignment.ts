import type { ProxyAssignment, ProxyAssignmentTargetType } from "@/types";

export type AssignmentMode = "assign" | "unassign";

export interface AssignmentResult {
  mode: AssignmentMode;
  assignment: ProxyAssignment;
  targetName: string;
}

/** Assignments are unique per target (one proxy per instance, account or profile). */
export const assignmentKey = (targetType: ProxyAssignmentTargetType | string, targetId: string) => `${targetType}:${targetId}`;

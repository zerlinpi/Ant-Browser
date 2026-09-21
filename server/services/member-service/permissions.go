package memberservice

import "strings"

type Role string
type Permission string

const (
	RoleOwner    Role = "owner"
	RoleAdmin    Role = "admin"
	RoleManager  Role = "manager"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

const (
	PermissionWorkspaceRead     Permission = "workspace.read"
	PermissionWorkspaceUpdate   Permission = "workspace.update"
	PermissionMemberRead        Permission = "member.read"
	PermissionMemberInvite      Permission = "member.invite"
	PermissionMemberManage      Permission = "member.manage"
	PermissionInstanceRead      Permission = "instance.read"
	PermissionInstanceCreate    Permission = "instance.create"
	PermissionInstanceUpdate    Permission = "instance.update"
	PermissionInstanceOperate   Permission = "instance.operate"
	PermissionInstanceDelete    Permission = "instance.delete"
	PermissionProfileRead       Permission = "profile.read"
	PermissionProfileManage     Permission = "profile.manage"
	PermissionProfileSync       Permission = "profile.sync"
	PermissionFingerprintRead   Permission = "fingerprint.read"
	PermissionFingerprintManage Permission = "fingerprint.manage"
	PermissionAccountRead       Permission = "account.read"
	PermissionAccountManage     Permission = "account.manage"
	PermissionProxyRead         Permission = "proxy.read"
	PermissionProxyManage       Permission = "proxy.manage"
	PermissionWorkflowRead      Permission = "workflow.read"
	PermissionWorkflowManage    Permission = "workflow.manage"
	PermissionTaskOperate       Permission = "task.operate"
	PermissionAnalyticsRead     Permission = "analytics.read"
	PermissionBillingRead       Permission = "billing.read"
	PermissionBillingManage     Permission = "billing.manage"
	PermissionAuditRead         Permission = "audit.read"
)

var rolePermissions = map[Role]map[Permission]struct{}{
	RoleOwner: permissionSet(
		PermissionWorkspaceRead, PermissionWorkspaceUpdate,
		PermissionMemberRead, PermissionMemberInvite, PermissionMemberManage,
		PermissionInstanceRead, PermissionInstanceCreate, PermissionInstanceUpdate, PermissionInstanceOperate, PermissionInstanceDelete,
		PermissionProfileRead, PermissionProfileManage, PermissionProfileSync, PermissionFingerprintRead, PermissionFingerprintManage,
		PermissionAccountRead, PermissionAccountManage, PermissionProxyRead, PermissionProxyManage,
		PermissionWorkflowRead, PermissionWorkflowManage, PermissionTaskOperate,
		PermissionAnalyticsRead, PermissionBillingRead, PermissionBillingManage, PermissionAuditRead,
	),
	RoleAdmin: permissionSet(
		PermissionWorkspaceRead, PermissionWorkspaceUpdate,
		PermissionMemberRead, PermissionMemberInvite, PermissionMemberManage,
		PermissionInstanceRead, PermissionInstanceCreate, PermissionInstanceUpdate, PermissionInstanceOperate, PermissionInstanceDelete,
		PermissionProfileRead, PermissionProfileManage, PermissionProfileSync, PermissionFingerprintRead, PermissionFingerprintManage,
		PermissionAccountRead, PermissionAccountManage, PermissionProxyRead, PermissionProxyManage,
		PermissionWorkflowRead, PermissionWorkflowManage, PermissionTaskOperate,
		PermissionAnalyticsRead, PermissionBillingRead, PermissionAuditRead,
	),
	RoleManager: permissionSet(
		PermissionWorkspaceRead, PermissionMemberRead, PermissionMemberInvite,
		PermissionInstanceRead, PermissionInstanceCreate, PermissionInstanceUpdate, PermissionInstanceOperate,
		PermissionProfileRead, PermissionProfileManage, PermissionProfileSync, PermissionFingerprintRead, PermissionFingerprintManage,
		PermissionAccountRead, PermissionAccountManage, PermissionProxyRead, PermissionProxyManage,
		PermissionWorkflowRead, PermissionWorkflowManage, PermissionTaskOperate,
		PermissionAnalyticsRead, PermissionAuditRead,
	),
	RoleOperator: permissionSet(
		PermissionWorkspaceRead, PermissionMemberRead,
		PermissionInstanceRead, PermissionInstanceOperate,
		PermissionProfileRead, PermissionProfileSync, PermissionFingerprintRead,
		PermissionAccountRead, PermissionProxyRead, PermissionWorkflowRead, PermissionTaskOperate,
	),
	RoleViewer: permissionSet(
		PermissionWorkspaceRead, PermissionMemberRead, PermissionInstanceRead,
		PermissionProfileRead, PermissionFingerprintRead,
		PermissionAccountRead, PermissionProxyRead, PermissionWorkflowRead, PermissionAnalyticsRead,
	),
}

func ParseRole(value string) (Role, bool) {
	role := Role(strings.ToLower(strings.TrimSpace(value)))
	_, ok := rolePermissions[role]
	return role, ok
}

func HasPermission(role Role, permission Permission) bool {
	permissions, ok := rolePermissions[role]
	if !ok {
		return false
	}
	_, ok = permissions[permission]
	return ok
}

func permissionSet(values ...Permission) map[Permission]struct{} {
	result := make(map[Permission]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

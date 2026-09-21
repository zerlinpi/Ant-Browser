package memberservice

import "testing"

func TestRolePermissionMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		role       Role
		permission Permission
		allowed    bool
	}{
		{RoleOwner, PermissionBillingManage, true},
		{RoleAdmin, PermissionMemberManage, true},
		{RoleAdmin, PermissionBillingManage, false},
		{RoleManager, PermissionInstanceCreate, true},
		{RoleManager, PermissionInstanceDelete, false},
		{RoleOperator, PermissionInstanceOperate, true},
		{RoleOperator, PermissionInstanceCreate, false},
		{RoleViewer, PermissionAnalyticsRead, true},
		{RoleViewer, PermissionInstanceOperate, false},
		{Role("unknown"), PermissionWorkspaceRead, false},
	}
	for _, test := range tests {
		if actual := HasPermission(test.role, test.permission); actual != test.allowed {
			t.Fatalf("role %q permission %q: got %v, want %v", test.role, test.permission, actual, test.allowed)
		}
	}
}

func TestParseRoleIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	role, ok := ParseRole("  MANAGER ")
	if !ok || role != RoleManager {
		t.Fatalf("ParseRole returned %q, %v", role, ok)
	}
}

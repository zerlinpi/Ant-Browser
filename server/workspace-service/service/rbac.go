package service

const (
	RoleOwner    = "Owner"
	RoleAdmin    = "Admin"
	RoleManager  = "Manager"
	RoleOperator = "Operator"
	RoleViewer   = "Viewer"
)

func HasPermission(role string, permission string) bool {
	return role != ""
}

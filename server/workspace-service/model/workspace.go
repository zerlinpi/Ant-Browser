package model

import "time"

type Workspace struct {
	ID        uint      `json:"id"`
	Name      string    `json:"name"`
	OwnerID   uint      `json:"owner_id"`
	CreatedAt time.Time `json:"created_at"`
}

type WorkspaceMember struct {
	ID          uint   `json:"id"`
	WorkspaceID uint   `json:"workspace_id"`
	UserID      uint   `json:"user_id"`
	Role        string `json:"role"`
}

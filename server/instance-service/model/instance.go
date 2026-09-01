package model

import "time"

type BrowserInstance struct {
	ID          uint      `json:"id"`
	WorkspaceID uint      `json:"workspace_id"`
	ProfileID   string    `json:"profile_id"`
	DeviceID    string    `json:"device_id"`
	Status      string    `json:"status"`
	LastOnline  time.Time `json:"last_online"`
}

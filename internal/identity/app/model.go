package app

import "time"

// Member mirrors the framework-owned member row (framework migration 003);
// orkome reads it only to gate product routes on workspace membership.
type Member struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

// Pin is a sidebar pin (product-owned, migration 1023).
type Pin struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	ItemType    string    `json:"item_type"`
	ItemID      string    `json:"item_id"`
	Position    int       `json:"position"`
	CreatedAt   time.Time `json:"created_at"`
}

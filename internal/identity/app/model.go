package app

import "time"

type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
	Language    string    `json:"language"`
	Timezone    string    `json:"timezone"`
	CreatedAt   time.Time `json:"created_at"`
}

type Workspace struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Slug            string    `json:"slug"`
	WorkItemCounter int64     `json:"work_item_counter"`
	CreatedAt       time.Time `json:"created_at"`
}

type Member struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

type Membership struct {
	Member
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

type Invitation struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	WorkspaceName string    `json:"workspace_name"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	Status        string    `json:"status"`
	ExpiresAt     time.Time `json:"expires_at"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}

type ShareLink struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	WorkspaceName string    `json:"workspace_name"`
	Code          string    `json:"code"`
	Role          string    `json:"role"`
	MaxUses       int       `json:"max_uses"`
	Uses          int       `json:"uses"`
	ExpiresAt     time.Time `json:"expires_at"`
	CreatedAt     time.Time `json:"created_at"`
}

type PAT struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type PendingCode struct {
	ID        string
	CodeHash  string
	Attempts  int
	ExpiresAt time.Time
	CreatedAt time.Time
}

type Pin struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	ItemType    string    `json:"item_type"`
	ItemID      string    `json:"item_id"`
	Position    int       `json:"position"`
	CreatedAt   time.Time `json:"created_at"`
}

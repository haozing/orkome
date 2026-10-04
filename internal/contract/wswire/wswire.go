package wswire

import "encoding/json"

type Frame struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	EventID string          `json:"event_id,omitempty"`
}

const (
	CtrlAuth       = "auth"
	CtrlSubscribe  = "subscribe"
	CtrlUnsub      = "unsubscribe"
	ConfirmPrefix  = "confirmed:"
	RejectedPrefix = "rejected:"
)

const (
	ScopeWorkspace = "workspace"
	ScopeUser      = "user"
	ScopeTask      = "task"
	ScopeChat      = "chat"
)

const (
	CapTask         = "task"
	CapWorkItem     = "work_item"
	CapComment      = "comment"
	CapBatch        = "batch"
	CapChat         = "chat"
	CapAgent        = "agent"
	CapNotification = "notification"
	CapWorkspace    = "workspace"

	CapAutopilot = "autopilot"
	CapSquad     = "squad"
)

func Capabilities() []string {
	return []string{CapTask, CapWorkItem, CapComment, CapBatch, CapChat, CapAgent,
		CapNotification, CapWorkspace, CapAutopilot, CapSquad}
}

const ConnectedFrameType = "connected"

func ScopeKey(prefix, id string) string { return prefix + ":" + id }

func WSName(dotEvent string) string {
	for i := 0; i < len(dotEvent); i++ {
		if dotEvent[i] == '.' {
			return dotEvent[:i] + ":" + dotEvent[i+1:]
		}
	}
	return dotEvent
}

type ChatDonePayload struct {
	SessionID string `json:"session_id"`
	TaskID    string `json:"task_id"`
	Role      string `json:"role"`
	Kind      string `json:"kind"`
	Content   string `json:"content"`
}

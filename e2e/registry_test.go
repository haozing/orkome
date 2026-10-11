package e2e

import "net/http"

type authLevel string

const (
	authPublic authLevel = "public"
	authUser   authLevel = "user"
	authMember authLevel = "workspace-member"
	authSelf   authLevel = "self-auth"
)

type iface struct {
	Method string
	Path   string
	Domain string
	Auth   authLevel

	Body func() any

	Setup func(w *world) string

	WantStatus int

	MissStatus int

	AnonExpect int

	ListLike bool

	Hdr map[string]string

	// Client overrides the positive-path actor (default "owner"); used by
	// routes that destroy their own session (logout).
	Client string

	SkipBadBody bool
}

var registry = []iface{

	{Method: http.MethodPost, Path: "/auth/send-code", Domain: "identity", Auth: authPublic,
		Body: func() any { return map[string]string{"email": "l1send@e2e.test"} }, WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/auth/verify-code", Domain: "identity", Auth: authPublic},
	{Method: http.MethodGet, Path: "/config", Domain: "identity", Auth: authPublic},

	{Method: http.MethodPost, Path: "/api/workspaces", Domain: "identity", Auth: authSelf,
		Body:       func() any { return map[string]string{"name": "L1-WS", "slug": "l1ws"} },
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/workspaces", Domain: "identity", Auth: authSelf,
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/workspaces/{workspaceId}/invitations", Domain: "identity", Auth: authSelf,
		Body:       func() any { return map[string]string{"email": "invite@l1.e2e", "role": "member"} },
		Setup:      func(w *world) string { return "/api/workspaces/" + w.str("wsID") + "/invitations" },
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/workspaces/{workspaceId}/members", Domain: "identity", Auth: authSelf,
		Setup:      func(w *world) string { return "/api/workspaces/" + w.str("wsID") + "/members" },
		WantStatus: http.StatusOK},
	{Method: http.MethodDelete, Path: "/api/workspaces/{workspaceId}/members/{memberId}", Domain: "identity", Auth: authSelf},
	{Method: http.MethodPost, Path: "/api/invitations/{invitationId}/accept", Domain: "identity", Auth: authSelf,
		AnonExpect: http.StatusUnauthorized}, // framework semantics: plain 401 (old local face answered 404)
	{Method: http.MethodPost, Path: "/api/share-links/redeem", Domain: "identity", Auth: authSelf},

	{Method: http.MethodPost, Path: "/api/work-items", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]string{"title": "L1 template case", "status": "todo"} },
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/work-items", Domain: "work", Auth: authMember,
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/comments", Domain: "work", Auth: authMember,
		Body: func() any { return map[string]string{"content": "L1 comment"} },
		Setup: func(w *world) string {
			return "/api/work-items/" + w.ensureWorkItem() + "/comments"
		},
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/comments", Domain: "work", Auth: authMember,
		ListLike: true,
		Setup: func(w *world) string {
			return "/api/work-items/" + w.ensureWorkItem() + "/comments"
		},
		WantStatus: http.StatusOK},
}

var registryP2 = []iface{
	{Method: http.MethodDelete, Path: "/api/comments/{commentId}", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/comments/{commentId}/keep-replies", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/comments/{commentId}/reactions", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/comments/{commentId}/resolve", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/labels/{labelId}", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/pins/{itemType}/{itemId}", Domain: "identity", Auth: authSelf},
	{Method: http.MethodDelete, Path: "/api/projects/{projectId}", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/projects/{projectId}/resources/{resourceId}", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/properties/{key}", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/quick-actions/{quickActionId}", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/work-items/{workItemId}/labels/{labelId}", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/work-items/{workItemId}/metadata/{key}", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/work-items/{workItemId}/properties/{key}", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/work-items/{workItemId}/reactions", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/work-items/{workItemId}/wakeups/{wakeupId}", Domain: "work", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/workspaces/{workspaceId}", Domain: "identity", Auth: authSelf},
	{Method: http.MethodDelete, Path: "/api/workspaces/{workspaceId}/invitations/{invitationId}", Domain: "identity", Auth: authSelf},
	{Method: http.MethodDelete, Path: "/api/workspaces/{workspaceId}/mcp-servers/{serverId}", Domain: "identity", Auth: authSelf,
		MissStatus: http.StatusForbidden},
	{Method: http.MethodDelete, Path: "/api/workspaces/{workspaceId}/share-links/{linkId}", Domain: "identity", Auth: authSelf},
	{Method: http.MethodGet, Path: "/api/batches/{batchId}/items", Domain: "work", Auth: authMember, ListLike: true},
	{Method: http.MethodGet, Path: "/api/comments/{commentId}/sub-work-item-preview", Domain: "work", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/projects/search", Domain: "work", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/work-item-wakeup-paused", Domain: "work", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/work-item-wakeup-summaries", Domain: "work", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/work-items/child-progress", Domain: "work", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/active-task", Domain: "work", Auth: authMember, ListLike: true},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/attachments", Domain: "work", Auth: authMember, ListLike: true},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/batches", Domain: "work", Auth: authMember, ListLike: true},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/properties/{key}", Domain: "work", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/system-wakeups", Domain: "work", Auth: authMember, ListLike: true},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/tasks", Domain: "work", Auth: authMember, ListLike: true},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/wakeups/{wakeupId}/tasks", Domain: "work", Auth: authMember},
	{Method: http.MethodPatch, Path: "/api/quick-actions/{quickActionId}", Domain: "work", Auth: authMember},
	{Method: http.MethodPatch, Path: "/api/work-item-statuses/{statusKey}", Domain: "work", Auth: authMember},
	{Method: http.MethodPatch, Path: "/api/workspaces/{workspaceId}/members/{memberId}", Domain: "identity", Auth: authSelf},
	{Method: http.MethodPost, Path: "/api/batches/{batchId}/cancel", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/batches/{batchId}/pause", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/batches/{batchId}/resume", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/batches/{batchId}/retry-failed", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/comments/{commentId}/reactions", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/comments/{commentId}/sub-work-items", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/projects/{projectId}/resources", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-item-statuses/reorder", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/batch-delete", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/preview-trigger", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/table/facets", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/table/groups", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/comments/trigger-preview", Domain: "work", Auth: authMember, MissStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/quick-actions/{quickActionId}/render", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/quick-actions/{quickActionId}/run", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/rerun", Domain: "work", Auth: authMember, MissStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/squad-evaluated", Domain: "work", Auth: authMember, MissStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/subscribers", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/system-wakeups", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/tasks/{taskId}/cancel", Domain: "work", Auth: authMember, MissStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/tasks/{taskId}/supplements", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/tasks/{taskId}/supplements/{commentId}/retry", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/wakeups", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/wakeups/{wakeupId}/checkin", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/wakeups/{wakeupId}/trigger", Domain: "work", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/workspaces/{workspaceId}/leave", Domain: "identity", Auth: authSelf},
	{Method: http.MethodPost, Path: "/api/workspaces/{workspaceId}/members", Domain: "identity", Auth: authSelf},
	{Method: http.MethodPost, Path: "/cli-token", Domain: "identity", Auth: authPublic},
	{Method: http.MethodPut, Path: "/api/pins/reorder", Domain: "identity", Auth: authSelf},
	{Method: http.MethodPut, Path: "/api/projects/{projectId}/resources/{resourceId}", Domain: "work", Auth: authMember},
	{Method: http.MethodPut, Path: "/api/work-items/{workItemId}/wakeups/{wakeupId}", Domain: "work", Auth: authMember},
}

var registryP3 = []iface{

	{Method: http.MethodPost, Path: "/api/client-usage", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]string{"install_id": "00000000-0000-0000-0000-00000000c2e9"} },
		Hdr:        map[string]string{"X-Client-Platform": "web", "X-Client-Version": "e2e", "X-Client-OS": "windows"},
		WantStatus: http.StatusNoContent},
	{Method: http.MethodDelete, Path: "/api/agents/{agentId}/labels/{labelId}", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/agents/{agentId}/mcp-servers/{serverId}", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/agents/{agentId}/skills/{skillId}", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/autopilots/{autopilotId}", Domain: "automation", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/autopilots/{autopilotId}/collaborators", Domain: "automation", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/autopilots/{autopilotId}/triggers/{triggerId}", Domain: "automation", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/chat/pinned-agents", Domain: "conversation", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/chat/sessions/{sessionId}", Domain: "conversation", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/chat/sessions/{sessionId}/draft-restores", Domain: "conversation", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/chat/sessions/{sessionId}/pending-task", Domain: "conversation", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/chat/sessions/{sessionId}/queued-tasks", Domain: "conversation", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/skills/{skillId}", Domain: "skills", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/skills/{skillId}/files/{fileId}", Domain: "skills", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/skills/{skillId}/labels/{labelId}", Domain: "skills", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/squads/{squadId}", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodDelete, Path: "/api/squads/{squadId}/members/{agentId}", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/agent-builder/sessions/{sessionId}/draft", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/agents/{agentId}/labels", Domain: "orchestration", Auth: authMember, ListLike: true},
	{Method: http.MethodGet, Path: "/api/agents/{agentId}/tasks", Domain: "orchestration", Auth: authMember, ListLike: true},
	{Method: http.MethodGet, Path: "/api/autopilots/{autopilotId}/runs", Domain: "automation", Auth: authMember, ListLike: true},
	{Method: http.MethodGet, Path: "/api/autopilots/{autopilotId}/runs/{runId}", Domain: "automation", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/chat/pending-tasks/has-any", Domain: "conversation", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/chat/pinned-agents", Domain: "conversation", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/chat/sessions/{sessionId}/draft-restores", Domain: "conversation", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/chat/sessions/{sessionId}/pending-task", Domain: "conversation", Auth: authMember, ListLike: true},
	{Method: http.MethodGet, Path: "/api/deliveries/{deliveryId}", Domain: "automation", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/notifications", Domain: "notification", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/squads/{squadId}/members/status", Domain: "orchestration", Auth: authMember, ListLike: true},
	{Method: http.MethodGet, Path: "/api/tasks/{taskId}/interrupt", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodGet, Path: "/api/tasks/{taskId}/messages", Domain: "orchestration", Auth: authMember, ListLike: true},
	{Method: http.MethodPatch, Path: "/api/autopilots/{autopilotId}/triggers/{triggerId}", Domain: "automation", Auth: authMember},
	{Method: http.MethodPatch, Path: "/api/squads/{squadId}/members/{agentId}", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/agents", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/agents/{agentId}/labels", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/autopilots", Domain: "automation", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/autopilots/{autopilotId}/collaborators", Domain: "automation", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/autopilots/{autopilotId}/trigger", Domain: "automation", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/autopilots/{autopilotId}/triggers/{triggerId}/rotate-webhook-token", Domain: "automation", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/chat/pinned-agents", Domain: "conversation", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/chat/sessions", Domain: "conversation", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/chat/sessions/{sessionId}/messages", Domain: "conversation", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/chat/sessions/{sessionId}/queued-tasks/{taskId}/prioritize", Domain: "conversation", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/chat/sessions/{sessionId}/unarchive", Domain: "conversation", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/chat/sessions/{sessionId}/unpin", Domain: "conversation", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/deliveries/{deliveryId}/replay", Domain: "automation", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/notifications/archive-all", Domain: "notification", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/notifications/archive-all-read", Domain: "notification", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/notifications/{itemId}/archive", Domain: "notification", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/notifications/{itemId}/read", Domain: "notification", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/notifications/{itemId}/unarchive", Domain: "notification", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/notifications/{itemId}/unread", Domain: "notification", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/skills", Domain: "skills", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/skills/import", Domain: "skills", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/skills/{skillId}/labels", Domain: "skills", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/skills/{skillId}/refresh", Domain: "skills", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/squads/{squadId}/members", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/tasks/{taskId}/approve", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/tasks/{taskId}/cancel", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/tasks/{taskId}/deny", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodPost, Path: "/api/tasks/{taskId}/retry-source-context", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodPut, Path: "/api/agent-builder/sessions/{sessionId}/draft", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodPut, Path: "/api/agents/{agentId}/mcp-servers/{serverId}", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodPut, Path: "/api/agents/{agentId}/skills", Domain: "orchestration", Auth: authMember},
	{Method: http.MethodPut, Path: "/api/autopilots/{autopilotId}/triggers/{triggerId}/signing-secret", Domain: "automation", Auth: authMember},
	{Method: http.MethodPut, Path: "/api/skills/{skillId}/files", Domain: "skills", Auth: authMember},
	{Method: http.MethodPut, Path: "/api/squads/{squadId}", Domain: "orchestration", Auth: authMember},
}

func allIfaces() []iface {
	all := append([]iface{}, registry...)
	all = append(all, registryP2...)
	all = append(all, registryP3...)
	all = append(all, registryPos...)
	all = append(all, registryPos2...)
	all = append(all, registryPos3...)
	return append(all, registryPos4...)
}

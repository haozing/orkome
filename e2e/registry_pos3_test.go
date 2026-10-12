package e2e

import (
	"encoding/json"
	"net/http"
)

var registryPos3 = []iface{

	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/move", Domain: "work", Auth: authMember,
		Body:        func() any { return map[string]any{} },
		Setup:       func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/move" },
		WantStatus:  http.StatusNoContent,
		SkipBadBody: true},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/reactions", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]string{"emoji": "🎯"} },
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/reactions" },
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/duplicates", Domain: "work", Auth: authMember,

		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/duplicates" },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/usage", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/usage" },
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/work-items/quick-create", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]string{"title": "quick-create item"} },
		WantStatus: http.StatusCreated},
	{Method: http.MethodPost, Path: "/api/work-items/batch-update", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]any{"ids": []string{gwi()}, "patch": map[string]any{"priority": 1}} },
		WantStatus: http.StatusOK, SkipBadBody: true},

	{Method: http.MethodPut, Path: "/api/work-items/{workItemId}/properties/{key}", Domain: "work", Auth: authMember,

		Body: func() any { return json.RawMessage(`"v1"`) },
		Setup: func(w *world) string {
			return "/api/work-items/" + w.ensureWorkItem() + "/properties/" + w.ensurePropDef()
		},
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/properties/{key}", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/properties/" + w.ensurePropDef() },
		WantStatus: http.StatusOK},
	{Method: http.MethodPatch, Path: "/api/properties/{key}", Domain: "work", Auth: authMember,
		Body:        func() any { return map[string]string{"name": "rename property"} },
		Setup:       func(w *world) string { return "/api/properties/" + w.ensurePropDef() },
		WantStatus:  http.StatusOK,
		SkipBadBody: true},

	{Method: http.MethodGet, Path: "/api/quick-actions", Domain: "work", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/quick-actions", Domain: "conversation", Auth: authMember,
		Body:       func() any { return map[string]string{"label": "recipe shortcut", "prompt": "do something"} },
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/work-item-view-preferences", Domain: "work", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodPut, Path: "/api/work-item-view-preferences", Domain: "work", Auth: authMember,
		Body:        func() any { return map[string]any{} },
		WantStatus:  http.StatusOK,
		SkipBadBody: true},

	{Method: http.MethodGet, Path: "/api/workspaces/{workspaceId}/mcp-servers", Domain: "identity", Auth: authSelf,
		Setup:      func(w *world) string { return "/api/workspaces/" + w.str("wsID") + "/mcp-servers" },
		MissStatus: http.StatusForbidden,
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/workspaces/{workspaceId}/mcp-servers", Domain: "identity", Auth: authSelf,
		Body:       func() any { return map[string]string{"name": "recipe ws-mcp", "url": "https://127.0.0.1:19999/sse"} },
		Setup:      func(w *world) string { return "/api/workspaces/" + w.str("wsID") + "/mcp-servers" },
		MissStatus: http.StatusForbidden,
		WantStatus: http.StatusCreated},
	{Method: http.MethodPut, Path: "/api/workspaces/{workspaceId}/mcp-servers/{serverId}", Domain: "identity", Auth: authSelf,
		Body:  func() any { return map[string]string{"name": "rename ws-mcp", "url": "https://127.0.0.1:19999/sse"} },
		Setup: func(w *world) string { return "/api/workspaces/" + w.str("wsID") + "/mcp-servers/" + w.ensureWsMcp() },

		MissStatus:  http.StatusForbidden,
		WantStatus:  http.StatusOK,
		SkipBadBody: true},
	{Method: http.MethodPost, Path: "/api/invitations/{invitationId}/decline", Domain: "identity", Auth: authSelf,
		Setup: func(w *world) string { return "/api/invitations/" + w.ensureInvitation() }},

	{Method: http.MethodGet, Path: "/api/agents/{agentId}/env", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/agents/" + w.ensureAgent() + "/env" },
		WantStatus: http.StatusOK},
	{Method: http.MethodPut, Path: "/api/agents/{agentId}/env", Domain: "orchestration", Auth: authMember,
		Body:        func() any { return map[string]any{"vars": map[string]string{"k": "v"}} },
		Setup:       func(w *world) string { return "/api/agents/" + w.ensureAgent() + "/env" },
		WantStatus:  http.StatusOK,
		SkipBadBody: true},
	{Method: http.MethodPost, Path: "/api/agents/{agentId}/cancel-tasks", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/agents/" + w.ensureAgent() + "/cancel-tasks" },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/agents/{agentId}/mcp-servers", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/agents/" + w.ensureAgent() + "/mcp-servers" },
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/agents/{agentId}/mcp-servers", Domain: "orchestration", Auth: authMember,
		Body: func() any {
			return map[string]string{"name": "recipe agent-mcp", "url": "https://127.0.0.1:19999/sse"}
		},
		Setup:      func(w *world) string { return "/api/agents/" + w.ensureAgent() + "/mcp-servers" },
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/squads/{squadId}", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/squads/" + w.ensureSquad() },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/squads/{squadId}/members", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/squads/" + w.ensureSquad() + "/members" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodPost, Path: "/api/agent-builder/sessions", Domain: "orchestration", Auth: authMember,
		Body:        func() any { return map[string]any{} },
		WantStatus:  http.StatusCreated,
		SkipBadBody: true},
	{Method: http.MethodGet, Path: "/api/agent-builder/sessions", Domain: "orchestration", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/working-agents", Domain: "orchestration", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/agent-run-counts", Domain: "orchestration", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/agent-run-snapshot", Domain: "orchestration", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/agent-activity-30d", Domain: "orchestration", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/deliveries", Domain: "automation", Auth: authMember, WantStatus: http.StatusOK},

	{Method: http.MethodPost, Path: "/api/chat/sessions/{sessionId}/read", Domain: "conversation", Auth: authMember,
		Setup:      func(w *world) string { return "/api/chat/sessions/" + w.ensureChatSession() + "/read" },
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/chat/sessions/{sessionId}/archive", Domain: "conversation", Auth: authMember,
		Setup:      func(w *world) string { return "/api/chat/sessions/" + w.ensureChatSession() + "/archive" },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/chat/thread", Domain: "conversation", Auth: authMember,
		Setup:      func(w *world) string { return "/api/chat/thread?session_id=" + w.ensureChatSession() },
		WantStatus: http.StatusOK},

	{Method: http.MethodGet, Path: "/api/autopilots/{autopilotId}/collaborators", Domain: "automation", Auth: authMember,
		Setup:      func(w *world) string { return "/api/autopilots/" + w.ensureAutopilot() + "/collaborators" },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/system-wakeups", Domain: "automation", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodPut, Path: "/api/work-items/{workItemId}/wakeups/{wakeupId}/instruction", Domain: "automation", Auth: authMember,
		Body: func() any { return map[string]string{"instruction": "rewrite instruction"} },
		Setup: func(w *world) string {
			return "/api/work-items/" + w.ensureWorkItem() + "/wakeups/" + w.ensureWakeup() + "/instruction"
		},
		SkipBadBody: true, WantStatus: http.StatusNoContent},

	{Method: http.MethodGet, Path: "/api/skills/search", Domain: "skills", Auth: authMember,
		Setup:      func(w *world) string { return "/api/skills/search?q=recipe" },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/skills/{skillId}/files", Domain: "skills", Auth: authMember,
		Setup:      func(w *world) string { return "/api/skills/" + w.ensureSkill() + "/files" },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/skills/{skillId}/labels", Domain: "skills", Auth: authMember,
		Setup:      func(w *world) string { return "/api/skills/" + w.ensureSkill() + "/labels" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodPut, Path: "/api/agents/{agentId}/skills/{skillId}/enabled", Domain: "skills", Auth: authMember,
		Body: func() any { return map[string]bool{"enabled": true} },
		// FP-08 fence（wave1 FixF）后缺失 agent/skill 一律 404（不再无校验静默写）
		MissStatus: http.StatusNotFound,
		Setup: func(w *world) string {
			return "/api/agents/" + w.ensureAgent() + "/skills/" + w.ensureSkill() + "/enabled"
		},
		WantStatus: http.StatusOK},

	{Method: http.MethodGet, Path: "/api/notifications/facets", Domain: "notification", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/notifications/mark-all-read", Domain: "notification", Auth: authMember, WantStatus: http.StatusNoContent},
	{Method: http.MethodPatch, Path: "/api/notification-preferences", Domain: "notification", Auth: authMember,
		Body:        func() any { return map[string]any{} },
		WantStatus:  http.StatusOK,
		SkipBadBody: true},
}

var gPropDef = func() string { return posWorld.ensurePropDef() }

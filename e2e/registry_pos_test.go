package e2e

import "net/http"

var registryPos = []iface{

	{Method: http.MethodGet, Path: "/auth/me", Domain: "identity", Auth: authSelf, WantStatus: http.StatusOK},
	{Method: http.MethodPatch, Path: "/auth/me", Domain: "identity", Auth: authSelf,
		Body: func() any { return map[string]string{"display_name": "recipe user"} },

		SkipBadBody: true, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/tokens", Domain: "identity", Auth: authSelf, WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/tokens", Domain: "identity", Auth: authSelf,
		Body: func() any { return map[string]string{"name": "recipe token"} },

		Setup:       func(w *world) string { return "/api/tokens" },
		WantStatus:  http.StatusCreated,
		SkipBadBody: true},
	{Method: http.MethodDelete, Path: "/api/tokens/{tokenId}", Domain: "identity", Auth: authSelf,
		Setup: func(w *world) string { return "/api/tokens/" + w.ensureToken() }},

	{Method: http.MethodPost, Path: "/auth/logout", Domain: "identity", Auth: authSelf,
		AnonExpect: http.StatusUnauthorized, WantStatus: http.StatusOK, // framework logout REVOKES the session — run last
		Client: "logout", Setup: func(w *world) string { w.loginAs("logout", "bye@e2e.test"); return "/auth/logout" }},
	{Method: http.MethodPatch, Path: "/api/workspaces/{workspaceId}", Domain: "identity", Auth: authSelf,
		Body:        func() any { return map[string]string{"name": "rename workspace"} },
		Setup:       func(w *world) string { return "/api/workspaces/" + w.str("wsID") },
		SkipBadBody: true,
		WantStatus:  http.StatusOK},
	{Method: http.MethodGet, Path: "/api/workspaces/{workspaceId}/invitations", Domain: "identity", Auth: authSelf,
		Setup:      func(w *world) string { return "/api/workspaces/" + w.str("wsID") + "/invitations" },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/workspaces/{workspaceId}/share-links", Domain: "identity", Auth: authSelf,
		Setup:      func(w *world) string { return "/api/workspaces/" + w.str("wsID") + "/share-links" },
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/workspaces/{workspaceId}/share-links", Domain: "identity", Auth: authSelf,
		Body:       func() any { return map[string]any{"role": "member", "max_uses": 10, "ttl_hours": 24} }, // framework validates max_uses 1-1000
		Setup:      func(w *world) string { return "/api/workspaces/" + w.str("wsID") + "/share-links" },
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/invitations/mine", Domain: "identity", Auth: authSelf, WantStatus: http.StatusOK},

	{Method: http.MethodGet, Path: "/api/pins", Domain: "identity", Auth: authSelf, WantStatus: http.StatusOK},
	{Method: http.MethodPut, Path: "/api/pins", Domain: "identity", Auth: authSelf,
		Body:       func() any { return map[string]string{"item_type": "work_item", "item_id": gwi()} },
		WantStatus: http.StatusCreated},

	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}", Domain: "work", Auth: authMember,
		Setup:       func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() },
		SkipBadBody: true,
		WantStatus:  http.StatusOK},
	{Method: http.MethodPut, Path: "/api/work-items/{workItemId}", Domain: "work", Auth: authMember,
		Body:        func() any { return map[string]any{"priority": 3} },
		Setup:       func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() },
		SkipBadBody: true,
		WantStatus:  http.StatusOK},
	{Method: http.MethodDelete, Path: "/api/work-items/{workItemId}", Domain: "work", Auth: authMember,
		Setup: func(w *world) string {
			return "/api/work-items/" + w.ensureWIExplicit("dedicated to forward deletion")
		}},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/timeline", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/timeline" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/labels", Domain: "work", Auth: authMember,

		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/labels" },
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/labels", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]string{"label_id": glbl()} },
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/labels" },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/subscribers", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/subscribers" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/subscribers", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/subscribers" },
		WantStatus: http.StatusCreated},
	{Method: http.MethodPut, Path: "/api/work-items/{workItemId}/metadata/{key}", Domain: "work", Auth: authMember,
		Body:        func() any { return map[string]string{"value": "v1"} },
		Setup:       func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/metadata/recipe_key" },
		SkipBadBody: true,
		WantStatus:  http.StatusOK},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/metadata/{key}", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/metadata/absent_key" },
		MissStatus: http.StatusNotFound},

	{Method: http.MethodPost, Path: "/api/work-items/query", Domain: "work", Auth: authMember,
		Body: func() any { return map[string]any{"filter": map[string]any{"status": []string{"todo"}}} },

		WantStatus: http.StatusOK, SkipBadBody: true},
	{Method: http.MethodPost, Path: "/api/work-items/table/rows", Domain: "work", Auth: authMember,
		Body:        func() any { return map[string]any{} },
		WantStatus:  http.StatusOK,
		SkipBadBody: true},
	{Method: http.MethodGet, Path: "/api/work-items/search", Domain: "work", Auth: authMember,

		Setup:      func(w *world) string { return "/api/work-items/search?q=recipe" },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/work-items/grouped", Domain: "work", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/work-items/children", Domain: "work", Auth: authMember,

		Setup:      func(w *world) string { return "/api/work-items/children?parent=" + w.ensureWorkItem() },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/work-items/assignee-frequency", Domain: "work", Auth: authMember, WantStatus: http.StatusOK},

	{Method: http.MethodGet, Path: "/api/labels", Domain: "work", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/labels", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]string{"name": "positive signature"} },
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/labels/{labelId}", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/labels/" + w.ensureLabel() },
		WantStatus: http.StatusOK},
	{Method: http.MethodPut, Path: "/api/labels/{labelId}", Domain: "work", Auth: authMember,
		Body:  func() any { return map[string]string{"name": "rename signature"} },
		Setup: func(w *world) string { return "/api/labels/" + w.ensureLabel() },
		// FP-18 D-02 fix: PUT updates by path id — 200 on hit, 404 on miss
		// (the old by-name upsert answered 201/201).
		MissStatus: http.StatusNotFound, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/projects", Domain: "work", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/projects", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]string{"name": "positive project"} },
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/projects/{projectId}", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/projects/" + w.ensureProject() },
		WantStatus: http.StatusOK},
	{Method: http.MethodPut, Path: "/api/projects/{projectId}", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]string{"name": "rename project"} },
		Setup:      func(w *world) string { return "/api/projects/" + w.ensureProject() },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/projects/{projectId}/resources", Domain: "work", Auth: authMember,

		Setup:      func(w *world) string { return "/api/projects/" + w.ensureProject() + "/resources" },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/work-item-statuses", Domain: "work", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/work-item-statuses", Domain: "work", Auth: authMember,

		Body: func() any {
			return map[string]string{"key": "recipe_status", "name": "recipe status", "category": "unstarted"}
		},
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/work-item-views", Domain: "work", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodPut, Path: "/api/work-item-views", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]any{"name": "recipe view", "filter": map[string]any{}} },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/properties", Domain: "work", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/properties", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]any{"key": "recipe_prop", "type": "text"} },
		WantStatus: http.StatusCreated},

	{Method: http.MethodPut, Path: "/api/comments/{commentId}", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]string{"content": "rewrite comment"} },
		Setup:      func(w *world) string { return "/api/comments/" + w.ensureComment() },
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/comments/{commentId}/resolve", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/comments/" + w.ensureComment() + "/resolve" },
		WantStatus: http.StatusNoContent},
}

var (
	gwi  = func() string { return posWorld.ensureWorkItem() }
	glbl = func() string { return posWorld.ensureLabel() }
)

var registryPos2 = []iface{

	{Method: http.MethodGet, Path: "/api/agents", Domain: "orchestration", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/agents/{agentId}", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/agents/" + w.ensureAgent() },
		WantStatus: http.StatusOK},
	{Method: http.MethodPut, Path: "/api/agents/{agentId}", Domain: "orchestration", Auth: authMember,
		Body: func() any {
			return map[string]any{"name": gAgentRename(), "description": "recipe rewrite",
				"runtime_config": map[string]any{"provider": "fake", "model": "fake-1"}}
		},
		Setup:      func(w *world) string { return "/api/agents/" + w.ensureAgent() },
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/agents/{agentId}/archive", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/agents/" + w.ensureAgent() + "/archive" },
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/agents/{agentId}/restore", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/agents/" + w.ensureAgent() + "/restore" },
		WantStatus: http.StatusOK},

	{Method: http.MethodPost, Path: "/api/squads", Domain: "orchestration", Auth: authMember,
		Body:       func() any { return map[string]any{"name": "recipe squad"} },
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/squads", Domain: "orchestration", Auth: authMember, WantStatus: http.StatusOK},

	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/batches", Domain: "orchestration", Auth: authMember,
		Body: func() any {
			return map[string]any{"kind": "generic", "payloads": []map[string]any{{"title": "batch sub-items"}}}
		},
		Setup: func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/batches" },

		MissStatus: http.StatusNotFound, WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/batches/{batchId}", Domain: "orchestration", Auth: authMember,
		MissStatus: http.StatusNotFound,
		Setup:      func(w *world) string { return "/api/batches/" + gBatch() },
		WantStatus: http.StatusOK},

	{Method: http.MethodGet, Path: "/api/chat/sessions", Domain: "conversation", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/chat/sessions/{sessionId}", Domain: "conversation", Auth: authMember,
		Setup:       func(w *world) string { return "/api/chat/sessions/" + w.ensureChatSession() },
		SkipBadBody: true,
		WantStatus:  http.StatusOK},
	{Method: http.MethodPatch, Path: "/api/chat/sessions/{sessionId}", Domain: "conversation", Auth: authMember,
		Body:        func() any { return map[string]string{"title": "renamed session"} },
		Setup:       func(w *world) string { return "/api/chat/sessions/" + w.ensureChatSession() },
		SkipBadBody: true,
		WantStatus:  http.StatusOK},
	{Method: http.MethodGet, Path: "/api/chat/sessions/{sessionId}/messages", Domain: "conversation", Auth: authMember,

		Setup:      func(w *world) string { return "/api/chat/sessions/" + w.ensureChatSession() + "/messages" },
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/chat/sessions/{sessionId}/pin", Domain: "conversation", Auth: authMember,
		Setup:      func(w *world) string { return "/api/chat/sessions/" + w.ensureChatSession() + "/pin" },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/chat/pending-tasks", Domain: "conversation", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/chat/history", Domain: "conversation", Auth: authMember,

		Setup:      func(w *world) string { return "/api/chat/history?session_id=" + w.ensureChatSession() },
		WantStatus: http.StatusOK},

	{Method: http.MethodGet, Path: "/api/autopilots", Domain: "automation", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/autopilots/{autopilotId}", Domain: "automation", Auth: authMember,
		Setup:       func(w *world) string { return "/api/autopilots/" + w.ensureAutopilot() },
		SkipBadBody: true,
		WantStatus:  http.StatusOK},
	{Method: http.MethodPatch, Path: "/api/autopilots/{autopilotId}", Domain: "automation", Auth: authMember,
		Body:        func() any { return map[string]string{"name": "rename autopilot"} },
		Setup:       func(w *world) string { return "/api/autopilots/" + w.ensureAutopilot() },
		SkipBadBody: true,
		WantStatus:  http.StatusOK},
	{Method: http.MethodPost, Path: "/api/autopilots/cron-preview", Domain: "automation", Auth: authMember,
		Body:       func() any { return map[string]string{"cron_expression": "*/5 * * * *"} },
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/autopilots/{autopilotId}/triggers", Domain: "automation", Auth: authMember,
		Body: func() any {
			return map[string]string{"kind": "schedule", "cron_expression": "0 9 * * 1", "timezone": "UTC"}
		},
		Setup:      func(w *world) string { return "/api/autopilots/" + w.ensureAutopilot() + "/triggers" },
		WantStatus: http.StatusCreated},

	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/wakeups", Domain: "automation", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/wakeups" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/wakeups/{wakeupId}", Domain: "automation", Auth: authMember,
		Setup: func(w *world) string {
			return "/api/work-items/" + w.ensureWorkItem() + "/wakeups/" + w.ensureWakeup()
		},
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/wakeups/{wakeupId}/disable", Domain: "automation", Auth: authMember,
		Setup: func(w *world) string {
			return "/api/work-items/" + w.ensureWorkItem() + "/wakeups/" + w.ensureWakeup() + "/disable"
		},
		WantStatus: http.StatusNoContent},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/wakeups/{wakeupId}/enable", Domain: "automation", Auth: authMember,
		Setup: func(w *world) string {
			return "/api/work-items/" + w.ensureWorkItem() + "/wakeups/" + w.ensureWakeup() + "/enable"
		},
		WantStatus: http.StatusNoContent},
	{Method: http.MethodGet, Path: "/api/work-item-wakeups", Domain: "automation", Auth: authMember, WantStatus: http.StatusOK},

	{Method: http.MethodGet, Path: "/api/skills", Domain: "skills", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/skills/{skillId}", Domain: "skills", Auth: authMember,
		Setup:      func(w *world) string { return "/api/skills/" + w.ensureSkill() },
		WantStatus: http.StatusOK},
	{Method: http.MethodPut, Path: "/api/skills/{skillId}", Domain: "skills", Auth: authMember,
		Body:       func() any { return map[string]string{"description": "recipe rewrite"} },
		Setup:      func(w *world) string { return "/api/skills/" + w.ensureSkill() },
		WantStatus: http.StatusOK, SkipBadBody: true},
	{Method: http.MethodGet, Path: "/api/agents/{agentId}/skills", Domain: "skills", Auth: authMember,
		Setup:      func(w *world) string { return "/api/agents/" + w.ensureAgent() + "/skills" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodPost, Path: "/api/agents/{agentId}/skills/add", Domain: "skills", Auth: authMember,
		Body:  func() any { return map[string]string{"skill_id": gSkill()} },
		Setup: func(w *world) string { return "/api/agents/" + w.ensureAgent() + "/skills/add" },

		MissStatus: http.StatusNotFound, WantStatus: http.StatusOK},

	{Method: http.MethodGet, Path: "/api/notifications/archived", Domain: "notification", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/notifications/unread-count", Domain: "notification", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/notifications/summary", Domain: "notification", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/notification-preferences", Domain: "notification", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodPut, Path: "/api/notification-preferences", Domain: "notification", Auth: authMember,
		Body:        func() any { return map[string]any{} },
		WantStatus:  http.StatusOK,
		SkipBadBody: true},
}

var (
	gBatch       = func() string { return posWorld.ensureBatch() }
	gSkill       = func() string { return posWorld.ensureSkill() }
	gAgentRename = func() string { return "recipe rename-" + posWorld.str("wsID")[:8] }
)

package e2e

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"net/http"
)

func mkWI(w *world) string {
	rec := w.st.do("owner", http.MethodPost, "/api/work-items",
		map[string]string{"title": "final batch work item", "status": "todo"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeJSON(w.t, rec)["id"].(string)
}

func mkLabel(w *world, name string) string {
	rec := w.st.do("owner", http.MethodPost, "/api/labels", map[string]string{"name": name}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeJSON(w.t, rec)["id"].(string)
}

func mkProject(w *world) string {
	rec := w.st.do("owner", http.MethodPost, "/api/projects", map[string]string{"name": "final batch project"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeJSON(w.t, rec)["id"].(string)
}

func mkComment(w *world) string {
	rec := w.st.do("owner", http.MethodPost,
		fmt.Sprintf("/api/work-items/%s/comments", w.ensureWorkItem()),
		map[string]string{"content": "final batch comment"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeJSON(w.t, rec)["comment"].(map[string]any)["id"].(string)
}

func mkWakeup(w *world, wi string) string {
	rec := w.st.do("owner", http.MethodPost,
		fmt.Sprintf("/api/work-items/%s/wakeups", wi), map[string]any{
			"agent_id": w.ensureAgent(), "kind": "time", "instruction": "final batch rule",
			"timezone": "UTC", "enabled": true,
		}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeJSON(w.t, rec)["id"].(string)
}

func mkSkill(w *world) string {
	rec := w.st.do("owner", http.MethodPost, "/api/skills",
		map[string]string{"name": fmt.Sprintf("final-skill-%s", w.str("wsID")[:8]), "description": "final batch"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeJSON(w.t, rec)["id"].(string)
}

func mkAutopilot(w *world, name string) string {
	rec := w.st.do("owner", http.MethodPost, "/api/autopilots", map[string]any{
		"name": name, "exec_mode": "create_work_item",
		"target_agent_id": w.ensureAgent(), "template": map[string]any{"title": "final batch {{date}}"},
		"enabled": true,
	}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeJSON(w.t, rec)["id"].(string)
}

func mkQA(w *world) string {
	rec := w.st.do("owner", http.MethodPost, "/api/quick-actions",
		map[string]string{"label": "final batch shortcut", "prompt": "do work"}, nil)
	require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
	return decodeJSON(w.t, rec)["id"].(string)
}

var registryPos4 = []iface{

	{Method: http.MethodDelete, Path: "/api/labels/{labelId}", Domain: "work", Auth: authMember,
		Setup: func(w *world) string { return "/api/labels/" + mkLabel(w, "final batch delete signature") }},
	{Method: http.MethodDelete, Path: "/api/projects/{projectId}", Domain: "work", Auth: authMember,
		Setup: func(w *world) string { return "/api/projects/" + mkProject(w) }},
	{Method: http.MethodDelete, Path: "/api/properties/{key}", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/properties/" + w.ensurePropDef() },
		WantStatus: http.StatusNoContent},
	{Method: http.MethodDelete, Path: "/api/quick-actions/{quickActionId}", Domain: "conversation", Auth: authMember,
		Setup: func(w *world) string { return "/api/quick-actions/" + mkQA(w) }},
	{Method: http.MethodDelete, Path: "/api/comments/{commentId}", Domain: "work", Auth: authMember,
		Setup: func(w *world) string { return "/api/comments/" + mkComment(w) }},
	{Method: http.MethodDelete, Path: "/api/comments/{commentId}/keep-replies", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/comments/" + mkComment(w) + "/keep-replies" },
		WantStatus: http.StatusNoContent},
	{Method: http.MethodPost, Path: "/api/comments/{commentId}/reactions", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]string{"emoji": "✅"} },
		Setup:      func(w *world) string { return "/api/comments/" + w.ensureComment() + "/reactions" },
		WantStatus: http.StatusCreated},
	{Method: http.MethodDelete, Path: "/api/comments/{commentId}/reactions", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]string{"emoji": "✅"} },
		Setup:      func(w *world) string { return "/api/comments/" + w.ensureComment() + "/reactions" },
		WantStatus: http.StatusOK},
	{Method: http.MethodDelete, Path: "/api/comments/{commentId}/resolve", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/comments/" + w.ensureComment() + "/resolve" },
		WantStatus: http.StatusNoContent},
	{Method: http.MethodDelete, Path: "/api/work-items/{workItemId}", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + mkWI(w) },
		WantStatus: http.StatusNoContent},
	{Method: http.MethodDelete, Path: "/api/work-items/{workItemId}/labels/{labelId}", Domain: "work", Auth: authMember,
		Setup: func(w *world) string {
			wi := mkWI(w)
			lb := mkLabel(w, "final batch attach signature")
			rec := w.st.do("owner", http.MethodPost, fmt.Sprintf("/api/work-items/%s/labels", wi),
				map[string]string{"label_id": lb}, nil)
			require.Equal(w.t, http.StatusOK, rec.Code, rec.Body.String())
			return fmt.Sprintf("/api/work-items/%s/labels/%s", wi, lb)
		}},
	{Method: http.MethodPut, Path: "/api/work-items/{workItemId}/metadata/{key}", Domain: "work", Auth: authMember,
		Body:        func() any { return map[string]string{"k": "v"} },
		Setup:       func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/metadata/final_key" },
		SkipBadBody: true, WantStatus: http.StatusOK},
	{Method: http.MethodDelete, Path: "/api/work-items/{workItemId}/metadata/{key}", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/metadata/final_key" },
		WantStatus: http.StatusNoContent},
	{Method: http.MethodDelete, Path: "/api/work-items/{workItemId}/properties/{key}", Domain: "work", Auth: authMember,
		Setup: func(w *world) string {
			return "/api/work-items/" + w.ensureWorkItem() + "/properties/" + w.ensurePropDef()
		},
		WantStatus: http.StatusNoContent},
	{Method: http.MethodDelete, Path: "/api/work-items/{workItemId}/reactions", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]string{"emoji": "🎯"} },
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/reactions" },
		WantStatus: http.StatusOK},
	{Method: http.MethodDelete, Path: "/api/work-items/{workItemId}/wakeups/{wakeupId}", Domain: "automation", Auth: authMember,
		Setup: func(w *world) string {
			wi := mkWI(w)
			return fmt.Sprintf("/api/work-items/%s/wakeups/%s", wi, mkWakeup(w, wi))
		}},

	{Method: http.MethodGet, Path: "/api/projects/search", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/projects/search?q=final-batch" },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/work-item-wakeup-paused", Domain: "automation", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/work-item-wakeup-summaries", Domain: "automation", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/work-items/child-progress", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/child-progress?parent=" + w.ensureWorkItem() },
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/active-task", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/active-task" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/attachments", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/attachments" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/batches", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/batches" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodGet, Path: "/api/batches/{batchId}/items", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/batches/" + w.ensureBatch() + "/items" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/tasks", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/tasks" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/system-wakeups", Domain: "automation", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + w.ensureWorkItem() + "/system-wakeups" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodGet, Path: "/api/work-items/{workItemId}/wakeups/{wakeupId}/tasks", Domain: "automation", Auth: authMember,
		Setup: func(w *world) string {
			return fmt.Sprintf("/api/work-items/%s/wakeups/%s/tasks", w.ensureWorkItem(), w.ensureWakeup())
		},
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/work-item-statuses/reorder", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]any{"order": []string{"todo", "in_progress", "done"}} },
		WantStatus: http.StatusNoContent, SkipBadBody: true},
	{Method: http.MethodPost, Path: "/api/work-items/batch-delete", Domain: "work", Auth: authMember,
		Body:       func() any { return map[string]any{"ids": []string{gwi()}} },
		WantStatus: http.StatusOK, SkipBadBody: true},
	{Method: http.MethodPost, Path: "/api/work-items/table/facets", Domain: "work", Auth: authMember,
		Body:        func() any { return map[string]any{} },
		WantStatus:  http.StatusOK,
		SkipBadBody: true},
	{Method: http.MethodPost, Path: "/api/work-items/table/groups", Domain: "work", Auth: authMember,
		Body:        func() any { return map[string]any{"group_by": "status"} },
		WantStatus:  http.StatusOK,
		SkipBadBody: true},
	{Method: http.MethodPost, Path: "/api/work-items/{workItemId}/subscribers", Domain: "work", Auth: authMember,
		Setup:      func(w *world) string { return "/api/work-items/" + mkWI(w) + "/subscribers" },
		WantStatus: http.StatusCreated},

	{Method: http.MethodDelete, Path: "/api/pins/{itemType}/{itemId}", Domain: "identity", Auth: authSelf,
		Setup: func(w *world) string {
			wi := mkWI(w)
			rec := w.st.do("owner", http.MethodPut, "/api/pins",
				map[string]string{"item_type": "work_item", "item_id": wi}, nil)
			require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
			return "/api/pins/work_item/" + wi
		}},

	{Method: http.MethodDelete, Path: "/api/workspaces/{workspaceId}/invitations/{invitationId}", Domain: "identity", Auth: authSelf,
		Setup: func(w *world) string {
			return fmt.Sprintf("/api/workspaces/%s/invitations/%s", w.str("wsID"), w.ensureInvitation())
		}},
	{Method: http.MethodDelete, Path: "/api/workspaces/{workspaceId}/share-links/{linkId}", Domain: "identity", Auth: authSelf,
		Setup: func(w *world) string {
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/workspaces/%s/share-links", w.str("wsID")),
				map[string]string{"role": "member"}, nil)
			require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
			return fmt.Sprintf("/api/workspaces/%s/share-links/%s", w.str("wsID"), decodeJSON(w.t, rec)["id"].(string))
		}},
	{Method: http.MethodDelete, Path: "/api/workspaces/{workspaceId}/mcp-servers/{serverId}", Domain: "identity", Auth: authSelf,
		Setup: func(w *world) string {
			return fmt.Sprintf("/api/workspaces/%s/mcp-servers/%s", w.str("wsID"), w.ensureWsMcp())
		},
		MissStatus: http.StatusForbidden, WantStatus: http.StatusOK},

	{Method: http.MethodPost, Path: "/api/agents", Domain: "orchestration", Auth: authMember,
		Body: func() any {
			return map[string]any{"name": fmt.Sprintf("final batch agent-%s", gAgentRename()[:12]), "runtime_config": map[string]any{"provider": "fake", "model": "fake-1"}}
		},
		WantStatus: http.StatusCreated},
	{Method: http.MethodPost, Path: "/api/agents/{agentId}/labels", Domain: "orchestration", Auth: authMember,
		Body:       func() any { return map[string]string{"label_id": glbl()} },
		Setup:      func(w *world) string { return "/api/agents/" + w.ensureAgent() + "/labels" },
		MissStatus: http.StatusNotFound, WantStatus: http.StatusOK},
	{Method: http.MethodDelete, Path: "/api/agents/{agentId}/labels/{labelId}", Domain: "orchestration", Auth: authMember,
		Setup: func(w *world) string {
			lb := mkLabel(w, "final batch agent signature")
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/agents/%s/labels", w.ensureAgent()),
				map[string]string{"label_id": lb}, nil)
			require.Equal(w.t, http.StatusOK, rec.Code, rec.Body.String())
			return fmt.Sprintf("/api/agents/%s/labels/%s", w.ensureAgent(), lb)
		}},
	{Method: http.MethodDelete, Path: "/api/agents/{agentId}/mcp-servers/{serverId}", Domain: "orchestration", Auth: authMember,
		Setup: func(w *world) string {
			return fmt.Sprintf("/api/agents/%s/mcp-servers/%s", w.ensureAgent(), w.ensureAgentMcp())
		}},
	{Method: http.MethodDelete, Path: "/api/agents/{agentId}/skills/{skillId}", Domain: "skills", Auth: authMember,
		Setup: func(w *world) string {
			ag, sk := w.ensureAgent(), w.ensureSkill()
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/agents/%s/skills/add", ag), map[string]string{"skill_id": sk}, nil)
			require.Equal(w.t, http.StatusOK, rec.Code, rec.Body.String())
			return fmt.Sprintf("/api/agents/%s/skills/%s", ag, sk)
		}},
	{Method: http.MethodGet, Path: "/api/agents/{agentId}/tasks", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/agents/" + w.ensureAgent() + "/tasks" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodGet, Path: "/api/agent-builder/sessions/{sessionId}/draft", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/agent-builder/sessions/" + w.ensureBuilderSession() + "/draft" },
		WantStatus: http.StatusOK},
	{Method: http.MethodPut, Path: "/api/agent-builder/sessions/{sessionId}/draft", Domain: "orchestration", Auth: authMember,
		Body:        func() any { return map[string]any{"draft": map[string]any{"name": "final batch draft"}} },
		Setup:       func(w *world) string { return "/api/agent-builder/sessions/" + w.ensureBuilderSession() + "/draft" },
		WantStatus:  http.StatusOK,
		SkipBadBody: true},
	{Method: http.MethodDelete, Path: "/api/autopilots/{autopilotId}", Domain: "automation", Auth: authMember,
		Setup: func(w *world) string { return "/api/autopilots/" + mkAutopilot(w, "final batch delete automation") }},
	{Method: http.MethodPost, Path: "/api/autopilots", Domain: "automation", Auth: authMember,
		Body: func() any {
			return map[string]any{"name": "final batch automation", "exec_mode": "create_work_item", "target_agent_id": gAgent(), "template": map[string]any{"title": "final batch {{date}}"}, "enabled": true}
		},
		WantStatus: http.StatusCreated},
	{Method: http.MethodDelete, Path: "/api/autopilots/{autopilotId}/triggers/{triggerId}", Domain: "automation", Auth: authMember,
		Setup: func(w *world) string {
			return fmt.Sprintf("/api/autopilots/%s/triggers/%s", w.ensureAutopilot(), w.ensureTrigger())
		}},
	{Method: http.MethodPost, Path: "/api/autopilots/{autopilotId}/collaborators", Domain: "automation", Auth: authMember,
		Body:  func() any { return map[string]string{"user_id": gOwnerID()} },
		Setup: func(w *world) string { return "/api/autopilots/" + w.ensureAutopilot() + "/collaborators" },

		WantStatus: http.StatusCreated},

	{Method: http.MethodPost, Path: "/api/chat/sessions", Domain: "conversation", Auth: authMember,
		Body:       func() any { return map[string]string{"agent_id": gAgent(), "title": "last-session"} },
		WantStatus: http.StatusCreated},
	{Method: http.MethodPost, Path: "/api/chat/sessions/{sessionId}/messages", Domain: "conversation", Auth: authMember,
		Body: func() any { return map[string]string{"content": "final batch message"} },

		Setup: func(w *world) string {
			rec := w.st.do("owner", http.MethodPost, "/api/chat/sessions", map[string]string{
				"agent_id": w.ensureAgent(), "title": "final batch message session",
			}, nil)
			require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
			return "/api/chat/sessions/" + decodeJSON(w.t, rec)["id"].(string) + "/messages"
		},
		WantStatus: http.StatusCreated},
	{Method: http.MethodDelete, Path: "/api/chat/sessions/{sessionId}", Domain: "conversation", Auth: authMember,
		Setup:      func(w *world) string { return "/api/chat/sessions/" + w.ensureChatSession() },
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/chat/sessions/{sessionId}/unarchive", Domain: "conversation", Auth: authMember,

		Setup: func(w *world) string {
			rec := w.st.do("owner", http.MethodPost, "/api/chat/sessions", map[string]string{
				"agent_id": w.ensureAgent(), "title": "final batch archive session",
			}, nil)
			require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
			sid := decodeJSON(w.t, rec)["id"].(string)
			rec = w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/chat/sessions/%s/archive", sid), nil, nil)
			require.Equal(w.t, http.StatusOK, rec.Code, rec.Body.String())
			return fmt.Sprintf("/api/chat/sessions/%s/unarchive", sid)
		},
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/chat/sessions/{sessionId}/unpin", Domain: "conversation", Auth: authMember,
		Setup: func(w *world) string {
			rec := w.st.do("owner", http.MethodPost, "/api/chat/sessions", map[string]string{
				"agent_id": w.ensureAgent(), "title": "final batch pinned session",
			}, nil)
			require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
			return "/api/chat/sessions/" + decodeJSON(w.t, rec)["id"].(string) + "/unpin"
		},
		WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/chat/pinned-agents", Domain: "conversation", Auth: authMember,
		Body:       func() any { return map[string]string{"agent_id": gAgent()} },
		WantStatus: http.StatusCreated},
	{Method: http.MethodGet, Path: "/api/chat/pinned-agents", Domain: "conversation", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/chat/pending-tasks/has-any", Domain: "conversation", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/chat/sessions/{sessionId}/pending-task", Domain: "conversation", Auth: authMember,
		Setup:      func(w *world) string { return "/api/chat/sessions/" + w.ensureChatSession() + "/pending-task" },
		WantStatus: http.StatusOK, ListLike: true},

	{Method: http.MethodPost, Path: "/api/skills", Domain: "skills", Auth: authMember,
		Body:       func() any { return map[string]string{"name": "final batch new skill", "description": "x"} },
		WantStatus: http.StatusCreated},
	{Method: http.MethodDelete, Path: "/api/skills/{skillId}", Domain: "skills", Auth: authMember,
		Setup: func(w *world) string { return "/api/skills/" + mkSkill(w) }},
	{Method: http.MethodPost, Path: "/api/skills/{skillId}/labels", Domain: "skills", Auth: authMember,
		Body:       func() any { return map[string]string{"label_id": glbl()} },
		Setup:      func(w *world) string { return "/api/skills/" + w.ensureSkill() + "/labels" },
		MissStatus: http.StatusNotFound, WantStatus: http.StatusOK},
	{Method: http.MethodDelete, Path: "/api/skills/{skillId}/labels/{labelId}", Domain: "skills", Auth: authMember,
		Setup: func(w *world) string {
			lb := mkLabel(w, "final batch skill signature")
			rec := w.st.do("owner", http.MethodPost,
				fmt.Sprintf("/api/skills/%s/labels", w.ensureSkill()),
				map[string]string{"label_id": lb}, nil)
			require.Equal(w.t, http.StatusOK, rec.Code, rec.Body.String())
			return fmt.Sprintf("/api/skills/%s/labels/%s", w.ensureSkill(), lb)
		}},
	{Method: http.MethodPut, Path: "/api/skills/{skillId}/files", Domain: "skills", Auth: authMember,
		Body:        func() any { return map[string]string{"path": "final.md", "content": "# attached files"} },
		Setup:       func(w *world) string { return "/api/skills/" + w.ensureSkill() + "/files" },
		WantStatus:  http.StatusOK,
		SkipBadBody: true},
	{Method: http.MethodPost, Path: "/api/skills/{skillId}/refresh", Domain: "skills", Auth: authMember,
		Setup:      func(w *world) string { return "/api/skills/" + mkSkill(w) + "/refresh" },
		WantStatus: http.StatusOK},

	{Method: http.MethodDelete, Path: "/api/squads/{squadId}", Domain: "orchestration", Auth: authMember,
		Setup: func(w *world) string {
			rec := w.st.do("owner", http.MethodPost, "/api/squads", map[string]any{"name": "final batch delete squad"}, nil)
			require.Equal(w.t, http.StatusCreated, rec.Code, rec.Body.String())
			return "/api/squads/" + decodeJSON(w.t, rec)["id"].(string)
		}},
	{Method: http.MethodPost, Path: "/api/squads/{squadId}/members", Domain: "orchestration", Auth: authMember,
		Body:  func() any { return map[string]string{"agent_id": gAgent()} },
		Setup: func(w *world) string { return "/api/squads/" + w.ensureSquad() + "/members" },

		MissStatus: http.StatusNotFound, WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/squads/{squadId}/members/status", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/squads/" + w.ensureSquad() + "/members/status" },
		WantStatus: http.StatusOK, ListLike: true},

	{Method: http.MethodGet, Path: "/api/notifications", Domain: "notification", Auth: authMember, WantStatus: http.StatusOK},
	{Method: http.MethodPost, Path: "/api/notifications/archive-all", Domain: "notification", Auth: authMember, WantStatus: http.StatusNoContent},
	{Method: http.MethodPost, Path: "/api/notifications/archive-all-read", Domain: "notification", Auth: authMember,
		WantStatus: http.StatusOK},
	{Method: http.MethodGet, Path: "/api/agents/{agentId}/labels", Domain: "orchestration", Auth: authMember,
		Setup:      func(w *world) string { return "/api/agents/" + w.ensureAgent() + "/labels" },
		WantStatus: http.StatusOK, ListLike: true},
	{Method: http.MethodGet, Path: "/api/autopilots/{autopilotId}/runs", Domain: "automation", Auth: authMember,
		Setup:      func(w *world) string { return "/api/autopilots/" + w.ensureAutopilot() + "/runs" },
		WantStatus: http.StatusOK, ListLike: true},
}

var (
	gAgent   = func() string { return posWorld.ensureAgent() }
	gOwnerID = func() string {
		var id string
		posWorld.st.pool.QueryRow(posWorld.t.Context(),
			`SELECT id::text FROM "user" WHERE email LIKE 'owner@%' LIMIT 1`).Scan(&id)
		return id
	}
)

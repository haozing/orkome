package main

import "net/http"

var RouteContract = map[string][]string{
	"identity_root": {
		"POST /auth/send-code", "POST /auth/verify-code",
		"POST /auth/logout", "GET /config", "POST /cli-token",
		// FP-16 P1-1 接线后活化的账号端点（identityhttp.Mount，Account 非 nil 才挂）
		"POST /auth/forgot-password", "POST /auth/reset-password",
		"POST /auth/send-verification", "POST /auth/verify-email", "DELETE /auth/me",
	},
	"identity_api": {
		"GET/PATCH /me", "GET/POST /workspaces", "GET/PATCH/DELETE /workspaces/{id}",
		"GET/POST /workspaces/{id}/members", "PATCH/DELETE .../members/{memberId}", "POST .../leave",
		"GET/POST/DELETE .../invitations", "GET /invitations/mine", "POST /invitations/{id}/accept|decline",
		"POST/GET/DELETE .../share-links", "POST /share-links/redeem", // 框架只有 redeem（batch1 根因 C：join 为幽灵契约）
		"GET/POST /tokens", "POST /tokens/current/renew", "DELETE /tokens/{id}",
	},
	"work": {
		"GET/POST /work-items", "GET/PUT/DELETE /work-items/{id}", "POST /work-items/{id}/move",
		"POST /work-items/batch-update|batch-delete", "POST /work-items/query",
		"POST /work-items/table/rows|groups|facets", "GET /work-items/search",
		"POST /work-items/preview-trigger", "POST /work-items/quick-create",
		"GET /work-items/{id}/comments", "POST .../comments",
		"GET /comments/{commentId}/sub-work-item-preview", "POST /comments/{commentId}/sub-work-items",
		"PUT/DELETE /comments/{commentId}", "POST/DELETE .../resolve",
		"GET/POST /labels", "GET/POST /work-item-statuses", "POST .../reorder",
		"GET/POST /properties", "GET/POST /projects", "GET /work-items/{id}/timeline",
		"GET /work-items/{id}/subscribers", "GET /work-items/{id}/attachments", "POST /client-usage",
	},
	"orchestration": {
		"GET/POST /agents", "GET/PUT /agents/{id}", "POST .../archive|restore|cancel-tasks",
		"GET/POST /agents/{id}/env", "GET/POST /credentials (write owner/admin)", "DELETE /credentials/{id}",
		"GET /agents/{id}/mcp-servers(?resolved=1)", "GET/POST /agents/{id}/mcp-servers",
		"PUT/DELETE /agents/{id}/mcp-servers/{serverId}", "GET/POST/DELETE /agents/{id}/labels",
		"GET/POST/PUT/DELETE /workspaces/{id}/mcp-servers",
		"GET/POST /agent-builder/sessions", "GET/PUT .../sessions/{id}/draft",
		"GET /tasks/{taskId}/messages", "POST /tasks/{taskId}/retry-source-context",
		"POST /tasks/{taskId}/approve", "POST /tasks/{taskId}/deny", "GET /tasks/{taskId}/interrupt",
		"POST/GET /work-items/{id}/batches", "GET /batches/{id}(/items)",
		"GET /batches/{id}/items/page",
		"POST /batches/{id}/start|pause|resume|cancel|retry-failed",
		"GET/POST /squads", "GET /squads/{id}/members(/status)",
		"PATCH/DELETE /squads/{id}/members/{agentId}", "POST /work-items/{id}/squad-evaluated",
	},
	"conversation": {
		"GET/POST /chat/sessions", "GET /chat/sessions/{id}", "POST .../archive|unarchive",
		"GET/POST .../messages", "GET/DELETE .../pending-task", "DELETE .../queued-tasks",
		"POST .../queued-tasks/{taskId}/prioritize", "POST .../read", "GET/DELETE .../draft-restores",
		"GET/POST /quick-actions", "PATCH/DELETE /quick-actions/{id}",
		"GET /chat/pending-tasks(/has-any)", "GET/POST/DELETE /chat/pinned-agents",
		"POST /work-items/{id}/quick-actions/{quickActionId}/run|render",
	},
	"notification": {
		"GET /notifications", "GET /notifications/unread-count", "POST /notifications/mark-all-read", "POST /notifications/archive-all",
		"POST /notifications/{id}/read|unread|archive|unarchive", "GET/PATCH/PUT /notification-preferences",
		"GET /notifications/archived|summary|facets", "POST /notifications/archive-all-read",
	},
	"notification_framework": { // ploykit notifyhttp.Mount via filtered router; product supersedes its list/{id}/read/{id}/archive/GET-prefs
		"GET /notifications/badge", "POST /notifications/read-all", "PUT /notification-preferences/{type}",
	},
	"pack": { // pack 一等公民化 P1（W1-2）+ 平台化：读 member / 写 admin
		"GET /packs", "GET /packs/{packId}", "PUT /packs (admin)", "DELETE /packs/{packId} (admin)",
		"PUT /packs/{packId}/subscriptions (admin)",
		"GET /packs/{packId}/usage-document",
		"POST /agents/{agentId}/pack (admin)", "POST /skills/{skillId}/pack (admin)",
	},
	"content": {
		"POST /upload-file", "GET/DELETE /attachments/{id}", "GET .../download|content|signed-download",
		"GET /avatars/{key}", "GET/HEAD /uploads/* (root path, LOCAL mode only)",
	},
	"automation": {
		"GET/POST/DELETE /autopilots/{id}/collaborators", "POST /channels/title-callback",
		"POST /webhooks/autopilots/{token}", "POST/GET /work-items/{id}/wakeups",
		"POST .../wakeups/{wakeupId}/enable", "POST /autopilots", "POST /autopilots/{id}/trigger",
		"PUT /autopilots/{id}/triggers/{triggerId}/signing-secret",
		"POST /autopilots/{id}/triggers/{triggerId}/rotate-webhook-token",
	},
	"webhooks": { // framework webhooks domain (ploykit/webhooks)
		"GET /workspaces/{id}/webhooks/events", "GET /workspaces/{id}/webhooks/deliveries",
		"POST /workspaces/{id}/webhooks/deliveries/{deliveryId}/redeliver",
		"GET/POST /workspaces/{id}/webhooks/subscriptions",
		"POST .../subscriptions/{subscriptionId}/ping",
		"POST .../subscriptions/{subscriptionId}/rotate-secret",
		"PATCH/DELETE .../subscriptions/{subscriptionId}",
	},
}

var _ = http.MethodGet

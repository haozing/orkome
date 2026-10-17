package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	"orkome/internal/work/app"
)

type Deps struct {
	WorkItems *app.WorkItemService
	Comments  *app.CommentService
	Catalog   *app.CatalogService
	Views     *app.ViewService
	Search    *app.SearchService

	Workflow *app.WorkflowService

	SourceCtx   *app.SourceContextService
	Timeline    func(ctx context.Context, workItemID string, limit int, before string) ([]app.TimelineEntry, error)
	Subscribers func(ctx context.Context, workItemID string) ([]app.Subscriber, error)

	ClientUsage *app.ClientUsageService

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		// 非 apierr 一律 500 且不打日志是"三无"模式根源（batch1 根因 D）：
		// 用户看到 internal error、服务端无根因痕迹。补日志后底层错误
		//（如 pgx 约束违规）可追溯。
		slog.Error("work: unclassified error on write path", "err", err)
		webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
		return
	}
	status := http.StatusInternalServerError
	switch ae.Code {
	case apierr.Unauthenticated:
		status = http.StatusUnauthorized
	case apierr.Forbidden:
		status = http.StatusForbidden
	case apierr.NotFound:
		status = http.StatusNotFound
	case apierr.Validation:
		status = http.StatusBadRequest
	case apierr.Conflict, apierr.DuplicatePendingTask, apierr.ActiveDuplicate, apierr.AwaitingState:
		status = http.StatusConflict
	case apierr.RateLimited:
		status = http.StatusTooManyRequests
	case apierr.SearchTimeout:
		status = http.StatusServiceUnavailable
	}
	webx.WriteError(w, status, ae.Code, ae.Message, ae.Details)
}

func decode[T any](w http.ResponseWriter, r *http.Request) (*T, bool) {
	var in T
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return nil, false
	}
	return &in, true
}

func principal(r *http.Request) *webx.Principal { return webx.PrincipalFrom(r.Context()) }

func wsID(r *http.Request) string { return webx.PrincipalFrom(r.Context()).WorkspaceID }

func Routes(d Deps, auth func(http.Handler) http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	Mount(mux, d, auth)
	return mux
}

func Mount(mux *http.ServeMux, d Deps, auth func(http.Handler) http.Handler) {
	wsGuard := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := webx.PrincipalFrom(r.Context())
			ws := r.Header.Get("X-Workspace-Id")
			if ws == "" {
				webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "X-Workspace-Id header required", nil)
				return
			}
			ok, err := d.MemberCheck(r.Context(), ws, p.UserID)
			if err != nil || !ok {
				webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "not a member of this workspace", nil)
				return
			}
			p.WorkspaceID = ws
			next.ServeHTTP(w, r)
		})
	}
	reg := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, auth(webx.RequireAuth(wsGuard(h))))
	}

	reg("GET /api/work-items", d.handleList)
	reg("POST /api/work-items", d.handleCreate)
	reg("POST /api/work-items/query", d.handleQuery)
	reg("POST /api/work-items/table/rows", d.handleRows)
	reg("POST /api/work-items/table/groups", d.handleGroups)
	reg("POST /api/work-items/table/facets", d.handleFacets)
	reg("GET /api/work-items/search", d.handleSearch)
	reg("POST /api/work-items/batch-update", d.handleBatchUpdate)
	reg("POST /api/work-items/batch-delete", d.handleBatchDelete)
	reg("GET /api/work-items/child-progress", d.handleChildProgress)
	reg("GET /api/work-items/children", d.handleChildren)
	reg("GET /api/work-items/grouped", d.handleGroupedByAssignee)
	reg("GET /api/work-items/assignee-frequency", d.handleAssigneeFreq)
	reg("POST /api/work-items/preview-trigger", d.handleTriggerPreview)

	reg("GET /api/work-items/{workItemId}", d.handleGet)
	reg("PUT /api/work-items/{workItemId}", d.handleUpdate)
	// PATCH 别名（P1-8）：与 PUT 同 handler——前端/第三方按 PATCH 语义改单
	// 时不再 405，语义上 PATCH 本就是 Update 的精准动词。
	reg("PATCH /api/work-items/{workItemId}", d.handleUpdate)
	reg("DELETE /api/work-items/{workItemId}", d.handleDelete)
	reg("POST /api/work-items/{workItemId}/move", d.handleMove)
	reg("POST /api/work-items/{workItemId}/reactions", d.handleReaction)
	reg("DELETE /api/work-items/{workItemId}/reactions", d.handleReactionDelete)
	reg("GET /api/work-items/{workItemId}/comments", d.handleListComments)
	reg("POST /api/work-items/{workItemId}/comments", d.handleCreateComment)
	reg("POST /api/work-items/{workItemId}/comments/trigger-preview", d.handleTriggerPreview)

	reg("GET /api/work-items/{workItemId}/labels", d.handleGetLabels)
	reg("POST /api/work-items/{workItemId}/labels", d.handleAttachLabel)
	reg("DELETE /api/work-items/{workItemId}/labels/{labelId}", d.handleDetachLabel)
	reg("GET /api/work-items/{workItemId}/timeline", d.handleTimeline)
	reg("GET /api/work-items/{workItemId}/subscribers", d.handleSubscribers)
	reg("POST /api/work-items/{workItemId}/subscribers", d.handleSubscribe)
	reg("GET /api/work-items/{workItemId}/duplicates", d.handleDuplicates)
	reg("GET /api/work-items/{workItemId}/metadata/{key}", d.handleMetadata)
	reg("PUT /api/work-items/{workItemId}/metadata/{key}", d.handleMetadataPut)
	reg("DELETE /api/work-items/{workItemId}/metadata/{key}", d.handleMetadataDel)

	reg("GET /api/work-items/{workItemId}/properties/{key}", d.handleGetPropertyKey)
	reg("PUT /api/work-items/{workItemId}/properties/{key}", d.handlePropertyKeyPut)
	reg("DELETE /api/work-items/{workItemId}/properties/{key}", d.handlePropertyKeyDel)

	reg("GET /api/labels", d.handleListLabelsAll)
	reg("POST /api/labels", d.handleCreateLabel)
	reg("GET /api/labels/{labelId}", d.handleGetLabel)
	reg("DELETE /api/labels/{labelId}", d.handleDeleteLabel)
	reg("PUT /api/labels/{labelId}", d.handleUpsertLabel)
	reg("GET /api/projects", d.handleListProjects)
	reg("POST /api/projects", d.handleCreateProject)
	reg("GET /api/projects/search", d.handleSearchProjects)
	reg("GET /api/projects/{projectId}", d.handleGetProject)
	reg("PUT /api/projects/{projectId}", d.handleUpdateProject)
	reg("DELETE /api/projects/{projectId}", d.handleDeleteProject)
	reg("GET /api/projects/{projectId}/resources", d.handleListProjectResources)
	reg("POST /api/projects/{projectId}/resources", d.handleCreateProjectResource)
	reg("PUT /api/projects/{projectId}/resources/{resourceId}", d.handleUpdateProjectResource)
	reg("DELETE /api/projects/{projectId}/resources/{resourceId}", d.handleDeleteProjectResource)

	if d.Workflow != nil {
		reg("PUT /api/projects/{projectId}/workflow", d.handleUpsertWorkflow)
		reg("GET /api/projects/{projectId}/workflow", d.handleGetWorkflow)
		reg("DELETE /api/projects/{projectId}/workflow", d.handleDeleteWorkflow)
		reg("POST /api/projects/{projectId}/workflow/enable", d.setWorkflowEnabled(true))
		reg("POST /api/projects/{projectId}/workflow/disable", d.setWorkflowEnabled(false))
		// W2 状态面（工作项侧）+ GET state（契约 §3；WorkflowPanel 消费）。
		reg("GET /api/work-items/{workItemId}/workflow", d.handleGetWorkflowState)
		reg("POST /api/work-items/{workItemId}/workflow/start", d.handleStartWorkflow)
		reg("POST /api/work-items/{workItemId}/workflow/restart", d.handleRestartWorkflow)
		reg("DELETE /api/work-items/{workItemId}/workflow", d.handleDetachWorkflow)
	}
	reg("GET /api/work-item-statuses", d.handleListStatuses)
	reg("POST /api/work-item-statuses", d.handleCreateStatus)
	reg("POST /api/work-item-statuses/reorder", d.handleReorderStatuses)
	reg("PATCH /api/work-item-statuses/{statusKey}", d.handleUpdateStatus)
	reg("GET /api/properties", d.handleListProps)
	reg("POST /api/properties", d.handleCreateProp)

	reg("GET /api/properties/{key}", d.handleGetPropertyDef)
	reg("PATCH /api/properties/{key}", d.handleUpdatePropertyDef)
	reg("DELETE /api/properties/{key}", d.handleDeletePropertyDef)

	reg("GET /api/work-item-views", d.handleListViews)
	reg("PUT /api/work-item-views", d.handleSaveView)
	reg("GET /api/work-item-view-preferences", d.handleViewPrefGet)
	reg("PUT /api/work-item-view-preferences", d.handleViewPrefPut)

	reg("POST /api/unsubscribe", d.handleUnsubscribe(false))
	reg("POST /api/unsubscribe/subtree", d.handleUnsubscribe(true))

	if d.ClientUsage != nil {
		reg("POST /api/client-usage", webx.RequireHuman(d.handleClientUsage))
	}

	reg("PUT /api/comments/{commentId}", d.handleUpdateComment)
	reg("DELETE /api/comments/{commentId}", d.handleDeleteComment)

	reg("DELETE /api/comments/{commentId}/keep-replies", d.handleDeleteComment)
	reg("POST /api/comments/{commentId}/resolve", d.handleResolve)
	reg("DELETE /api/comments/{commentId}/resolve", d.handleUnresolve)

	reg("POST /api/comments/{commentId}/reactions", d.handleCommentReaction)
	reg("DELETE /api/comments/{commentId}/reactions", d.handleCommentReactionDelete)
	if d.SourceCtx != nil {
		reg("GET /api/comments/{commentId}/sub-work-item-preview", webx.RequireHuman(d.handleSourcePreview))
		reg("POST /api/comments/{commentId}/sub-work-items", webx.RequireHuman(d.handleSourceCreate))
	}

	if d.SourceCtx != nil {
		reg("POST /api/work-items/quick-create", d.handleQuickCreate)
	}
}

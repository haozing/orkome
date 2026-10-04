package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"orkome/internal/contract/apierr"
	"orkome/internal/platform/webx"
	"orkome/internal/work/app"
)

type Deps struct {
	WorkItems *app.WorkItemService
	Comments  *app.CommentService
	Catalog   *app.CatalogService
	Views     *app.ViewService
	Search    *app.SearchService

	SourceCtx   *app.SourceContextService
	Timeline    func(ctx context.Context, workItemID string, limit int, before string) ([]app.TimelineEntry, error)
	Subscribers func(ctx context.Context, workItemID string) ([]app.Subscriber, error)

	ClientUsage *app.ClientUsageService

	MemberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apierr.Error
	if !errors.As(err, &ae) {
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

func Routes(d Deps, auth func(http.Handler) http.Handler) chi.Router {
	r := chi.NewRouter()
	Mount(r, d, auth)
	return r
}

func Mount(r chi.Router, d Deps, auth func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(auth, webx.RequireAuth)
		r.Use(func(next http.Handler) http.Handler {
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
		})

		r.Get("/work-items", d.handleList)
		r.Post("/work-items", d.handleCreate)
		r.Post("/work-items/query", d.handleQuery)
		r.Post("/work-items/table/rows", d.handleRows)
		r.Post("/work-items/table/groups", d.handleGroups)
		r.Post("/work-items/table/facets", d.handleFacets)
		r.Get("/work-items/search", d.handleSearch)
		r.Post("/work-items/batch-update", d.handleBatchUpdate)
		r.Post("/work-items/batch-delete", d.handleBatchDelete)
		r.Get("/work-items/child-progress", d.handleChildProgress)
		r.Get("/work-items/children", d.handleChildren)
		r.Get("/work-items/grouped", d.handleGroupedByAssignee)
		r.Get("/work-items/assignee-frequency", d.handleAssigneeFreq)
		r.Post("/work-items/preview-trigger", d.handleTriggerPreview)

		r.Route("/work-items/{workItemId}", func(r chi.Router) {
			r.Get("/", d.handleGet)
			r.Put("/", d.handleUpdate)
			r.Delete("/", d.handleDelete)
			r.Post("/move", d.handleMove)
			r.Post("/reactions", d.handleReaction)
			r.Delete("/reactions", d.handleReactionDelete)
			r.Get("/comments", d.handleListComments)
			r.Post("/comments", d.handleCreateComment)
			r.Post("/comments/trigger-preview", d.handleTriggerPreview)

			r.Get("/labels", d.handleGetLabels)
			r.Post("/labels", d.handleAttachLabel)
			r.Delete("/labels/{labelId}", d.handleDetachLabel)
			r.Get("/timeline", d.handleTimeline)
			r.Get("/subscribers", d.handleSubscribers)
			r.Post("/subscribers", d.handleSubscribe)
			r.Get("/usage", d.handleUsage)
			r.Get("/duplicates", d.handleDuplicates)
			r.Get("/metadata/{key}", d.handleMetadata)
			r.Put("/metadata/{key}", d.handleMetadataPut)
			r.Delete("/metadata/{key}", d.handleMetadataDel)

			r.Get("/properties/{key}", d.handleGetPropertyKey)
			r.Put("/properties/{key}", d.handlePropertyKeyPut)
			r.Delete("/properties/{key}", d.handlePropertyKeyDel)
		})

		r.Get("/labels", d.handleListLabelsAll)
		r.Post("/labels", d.handleCreateLabel)
		r.Route("/labels/{labelId}", func(r chi.Router) {
			r.Get("/", d.handleGetLabel)
			r.Delete("/", d.handleDeleteLabel)
			r.Put("/", d.handleUpsertLabel)
		})
		r.Get("/projects", d.handleListProjects)
		r.Post("/projects", d.handleCreateProject)
		r.Get("/projects/search", d.handleSearchProjects)
		r.Route("/projects/{projectId}", func(r chi.Router) {
			r.Get("/", d.handleGetProject)
			r.Put("/", d.handleUpdateProject)
			r.Delete("/", d.handleDeleteProject)

			r.Get("/resources", d.handleListProjectResources)
			r.Post("/resources", d.handleCreateProjectResource)
			r.Route("/resources/{resourceId}", func(r chi.Router) {
				r.Put("/", d.handleUpdateProjectResource)
				r.Delete("/", d.handleDeleteProjectResource)
			})
		})
		r.Get("/work-item-statuses", d.handleListStatuses)
		r.Post("/work-item-statuses", d.handleCreateStatus)
		r.Post("/work-item-statuses/reorder", d.handleReorderStatuses)
		r.Patch("/work-item-statuses/{statusKey}", d.handleUpdateStatus)
		r.Get("/properties", d.handleListProps)
		r.Post("/properties", d.handleCreateProp)

		r.Get("/properties/{key}", d.handleGetPropertyDef)
		r.Patch("/properties/{key}", d.handleUpdatePropertyDef)
		r.Delete("/properties/{key}", d.handleDeletePropertyDef)

		r.Get("/work-item-views", d.handleListViews)
		r.Put("/work-item-views", d.handleSaveView)
		r.Get("/work-item-view-preferences", d.handleViewPrefGet)
		r.Put("/work-item-view-preferences", d.handleViewPrefPut)

		r.Post("/unsubscribe", d.handleUnsubscribe(false))
		r.Post("/unsubscribe/subtree", d.handleUnsubscribe(true))

		if d.ClientUsage != nil {
			r.Post("/client-usage", webx.RequireHuman(d.handleClientUsage))
		}

		r.Route("/comments/{commentId}", func(r chi.Router) {
			r.Put("/", d.handleUpdateComment)
			r.Delete("/", d.handleDeleteComment)

			r.Delete("/keep-replies", d.handleDeleteComment)
			r.Post("/resolve", d.handleResolve)
			r.Delete("/resolve", d.handleUnresolve)

			r.Post("/reactions", d.handleCommentReaction)
			r.Delete("/reactions", d.handleCommentReactionDelete)
			if d.SourceCtx != nil {
				r.Get("/sub-work-item-preview", webx.RequireHuman(d.handleSourcePreview))
				r.Post("/sub-work-items", webx.RequireHuman(d.handleSourceCreate))
			}
		})

		if d.SourceCtx != nil {
			r.Post("/work-items/quick-create", d.handleQuickCreate)
		}

	})
}

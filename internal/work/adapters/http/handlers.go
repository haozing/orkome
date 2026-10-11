package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	"orkome/internal/contract/dispatchreason"
	workapp "orkome/internal/work/app"
	"orkome/internal/work/domain"
)

func (d Deps) handleCreate(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	in, ok := decode[struct {
		Title        string `json:"title"`
		Description  string `json:"description"`
		Status       string `json:"status"`
		Priority     int    `json:"priority"`
		AssigneeType string `json:"assignee_type"`
		AssigneeID   string `json:"assignee_id"`
		ParentID     string `json:"parent_work_item_id"`
		ProjectID    string `json:"project_id"`
	}](w, r)
	if !ok {
		return
	}
	if in.AssigneeType == "" {
		in.AssigneeType = "none"
	}
	wi, err := d.WorkItems.Create(r.Context(), workapp.NewWorkItem{
		WorkspaceID: wsID(r), Title: in.Title, Description: in.Description, StatusKey: in.Status,
		Priority: in.Priority, AssigneeType: in.AssigneeType, AssigneeID: in.AssigneeID,
		CreatorType: "user", CreatorID: p.UserID, ParentID: in.ParentID, ProjectID: in.ProjectID,
	}, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, wi)
}

func (d Deps) handleGet(w http.ResponseWriter, r *http.Request) {
	wi, err := d.WorkItems.Get(r.Context(), wsID(r), r.PathValue("workItemId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, wi)
}

func (d Deps) handleList(w http.ResponseWriter, r *http.Request) {
	q := urlQuery(r)
	items, next, err := d.WorkItems.List(r.Context(), q)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (d Deps) handleUpdate(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	in, ok := decode[struct {
		Title        *string        `json:"title"`
		Description  *string        `json:"description"`
		Status       *string        `json:"status"`
		Priority     *int           `json:"priority"`
		AssigneeType *string        `json:"assignee_type"`
		AssigneeID   *string        `json:"assignee_id"`
		ProjectID    *string        `json:"project_id"`
		DueDate      *string        `json:"due_date"`
		StartDate    *string        `json:"start_date"`
		ParentID     *string        `json:"parent_work_item_id"`
		Properties   map[string]any `json:"properties"`
		DuplicateOf  *string        `json:"duplicate_of_work_item_id"`
		TriageState  *string        `json:"triage_state"`
		ExpectedRev  int64          `json:"expected_revision"`
	}](w, r)
	if !ok {
		return
	}
	wi, err := d.WorkItems.Update(r.Context(), wsID(r), r.PathValue("workItemId"), workapp.WorkItemPatch{
		Title: in.Title, Description: in.Description, StatusKey: in.Status, Priority: in.Priority,
		AssigneeType: in.AssigneeType, AssigneeID: in.AssigneeID, ProjectID: in.ProjectID,
		DueDate: in.DueDate, StartDate: in.StartDate, ParentID: in.ParentID,
		Properties: in.Properties, DuplicateOf: in.DuplicateOf,
		TriageState: in.TriageState,
	}, in.ExpectedRev, p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, wi)
}

func (d Deps) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := d.WorkItems.Delete(r.Context(), wsID(r), r.PathValue("workItemId"), principal(r).UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleMove(w http.ResponseWriter, r *http.Request) {
	in, _ := decode[struct {
		Before *string `json:"before"`
		After  *string `json:"after"`
		Status string  `json:"status"`
	}](w, r)
	var before, after *string
	if in != nil {
		before, after = in.Before, in.After
	}
	if err := d.WorkItems.Move(r.Context(), wsID(r), r.PathValue("workItemId"), before, after); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleBatchUpdate(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		IDs   []string       `json:"ids"`
		Patch map[string]any `json:"patch"`
	}](w, r)
	if !ok {
		return
	}
	patch := mapToPatch(in.Patch)

	if err := d.WorkItems.CheckBatchTriageLock(r.Context(), wsID(r), in.IDs, patch); err != nil {
		writeErr(w, err)
		return
	}
	updated := 0
	for _, id := range in.IDs {
		if _, err := d.WorkItems.Update(r.Context(), wsID(r), id, patch, 0, principal(r).UserID); err == nil {
			updated++
		}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"updated": updated})
}

func (d Deps) handleBatchDelete(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		IDs []string `json:"ids"`
	}](w, r)
	if !ok {
		return
	}
	deleted := 0
	for _, id := range in.IDs {
		if err := d.WorkItems.Delete(r.Context(), wsID(r), id, principal(r).UserID); err == nil {
			deleted++
		}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

func (d Deps) handleChildren(w http.ResponseWriter, r *http.Request) {

	parent := r.URL.Query().Get("parent")
	if parent == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "parent query parameter required", nil)
		return
	}
	q := urlQuery(r)
	q.ParentID = &parent
	items, next, err := d.WorkItems.List(r.Context(), q)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (d Deps) handleChildProgress(w http.ResponseWriter, r *http.Request) {

	rows, err := d.WorkItems.ChildProgress(r.Context(), wsID(r), r.URL.Query().Get("parent"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, rows)
}

func (d Deps) handleGroupedByAssignee(w http.ResponseWriter, r *http.Request) {
	rows, err := d.Views.Groups(r.Context(), wsID(r), "assignee", filtersFromQuery(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, rows)
}

func (d Deps) handleAssigneeFreq(w http.ResponseWriter, r *http.Request) {
	rows, err := d.Views.Groups(r.Context(), wsID(r), "assignee", filtersFromQuery(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, rows)
}

func (d Deps) handleReaction(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	in, ok := decode[struct {
		Emoji string `json:"emoji"`
	}](w, r)
	if !ok {
		return
	}
	counts, err := d.WorkItems.AddReaction(r.Context(), wsID(r), r.PathValue("workItemId"), p.UserID, in.Emoji)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]any{"reactions": counts})
}

func (d Deps) handleReactionDelete(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	emoji := r.URL.Query().Get("emoji")
	if emoji == "" {
		in, _ := decode[struct {
			Emoji string `json:"emoji"`
		}](w, r)
		if in != nil {
			emoji = in.Emoji
		}
	}
	counts, err := d.WorkItems.RemoveReaction(r.Context(), wsID(r), r.PathValue("workItemId"), p.UserID, emoji)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"reactions": counts})
}

func (d Deps) handleUsage(w http.ResponseWriter, r *http.Request) {
	rows, err := d.WorkItems.Usage(r.Context(), wsID(r), r.PathValue("workItemId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (d Deps) handleDuplicates(w http.ResponseWriter, r *http.Request) {
	items, err := d.WorkItems.Duplicates(r.Context(), wsID(r), r.PathValue("workItemId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (d Deps) handleMetadata(w http.ResponseWriter, r *http.Request) {
	val, err := d.WorkItems.GetMetadataKey(r.Context(), wsID(r), r.PathValue("workItemId"), r.PathValue("key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"key": r.PathValue("key"), "value": val})
}

func (d Deps) handleMetadataPut(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "cannot read body", nil)
		return
	}
	if !json.Valid(raw) {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	meta, wi, err := d.WorkItems.SetMetadataKey(r.Context(), wsID(r), r.PathValue("workItemId"),
		r.PathValue("key"), json.RawMessage(raw), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"metadata": meta, "revision": wi.Revision})
}

func (d Deps) handleMetadataDel(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := d.WorkItems.DeleteMetadataKey(r.Context(), wsID(r), r.PathValue("workItemId"),
		r.PathValue("key"), p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleGetPropertyKey(w http.ResponseWriter, r *http.Request) {
	val, err := d.WorkItems.GetPropertyKey(r.Context(), wsID(r), r.PathValue("workItemId"), r.PathValue("key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"key": r.PathValue("key"), "value": val})
}

func (d Deps) handlePropertyKeyPut(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "cannot read body", nil)
		return
	}
	if !json.Valid(raw) {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	key := r.PathValue("key")
	val, props, wi, err := d.WorkItems.SetPropertyKey(r.Context(), wsID(r), r.PathValue("workItemId"),
		key, json.RawMessage(raw), p.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{
		"key": key, "value": val, "properties": props, "revision": wi.Revision,
	})
}

func (d Deps) handlePropertyKeyDel(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := d.WorkItems.DeletePropertyKey(r.Context(), wsID(r), r.PathValue("workItemId"),
		r.PathValue("key"), p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	in, ok := decode[struct {
		Content  string `json:"content"`
		ParentID string `json:"parent_id"`
	}](w, r)
	if !ok {
		return
	}
	c, admission, err := d.Comments.Create(r.Context(), workapp.NewComment{
		WorkItemID: r.PathValue("workItemId"), WorkspaceID: wsID(r),
		AuthorType: "user", AuthorID: p.UserID, Content: in.Content, ParentID: in.ParentID,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]any{"comment": c, "admission": admission})
}

func (d Deps) handleListComments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("limit") != "" || q.Get("before") != "" {
		limit := 50
		if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
			limit = n
		}
		items, next, err := d.Comments.ListThreadPage(r.Context(), r.PathValue("workItemId"),
			limit, strings.TrimSpace(q.Get("before")))
		if err != nil {
			writeErr(w, err)
			return
		}
		if items == nil {
			items = []workapp.Comment{}
		}
		resp := map[string]any{"items": items}
		if next != "" {
			resp["next_before"] = next
		}
		webx.WriteJSON(w, http.StatusOK, resp)
		return
	}
	items, err := d.Comments.ListThread(r.Context(), r.PathValue("workItemId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (d Deps) handleUpdateComment(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	in, ok := decode[struct {
		Content          string `json:"content"`
		ExpectedRevision int64  `json:"expected_revision"`
	}](w, r)
	if !ok {
		return
	}
	c, err := d.Comments.Update(r.Context(), wsID(r), r.PathValue("commentId"), in.Content, p.UserID, in.ExpectedRevision)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, c)
}

func (d Deps) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	if err := d.Comments.Delete(r.Context(), wsID(r), r.PathValue("commentId"), principal(r).UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleResolve(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := d.Comments.SetResolve(r.Context(), wsID(r), r.PathValue("commentId"), true, "user", p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleUnresolve(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := d.Comments.SetResolve(r.Context(), wsID(r), r.PathValue("commentId"), false, "user", p.UserID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleCommentReaction(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	in, ok := decode[struct {
		Emoji string `json:"emoji"`
	}](w, r)
	if !ok {
		return
	}
	counts, err := d.Comments.AddCommentReaction(r.Context(), wsID(r), r.PathValue("commentId"), p.UserID, in.Emoji)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]any{"reactions": counts})
}

func (d Deps) handleCommentReactionDelete(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	emoji := r.URL.Query().Get("emoji")
	if emoji == "" {
		in, _ := decode[struct {
			Emoji string `json:"emoji"`
		}](w, r)
		if in != nil {
			emoji = in.Emoji
		}
	}
	counts, err := d.Comments.RemoveCommentReaction(r.Context(), wsID(r), r.PathValue("commentId"), p.UserID, emoji)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"reactions": counts})
}

func (d Deps) handleTimeline(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := d.Timeline(r.Context(), r.PathValue("workItemId"), limit, r.URL.Query().Get("before"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (d Deps) handleSubscribers(w http.ResponseWriter, r *http.Request) {
	items, err := d.Subscribers(r.Context(), r.PathValue("workItemId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (d Deps) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	sub, err := d.WorkItems.Subscribe(r.Context(), wsID(r), r.PathValue("workItemId"), principal(r).UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, sub)
}

func (d Deps) handleListLabelsAll(w http.ResponseWriter, r *http.Request) {
	items, err := d.Catalog.ListLabels(r.Context(), wsID(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, items)
}

func (d Deps) handleCreateLabel(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}](w, r)
	if !ok {
		return
	}
	l, err := d.Catalog.UpsertLabel(r.Context(), wsID(r), in.Name, in.Color)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, l)
}

func (d Deps) handleUpsertLabel(w http.ResponseWriter, r *http.Request) { d.handleCreateLabel(w, r) }

func (d Deps) handleDeleteLabel(w http.ResponseWriter, r *http.Request) {
	if err := d.Catalog.DeleteLabel(r.Context(), wsID(r), r.PathValue("labelId")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleGetLabels(w http.ResponseWriter, r *http.Request) {
	items, err := d.WorkItems.Labels(r.Context(), wsID(r), r.PathValue("workItemId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, items)
}

func (d Deps) handleAttachLabel(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		LabelID string `json:"label_id"`
	}](w, r)
	if !ok {
		return
	}
	if in.LabelID == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "label_id required", nil)
		return
	}
	items, err := d.WorkItems.AttachLabel(r.Context(), wsID(r), r.PathValue("workItemId"), in.LabelID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, items)
}

func (d Deps) handleDetachLabel(w http.ResponseWriter, r *http.Request) {
	items, err := d.WorkItems.DetachLabel(r.Context(), wsID(r), r.PathValue("workItemId"), r.PathValue("labelId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, items)
}

func (d Deps) handleListStatuses(w http.ResponseWriter, r *http.Request) {
	items, err := d.Catalog.ListStatuses(r.Context(), wsID(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, items)
}

func (d Deps) handleCreateStatus(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Name     string `json:"name"`
		Category string `json:"category"`
	}](w, r)
	if !ok {
		return
	}
	st, err := d.Catalog.CreateStatus(r.Context(), wsID(r), in.Name, in.Category)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, st)
}

func (d Deps) handleReorderStatuses(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Keys []string `json:"keys"`
	}](w, r)
	if !ok {
		return
	}
	if err := d.Catalog.ReorderStatuses(r.Context(), wsID(r), in.Keys); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handleListProps(w http.ResponseWriter, r *http.Request) {
	items, err := d.Catalog.ListPropertyDefs(r.Context(), wsID(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, items)
}

func (d Deps) handleCreateProp(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Key         string   `json:"key"`
		Name        string   `json:"name"`
		Type        string   `json:"type"`
		Options     []string `json:"options"`
		Description string   `json:"description"`
	}](w, r)
	if !ok {
		return
	}
	def, err := d.Catalog.CreatePropertyDef(r.Context(), workapp.PropertyDef{
		Key: in.Key, Name: in.Name, Type: in.Type, Options: in.Options, Description: in.Description,
	}, wsID(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, def)
}

func (d Deps) handleListProjects(w http.ResponseWriter, r *http.Request) {
	items, err := d.Catalog.ListProjects(r.Context(), wsID(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, items)
}

func (d Deps) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}](w, r)
	if !ok {
		return
	}
	p, err := d.Catalog.CreateProject(r.Context(), wsID(r), in.Name, in.Description)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, p)
}

func filtersFromQuery(r *http.Request) workapp.QueryFilters {
	q := r.URL.Query()
	return workapp.QueryFilters{
		Status:   q["status"],
		Assignee: q.Get("assignee"),
		Project:  q.Get("project"),
		Search:   q.Get("search"),
		Involves: q.Get("involves"),
		Props:    propQueryParams(q),
	}
}

func (d Deps) handleQuery(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[workapp.CompileRequest](w, r)
	if !ok {
		return
	}
	out, err := d.Views.Compile(r.Context(), wsID(r), *in)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) handleRows(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Filters workapp.QueryFilters `json:"filters"`
		Cursor  string               `json:"cursor"`
		Limit   int                  `json:"limit"`
	}](w, r)
	if !ok {
		return
	}
	out, err := d.Views.Rows(r.Context(), wsID(r), in.Filters, in.Cursor, in.Limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) handleGroups(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		GroupBy string               `json:"group_by"`
		Filters workapp.QueryFilters `json:"filters"`
	}](w, r)
	if !ok {
		return
	}
	rows, err := d.Views.Groups(r.Context(), wsID(r), in.GroupBy, in.Filters)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, rows)
}

func (d Deps) handleFacets(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Filters workapp.QueryFilters `json:"filters"`
	}](w, r)
	if !ok {
		return
	}
	out, err := d.Views.Facets(r.Context(), wsID(r), in.Filters)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, out)
}

func (d Deps) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("mode") == "federated" {
		hits, err := d.Search.SearchFederated(r.Context(), wsID(r), r.URL.Query().Get("q"))
		if err != nil {
			writeErr(w, err)
			return
		}
		if hits == nil {
			hits = []domain.SearchHit{}
		}
		webx.WriteJSON(w, http.StatusOK, map[string]any{"items": hits})
		return
	}
	defs, _ := d.Catalog.ListStatuses(r.Context(), wsID(r))
	includeTerminal := r.URL.Query().Get("include_terminal") == "true"
	rows, err := d.Search.Search(r.Context(), wsID(r), r.URL.Query().Get("q"), includeTerminal,
		defs)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (d Deps) handleTriggerPreview(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p == nil {
		webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "authentication required", nil)
		return
	}
	if !workapp.TriggerEngineWired() {
		webx.WriteJSON(w, http.StatusOK, map[string]any{
			"result":      dispatchreason.Blocked,
			"reason_code": dispatchreason.TriggerEngineNotWired,
		})
		return
	}
	ws := wsID(r)
	var in struct {
		Content string `json:"content"`

		ChangeKind   string `json:"change_kind"`
		AssigneeType string `json:"assignee_type"`
		AssigneeID   string `json:"assignee_id"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&in)
	}
	id := r.PathValue("workItemId")

	if in.Content == "" && in.ChangeKind != "" {
		decision := d.WorkItems.WriteTriggerProbe(r.Context(), ws, id, workapp.WriteProbeFacts{
			ChangeKind: in.ChangeKind, AssigneeType: in.AssigneeType, AssigneeID: in.AssigneeID,
		})
		if decision.Trigger {
			webx.WriteJSON(w, http.StatusOK, map[string]any{"result": "would_trigger"})
			return
		}
		webx.WriteJSON(w, http.StatusOK, map[string]any{
			"result": dispatchreason.Blocked, "reason_code": decision.ReasonCode,
		})
		return
	}
	assigneeType, assigneeID := workapp.WorkItemAssigneeOf(r.Context(), id)

	var triageState *string
	if id != "" {
		if wi, err := d.WorkItems.Get(r.Context(), ws, id); err == nil {
			triageState = wi.TriageState
		}
	}
	triggers, supps, err := workapp.PreviewTrigger(r.Context(), workapp.TriggerInput{
		WorkspaceID: ws, WorkItemID: id,
		AuthorType: "user", AuthorID: p.UserID, Content: in.Content,
		AssigneeType: assigneeType, AssigneeID: assigneeID,
		TriageState: triageState,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if len(triggers) == 0 {
		rc := domain.ProbeNoTarget
		if len(supps) > 0 {
			rc = supps[0].Reason
		}
		webx.WriteJSON(w, http.StatusOK, map[string]any{
			"result": dispatchreason.Blocked, "reason_code": rc, "suppressions": supps,
		})
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{
		"result": "would_trigger", "triggers": triggers, "suppressions": supps,
	})
}

func (d Deps) handleListViews(w http.ResponseWriter, r *http.Request) {
	items, err := d.Views.Views(r.Context(), wsID(r), principal(r).UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if items == nil {
		items = []workapp.View{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (d Deps) handleSaveView(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[workapp.View](w, r)
	if !ok {
		return
	}
	v, err := d.Views.SaveView(r.Context(), wsID(r), principal(r).UserID, *in)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, v)
}

func (d Deps) handleViewPrefGet(w http.ResponseWriter, r *http.Request) {
	viewID, err := d.Views.ViewPreference(r.Context(), wsID(r), principal(r).UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"default_view_id": viewID})
}

func (d Deps) handleViewPrefPut(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		DefaultViewID *string `json:"default_view_id"`
	}](w, r)
	if !ok {
		return
	}
	if err := d.Views.SetViewPreference(r.Context(), wsID(r), principal(r).UserID, in.DefaultViewID); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"default_view_id": in.DefaultViewID})
}

func (d Deps) handleUnsubscribe(subtree bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := principal(r)
		in, ok := decode[struct {
			WorkItemID string `json:"work_item_id"`
			UserID     string `json:"user_id"`
		}](w, r)
		if !ok {
			return
		}
		if in.WorkItemID == "" {
			webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "work_item_id required", nil)
			return
		}
		target := in.UserID
		if target != "" && target != p.UserID {
			ok, err := d.MemberCheck(r.Context(), wsID(r), target)
			if err != nil || !ok {
				webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "target user is not a member of this workspace", nil)
				return
			}
		}
		var removed []string
		var err error
		if subtree {
			removed, err = d.WorkItems.UnsubscribeSubtree(r.Context(), wsID(r), in.WorkItemID, p.UserID, target)
		} else {
			_, err = d.WorkItems.Unsubscribe(r.Context(), wsID(r), in.WorkItemID, p.UserID, target)
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		webx.WriteJSON(w, http.StatusOK, map[string]any{"subscribed": false, "removed": removed})
	}
}

func urlQuery(r *http.Request) workapp.ListQuery {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit == 0 {
		limit = 50
	}
	var parent *string
	if pv := q.Get("parent"); pv != "" || q.Get("root_only") == "true" {
		parent = &pv
	}
	return workapp.ListQuery{
		WorkspaceID: wsID(r), StatusKeys: q["status"], AssigneeID: q.Get("assignee"),
		CreatorID: q.Get("creator"), SubscriberID: q.Get("subscriber"),
		LabelID: q.Get("label"), ProjectID: q.Get("project"), Search: q.Get("search"),
		Limit: limit, Cursor: q.Get("cursor"), ParentID: parent,
		Involves: q.Get("involves"), Props: propQueryParams(q),
	}
}

func propQueryParams(q url.Values) map[string]string {
	var props map[string]string
	for name, vals := range q {
		if !strings.HasPrefix(name, "prop_") || len(vals) == 0 {
			continue
		}
		key := strings.TrimPrefix(name, "prop_")
		if key == "" {
			continue
		}
		if props == nil {
			props = map[string]string{}
		}
		props[key] = vals[len(vals)-1]
	}
	return props
}

func mapToPatch(m map[string]any) workapp.WorkItemPatch {
	var p workapp.WorkItemPatch
	if v, ok := m["title"].(string); ok {
		p.Title = &v
	}
	if v, ok := m["status"].(string); ok {
		p.StatusKey = &v
	}
	if v, ok := m["priority"].(float64); ok {
		i := int(v)
		p.Priority = &i
	}
	if v, ok := m["assignee_id"].(string); ok {
		p.AssigneeID = &v
		if t, ok := m["assignee_type"].(string); ok {
			p.AssigneeType = &t
		}
	}
	if v, ok := m["properties"].(map[string]any); ok {
		p.Properties = v
	}
	return p
}

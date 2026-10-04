package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/webx"
)

func (d Deps) handleCreateBatch(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Kind        string           `json:"kind"`
		Params      map[string]any   `json:"params"`
		Source      map[string]any   `json:"source"`
		Concurrency int              `json:"concurrency"`
		ChunkSize   int              `json:"chunk_size"`
		Payloads    []map[string]any `json:"payloads"`
	}](w, r)
	if !ok {
		return
	}
	p := webx.PrincipalFrom(r.Context())
	b, err := d.Batches.CreateAndMaterialize(r.Context(), orchapp.Batch{
		WorkspaceID: d.ws(r), WorkItemID: chi.URLParam(r, "workItemId"),
		Kind: in.Kind, Params: in.Params, Source: in.Source,
		Concurrency: in.Concurrency, ChunkSize: in.ChunkSize,
		OriginatorUserID: p.UserID,
	}, in.Payloads)
	if err != nil {

		var ae *apierr.Error
		if errors.As(err, &ae) {
			writeErr(w, err)
			return
		}
		writeErr(w, apierr.NewValidation(err.Error()))
		return
	}
	webx.WriteJSON(w, http.StatusCreated, b)
}

func (d Deps) handleListBatches(w http.ResponseWriter, r *http.Request) {
	b, err := d.Batches.ListByWorkItem(r.Context(), d.ws(r), chi.URLParam(r, "workItemId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"batches": orEmpty(b)})
}

func (d Deps) handleGetBatch(w http.ResponseWriter, r *http.Request) {
	b, ok, err := d.Batches.Get(r.Context(), d.ws(r), chi.URLParam(r, "batchId"))
	if err != nil || !ok {
		writeErr(w, apierr.New(apierr.NotFound, "batch not found"))
		return
	}
	pending, claimed, processing, done, _ := d.Batches.Counts(r.Context(), b.ID)
	webx.WriteJSON(w, http.StatusOK, map[string]any{
		"batch": b, "pending": pending, "claimed": claimed, "processing": processing, "done": done,
	})
}

func (d Deps) handleBatchItems(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 1000 {
		limit = n
	}
	items, err := d.Batches.ListItems(r.Context(), d.ws(r), chi.URLParam(r, "batchId"),
		r.URL.Query().Get("status"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": orEmpty(items)})
}

func (d Deps) handleBatchPause(w http.ResponseWriter, r *http.Request) {
	if err := d.Batches.Pause(r.Context(), d.ws(r), chi.URLParam(r, "batchId")); err != nil {
		writeErr(w, apierr.New(apierr.Conflict, err.Error()))
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "paused"})
}

func (d Deps) handleBatchResume(w http.ResponseWriter, r *http.Request) {
	if err := d.Batches.Resume(r.Context(), d.ws(r), chi.URLParam(r, "batchId")); err != nil {
		writeErr(w, apierr.New(apierr.Conflict, err.Error()))
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "resumed"})
}

func (d Deps) handleBatchCancel(w http.ResponseWriter, r *http.Request) {
	if err := d.Batches.Cancel(r.Context(), d.ws(r), chi.URLParam(r, "batchId")); err != nil {
		writeErr(w, apierr.New(apierr.Conflict, err.Error()))
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "cancelled"})
}

func (d Deps) handleBatchRetryFailed(w http.ResponseWriter, r *http.Request) {
	n, err := d.Batches.RequeueFailed(r.Context(), d.ws(r), chi.URLParam(r, "batchId"))
	if err != nil {
		writeErr(w, apierr.New(apierr.Conflict, err.Error()))
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"requeued": n})
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/haozing/ploykit/platform/webx"
	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
)

func (d Deps) handleCreateBatch(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[struct {
		Kind            string           `json:"kind"`
		Params          map[string]any   `json:"params"`
		Source          map[string]any   `json:"source"`
		Concurrency     int              `json:"concurrency"`
		ChunkSize       int              `json:"chunk_size"`
		Payloads        []map[string]any `json:"payloads"`
		PayloadTemplate map[string]any   `json:"payload_template"`
		Required        []string         `json:"required"`
	}](w, r)
	if !ok {
		return
	}
	p := webx.PrincipalFrom(r.Context())
	base := orchapp.Batch{
		WorkspaceID: d.ws(r), WorkItemID: r.PathValue("workItemId"),
		Kind: in.Kind, Params: in.Params,
		Concurrency: in.Concurrency, ChunkSize: in.ChunkSize,
		OriginatorUserID: p.UserID,
	}
	// 双通道（batch v2.0）：source+template → 通道 B（物化落 draft，响应带
	// 对账与预览）；否则通道 A inline（原行为，自动开跑）。
	if in.Source != nil {
		base.Source = map[string]any{
			"source":           in.Source,
			"payload_template": in.PayloadTemplate,
			"required":         in.Required,
		}
		b, err := d.Batches.CreateDraftFromSource(r.Context(), base, d.BatchSourceMater)
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
		return
	}
	b, err := d.Batches.CreateAndMaterialize(r.Context(), base, in.Payloads)
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
	b, err := d.Batches.ListByWorkItem(r.Context(), d.ws(r), r.PathValue("workItemId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"batches": orEmpty(b)})
}

func (d Deps) handleGetBatch(w http.ResponseWriter, r *http.Request) {
	b, ok, err := d.Batches.Get(r.Context(), d.ws(r), r.PathValue("batchId"))
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
	items, err := d.Batches.ListItems(r.Context(), d.ws(r), r.PathValue("batchId"),
		r.URL.Query().Get("status"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": orEmpty(items)})
}

// handleBatchItemsPage 结果收割分页（seq 游标；batch_results 工具同源）。
func (d Deps) handleBatchItemsPage(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	afterSeq, _ := strconv.Atoi(r.URL.Query().Get("after_seq"))
	items, err := d.Batches.ListItemsPage(r.Context(), d.ws(r), r.PathValue("batchId"),
		r.URL.Query().Get("status"), afterSeq, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": orEmpty(items)})
}

// handleBatchStart draft→pending（通道 B：物化预览确认后开跑）。
func (d Deps) handleBatchStart(w http.ResponseWriter, r *http.Request) {
	if err := d.Batches.Start(r.Context(), d.ws(r), r.PathValue("batchId")); err != nil {
		writeErr(w, apierr.New(apierr.Conflict, err.Error()))
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "started"})
}

func (d Deps) handleBatchPause(w http.ResponseWriter, r *http.Request) {
	if err := d.Batches.Pause(r.Context(), d.ws(r), r.PathValue("batchId")); err != nil {
		writeErr(w, apierr.New(apierr.Conflict, err.Error()))
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "paused"})
}

func (d Deps) handleBatchResume(w http.ResponseWriter, r *http.Request) {
	if err := d.Batches.Resume(r.Context(), d.ws(r), r.PathValue("batchId")); err != nil {
		writeErr(w, apierr.New(apierr.Conflict, err.Error()))
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "resumed"})
}

func (d Deps) handleBatchCancel(w http.ResponseWriter, r *http.Request) {
	if err := d.Batches.Cancel(r.Context(), d.ws(r), r.PathValue("batchId")); err != nil {
		writeErr(w, apierr.New(apierr.Conflict, err.Error()))
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "cancelled"})
}

func (d Deps) handleBatchRetryFailed(w http.ResponseWriter, r *http.Request) {
	n, err := d.Batches.RequeueFailed(r.Context(), d.ws(r), r.PathValue("batchId"))
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

package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/haozing/ploykit/platform/webx"

	"orkome/internal/contract/apierr"
)

func writeErr(w http.ResponseWriter, err error) {
	var ae *apierr.Error
	if errors.As(err, &ae) {
		status := map[string]int{
			apierr.Unauthenticated: 401, apierr.Forbidden: 403, apierr.NotFound: 404,
			apierr.Validation: 400, apierr.Conflict: 409,
		}[ae.Code]
		if status == 0 {
			status = 500
		}
		webx.WriteError(w, status, ae.Code, ae.Message, ae.Details)
		return
	}
	webx.WriteError(w, http.StatusInternalServerError, apierr.Internal, "internal error", nil)
}

func (d Deps) ws(r *http.Request) string { return webx.PrincipalFrom(r.Context()).WorkspaceID }

// GET /api/packs
func (d Deps) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := d.Packs.List(r.Context(), d.ws(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"items": list})
}

// GET /api/packs/{packId}
func (d Deps) handleGet(w http.ResponseWriter, r *http.Request) {
	detail, err := d.Packs.Get(r.Context(), d.ws(r), r.PathValue("packId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	// 嵌套形状 {pack: 元数据, servers/tools/...: 五区块}——包元数据与聚合
	// 分离，前端契约见 web/src/api/packs.ts PackDetail。
	webx.WriteJSON(w, http.StatusOK, map[string]any{
		"pack":          detail.Pack,
		"servers":       detail.Servers,
		"tool_schemas":  detail.ToolSchemas,
		"ui_entry":      detail.UIEntry,
		"subscriptions": detail.Subscriptions,
		"agents":        detail.Agents,
		"skills":        detail.Skills,
	})
}

// PUT /api/packs（admin；provision 第 0 步，body {name, version}）
func (d Deps) handleUpsert(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	var in struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	p, err := d.Packs.Upsert(r.Context(), d.ws(r), in.Name, in.Version)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, p)
}

// DELETE /api/packs/{packId}（admin；P1 最小卸载）
func (d Deps) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	if err := d.Packs.Delete(r.Context(), d.ws(r), r.PathValue("packId")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/packs/{packId}/subscriptions（admin；provision 订阅创建后关联
// 连接表——订阅本体在 vendor 表，此处只写 pack_subscription）。
func (d Deps) handleLinkSubscription(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	var in struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	if in.SubscriptionID == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "subscription_id required", nil)
		return
	}
	packID := r.PathValue("packId")
	if err := d.Packs.Owns(r.Context(), d.ws(r), packID); err != nil {
		writeErr(w, err)
		return
	}
	if err := d.Packs.LinkSubscription(r.Context(), packID, in.SubscriptionID); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "linked"})
}

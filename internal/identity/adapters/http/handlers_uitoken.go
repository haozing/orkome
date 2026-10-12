package http

import (
	"encoding/json"
	"net/http"

	"github.com/haozing/ploykit/platform/webx"

	"orkome/internal/contract/apierr"
)

func (d Deps) handleUITokenMint(w http.ResponseWriter, r *http.Request) {
	p := webx.PrincipalFrom(r.Context())
	if p == nil {
		webx.WriteError(w, http.StatusUnauthorized, apierr.Unauthenticated, "authentication required", nil)
		return
	}
	var in struct {
		Plugin      string `json:"plugin"`
		WorkspaceID string `json:"workspace_id"`
		// WorkItemID 可选（FP-15 P1-2）：非空时平台校验该工单 ∈ workspace_id，
		// 通过后写进票 claims，pack 侧凭 consume 响应比对 URL 参数。
		WorkItemID string `json:"work_item_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil ||
		in.Plugin == "" || in.WorkspaceID == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "plugin and workspace_id required", nil)
		return
	}

	if _, ok, err := d.Repo.GetMember(r.Context(), in.WorkspaceID, p.UserID); err != nil || !ok {
		webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "workspace membership required", nil)
		return
	}
	token, err := d.UITokens.Mint(r.Context(), in.WorkspaceID, p.UserID, in.Plugin, in.WorkItemID)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, map[string]any{"token": token, "expires_in": 60})
}

// handleUITokenConsume 以票自证（FP-15 P1-1）：jti 单次 DB 原子消费 + 60s TTL
// 即凭证，本 handler 不读也不要求会话 Principal——pack iframe / pack 后端
// 没有会话也没有 PAT，会话门只会把回验链锁死（路由侧已摘鉴权壳）。
func (d Deps) handleUITokenConsume(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil || in.Token == "" {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "token required", nil)
		return
	}
	claims, err := d.UITokens.Consume(r.Context(), in.Token)
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{
		"user_id": claims.Sub, "workspace_id": claims.WorkspaceID, "plugin": claims.Plugin,
		"work_item_id": claims.WorkItemID,
	})
}

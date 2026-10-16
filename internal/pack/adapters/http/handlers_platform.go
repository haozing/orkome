package http

// 能力包平台化的三个端点（挂载进 packhttp.Mount 集中管理）：
//
//	GET  /api/packs/{packId}/usage-document      member LLM 用法文档
//	POST /api/agents/{agentId}/pack              admin  挂载/卸载 agent 归属
//	POST /api/skills/{skillId}/pack              admin  挂载/卸载 skill 归属
//
// agent/skill 的本体写是 orchestration / skills 域的事——packhttp 只经注入
// 的窄接口（AgentBinder / SkillBinder）透传，不 import 那两个域
//（arch iron law：跨域经端口接口 + 组合根适配）。
//
// 历史注记：POST /api/packs/install（安装向导编排器）已随"集成页终态"
// 裁决退役（2026-10-15）——装包统一走 pack.yaml 分发（provision CLI），
// 运行时配置走集成页三区。

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/haozing/ploykit/platform/webx"

	"orkome/internal/contract/apierr"
)

// AgentBinder 是 orchestration 域 agent 归属写的窄端口（组合根适配注入）。
type AgentBinder interface {
	SetAgentPack(ctx context.Context, workspaceID, agentID string, packID *string) error
}

// SkillBinder 是 skills 域 skill 归属写的窄端口（同上）。
type SkillBinder interface {
	SetSkillPack(ctx context.Context, ws, skillID string, packID *string) error
}

// ---- GET /api/packs/{packId}/usage-document ----

func (d Deps) handleUsageDocument(w http.ResponseWriter, r *http.Request) {
	if !d.ToolDetailsWired {
		webx.WriteError(w, http.StatusServiceUnavailable, apierr.Internal, "tool schema detail reader not wired", nil)
		return
	}
	md, err := d.Packs.UsageDocument(r.Context(), d.ws(r), r.PathValue("packId"))
	if err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"markdown": md})
}

// ---- POST /api/agents/{agentId}/pack、POST /api/skills/{skillId}/pack ----
// body {pack_id: string}，空串=卸下。pack_id 非空先过 Packs.Owns 防跨区挂载；
// agent/skill 本体的 ws 归属由各域实现收口（rows affected=0 → 404）。

func (d Deps) handleAgentPack(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	if d.AgentBinder == nil {
		webx.WriteError(w, http.StatusServiceUnavailable, apierr.Internal, "agent binder not wired", nil)
		return
	}
	packID, ok := d.decodePackRef(w, r)
	if !ok {
		return
	}
	if err := d.AgentBinder.SetAgentPack(r.Context(), d.ws(r), r.PathValue("agentId"), packID); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "bound"})
}

func (d Deps) handleSkillPack(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	if d.SkillBinder == nil {
		webx.WriteError(w, http.StatusServiceUnavailable, apierr.Internal, "skill binder not wired", nil)
		return
	}
	packID, ok := d.decodePackRef(w, r)
	if !ok {
		return
	}
	if err := d.SkillBinder.SetSkillPack(r.Context(), d.ws(r), r.PathValue("skillId"), packID); err != nil {
		writeErr(w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "bound"})
}

// decodePackRef 解析 {pack_id} body：空串 = 卸下（nil 指针）；非空 = 先过
// Owns 归属闸（防跨区挂载），失败即短路。
func (d Deps) decodePackRef(w http.ResponseWriter, r *http.Request) (*string, bool) {
	var in struct {
		PackID string `json:"pack_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return nil, false
	}
	if in.PackID == "" {
		return nil, true // 卸下
	}
	if err := d.Packs.Owns(r.Context(), d.ws(r), in.PackID); err != nil {
		writeErr(w, err)
		return nil, false
	}
	return &in.PackID, true
}

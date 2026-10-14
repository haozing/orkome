package http

// 能力包平台化的四个新端点（挂载进 packhttp.Mount 集中管理）：
//
//	POST /api/packs/install                      admin  一键安装编排
//	GET  /api/packs/{packId}/usage-document      member LLM 用法文档
//	POST /api/agents/{agentId}/pack              admin  挂载/卸载 agent 归属
//	POST /api/skills/{skillId}/pack              admin  挂载/卸载 skill 归属
//
// agent/skill 的本体写是 orchestration / skills 域的事——packhttp 只经注入
// 的窄接口（AgentBinder / SkillBinder）透传，不 import 那两个域
//（arch iron law：跨域经端口接口 + 组合根适配）。

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/haozing/ploykit/platform/webx"

	"orkome/internal/contract/apierr"
	"orkome/internal/pack/app"
)

// AgentBinder 是 orchestration 域 agent 归属写的窄端口（组合根适配注入）。
type AgentBinder interface {
	SetAgentPack(ctx context.Context, workspaceID, agentID string, packID *string) error
}

// SkillBinder 是 skills 域 skill 归属写的窄端口（同上）。
type SkillBinder interface {
	SetSkillPack(ctx context.Context, ws, skillID string, packID *string) error
}

// ---- POST /api/packs/install ----

// installWire 是 InstallSpec 的 wire 形状（snake_case，与 app 层 tag 一致，
// 这里显式重申一层以便未来 wire 演进与领域形状解耦）。
type installWire struct {
	Name    string         `json:"name"`
	Version string         `json:"version"`
	MCP     []installMCP   `json:"mcp"`
	Events  []installEvent `json:"events"`
	UI      *installUI     `json:"ui"`
}

type installMCP struct {
	Name       string       `json:"name"`
	Transport  string       `json:"transport"`
	URL        string       `json:"url"`
	Credential *installCred `json:"credential"`
}

type installCred struct {
	Provider string `json:"provider"`
	Secret   string `json:"secret"`
}

type installEvent struct {
	URL         string   `json:"url"`
	Types       []string `json:"types"`
	Description string   `json:"description"`
}

type installUI struct {
	Label       string          `json:"label"`
	URLTemplate string          `json:"url_template"`
	Slots       []installUISlot `json:"slots"`
}

type installUISlot struct {
	Slot        string `json:"slot"`
	Label       string `json:"label"`
	URLTemplate string `json:"url_template"`
	Height      int    `json:"height"`
}

func (x installWire) toSpec() app.InstallSpec {
	spec := app.InstallSpec{Name: x.Name, Version: x.Version}
	for _, m := range x.MCP {
		im := app.InstallMCP{Name: m.Name, Transport: m.Transport, URL: m.URL}
		if m.Credential != nil {
			im.Credential = &app.InstallCredential{Provider: m.Credential.Provider, Secret: m.Credential.Secret}
		}
		spec.MCP = append(spec.MCP, im)
	}
	for _, e := range x.Events {
		spec.Events = append(spec.Events, app.InstallEvent{URL: e.URL, Types: e.Types, Description: e.Description})
	}
	if x.UI != nil {
		ui := &app.InstallUI{Label: x.UI.Label, URLTemplate: x.UI.URLTemplate}
		for _, sl := range x.UI.Slots {
			ui.Slots = append(ui.Slots, app.InstallUISlot{Slot: sl.Slot, Label: sl.Label, URLTemplate: sl.URLTemplate, Height: sl.Height})
		}
		spec.UI = ui
	}
	return spec
}

// handleInstall 一键安装（admin）。装配缺失（*not wired）映射 503——
// 与 LinkSubscription 的 "not wired" 语义同款：这不是调用方的错。
func (d Deps) handleInstall(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	if !d.InstallWired {
		webx.WriteError(w, http.StatusServiceUnavailable, apierr.Internal, "install ports not wired", nil)
		return
	}
	var in installWire
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		webx.WriteError(w, http.StatusBadRequest, apierr.Validation, "invalid JSON body", nil)
		return
	}
	res, err := d.Packs.Install(r.Context(), d.ws(r), webx.PrincipalFrom(r.Context()).UserID, in.toSpec())
	if err != nil {
		writeErr(w, err)
		return
	}
	if res.Subscriptions == nil {
		res.Subscriptions = []app.InstalledSubscription{}
	}
	webx.WriteJSON(w, http.StatusCreated, res)
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

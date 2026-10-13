package http

// MCP 服务器两级注册表的 HTTP handler（W2-3 自 routes.go 拆出为本文件，
// MCP 契约的独占维护点）。ws 级 /api/workspaces/{wsId}/mcp-servers 与
// agent 级 /api/agents/{agentId}/mcp-servers 共用同一套 wire 语义。
//
// # 三态凭证语义（FP-09 P1-4 裁决：两级统一）
//
// PUT body 的 credentials 用 json.RawMessage 区分"缺字段 vs 显式空"
// （不能用指针：*map 对显式 null 会解成 nil 指针，与缺字段不可区分）：
//   - 字段缺失 → nil map → SQL NULL → coalesce 保留库中现值；
//   - 显式 null / {} → 非 nil 空 map → 落 '{}' → 显式清空；
//   - 新值 → 覆盖写入（repo 侧 sealx 密封，见 pgrepo/repo_mcp.go）。
//
// Create 侧无"保留"可言，缺省即空凭证，与显式空等价。

import (
	"encoding/json"
	"net/http"

	"github.com/haozing/ploykit/platform/webx"

	"orkome/internal/contract/apierr"
	orchapp "orkome/internal/orchestration/app"
)

func (d Deps) handleMCPList(w http.ResponseWriter, r *http.Request) {
	list, err := d.MCP.List(r.Context(), d.ws(r))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if list == nil {
		list = []orchapp.WorkspaceMCP{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"servers": list})
}

func (d Deps) handleMCPCreate(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[mcpWire](w, r)
	if !ok {
		return
	}
	m, err := in.toModel(d.ws(r))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	created, err := d.MCP.Create(r.Context(), m)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, created)
}

func (d Deps) handleMCPUpdate(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[mcpWire](w, r)
	if !ok {
		return
	}
	m, err := in.toModel(d.ws(r))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	m.ID = r.PathValue("serverId")
	updated, err := d.MCP.Update(r.Context(), m)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, updated)
}

type mcpWire struct {
	Name string `json:"name"`
	// Credentials 用 RawMessage 承载三态（不用指针：*map 对显式 null 会解成
	// nil 指针，与"字段缺失"不可区分，三态塌缩成两态）。
	Credentials json.RawMessage `json:"credentials"`
	Transport   string          `json:"transport"`
	URL         string          `json:"url"`
	// PackID 来源能力包（pack 一等公民 §1.2）：provision 通道传入；
	// 空 = 手工注册。服务端不校验存在性（FK 由落库兜底）。
	PackID string `json:"pack_id"`
}

func (x mcpWire) toModel(ws string) (orchapp.WorkspaceMCP, error) {
	creds, err := credsWireToModel(x.Credentials)
	if err != nil {
		return orchapp.WorkspaceMCP{}, err
	}
	return orchapp.WorkspaceMCP{WorkspaceID: ws, Name: x.Name, Transport: x.Transport,
		URL: x.URL, Credentials: creds, PackID: x.PackID}, nil
}

func (d Deps) handleMCPDelete(w http.ResponseWriter, r *http.Request) {
	if err := d.MCP.Delete(r.Context(), d.ws(r), r.PathValue("serverId")); err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "deleted"})
}

// handleMCPProbeTools 实时枚举一台工作区 MCP 服务器的工具清单（工具集成页
// "查看工具"）。POST 语义：有副作用之外的实时外呼（建连目标服务器），不做
// 缓存——每次点击都是当下真实状态。
func (d Deps) handleMCPProbeTools(w http.ResponseWriter, r *http.Request) {
	tools, err := d.MCP.ProbeTools(r.Context(), d.ws(r), r.PathValue("serverId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if tools == nil {
		tools = []orchapp.ProbeTool{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

func (d Deps) handleAgentMCP(w http.ResponseWriter, r *http.Request) {
	ws := d.ws(r)
	agentID := r.PathValue("agentId")
	if r.URL.Query().Get("resolved") == "1" {
		list, err := d.MCP.Resolved(r.Context(), ws, agentID)
		if err != nil {
			writeErrReq(r, w, err)
			return
		}
		webx.WriteJSON(w, http.StatusOK, map[string]any{"servers": list})
		return
	}
	list, err := d.MCP.AgentMCP(r.Context(), ws, agentID)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if list == nil {
		list = []orchapp.AgentMCP{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"servers": list})
}

type agentMCPWire struct {
	Name string `json:"name"`
	// Credentials 三态口径同 mcpWire（两级统一）。
	Credentials json.RawMessage `json:"credentials"`
	Transport   string          `json:"transport"`
	URL         string          `json:"url"`
	Enabled     *bool           `json:"enabled"`
}

func (x agentMCPWire) toModel(ws, agentID, id string) (orchapp.AgentMCP, error) {
	enabled := true
	if x.Enabled != nil {
		enabled = *x.Enabled
	}
	creds, err := credsWireToModel(x.Credentials)
	if err != nil {
		return orchapp.AgentMCP{}, err
	}
	return orchapp.AgentMCP{ID: id, WorkspaceID: ws, AgentID: agentID, Name: x.Name,
		Transport: x.Transport, URL: x.URL, Enabled: enabled,
		Credentials: creds}, nil
}

// credsWireToModel：三态 wire → model 的归一出口（FP-09 P1-4 裁决，两级统一）。
//   - 字段缺失（len==0）→ nil map → pg 层 coalesce 保留库中现值；
//   - 显式 null / {} → 非 nil 空 map → pg 层落 '{}' = 显式清空；
//   - 对象 → 反序列化 map → 覆盖写入（pg 层 sealx 密封，见 pgrepo/repo_mcp.go）；
//   - 其余 JSON 标量/数组 → 400（credentials 只接受对象）。
func credsWireToModel(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if string(raw) == "null" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, apierr.New(apierr.Validation, "credentials must be a JSON object: "+err.Error())
	}
	if m == nil {
		return map[string]any{}, nil
	}
	return m, nil
}

func (d Deps) handleAgentMCPCreate(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[agentMCPWire](w, r)
	if !ok {
		return
	}
	m, err := in.toModel(d.ws(r), r.PathValue("agentId"), "")
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	created, err := d.MCP.CreateAgentMCP(r.Context(), m)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, created)
}

func (d Deps) handleAgentMCPUpdate(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[agentMCPWire](w, r)
	if !ok {
		return
	}
	m, err := in.toModel(d.ws(r), r.PathValue("agentId"), r.PathValue("serverId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	updated, err := d.MCP.UpdateAgentMCP(r.Context(), m)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, updated)
}

func (d Deps) handleAgentMCPDelete(w http.ResponseWriter, r *http.Request) {
	if err := d.MCP.DeleteAgentMCP(r.Context(), d.ws(r), r.PathValue("agentId"), r.PathValue("serverId")); err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "deleted"})
}

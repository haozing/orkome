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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

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
	out := make([]mcpWSOut, 0, len(list))
	for _, m := range list {
		out = append(out, mcpWSOut{WorkspaceMCP: m, AuthEffective: authEffective(m.Credentials)})
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"servers": out})
}

// handleMCPCreate ws 级创建（P2-D7：写面 admin 闸——挂法照抄同文件
// credentials/ui-entries 先例，handler 首行 requireWorkspaceAdmin；读面
// GET 与 probe 保持成员闸）。
func (d Deps) handleMCPCreate(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	in, ok := decode[mcpWire](w, r)
	if !ok {
		return
	}
	m, err := in.toModel(d.ws(r))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if err := d.validateMCPCreds(r.Context(), d.ws(r), m.Credentials); err != nil {
		writeErrReq(r, w, err)
		return
	}
	created, err := d.MCP.Create(r.Context(), m)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, mcpWSOut{WorkspaceMCP: *created, AuthEffective: authEffective(created.Credentials)})
}

func (d Deps) handleMCPUpdate(w http.ResponseWriter, r *http.Request) {
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	in, ok := decode[mcpWire](w, r)
	if !ok {
		return
	}
	m, err := in.toModel(d.ws(r))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if err := d.validateMCPCreds(r.Context(), d.ws(r), m.Credentials); err != nil {
		writeErrReq(r, w, err)
		return
	}
	m.ID = r.PathValue("serverId")
	updated, err := d.MCP.Update(r.Context(), m)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, mcpWSOut{WorkspaceMCP: *updated, AuthEffective: authEffective(updated.Credentials)})
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
	if !requireWorkspaceAdmin(w, r) {
		return
	}
	if err := d.MCP.Delete(r.Context(), d.ws(r), r.PathValue("serverId")); err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "deleted"})
}

// handleMCPProbeTools 实时枚举一台工作区 MCP 服务器的工具清单（工具集成页
// "查看工具"）。POST 语义：有副作用之外的实时外呼（建连目标服务器），不做
// 缓存——每次点击都是当下真实状态。total 是截断前总数（P0-5）：tools 在
// 32 上限截断时 total 与 len(tools) 之差即被截掉的部分。
func (d Deps) handleMCPProbeTools(w http.ResponseWriter, r *http.Request) {
	tools, total, err := d.MCP.ProbeTools(r.Context(), d.ws(r), r.PathValue("serverId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if tools == nil {
		tools = []orchapp.ProbeTool{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"tools": tools, "total": total})
}

// handleAgentMCPProbeTools agent 级条目的实时工具探针（P1-3）：agent 级
// 凭证配错此前只能在任务执行里炸，注册页与 ws 级同权即时验证。
func (d Deps) handleAgentMCPProbeTools(w http.ResponseWriter, r *http.Request) {
	tools, total, err := d.MCP.ProbeAgentTools(r.Context(), d.ws(r), r.PathValue("agentId"), r.PathValue("serverId"))
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	if tools == nil {
		tools = []orchapp.ProbeTool{}
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"tools": tools, "total": total})
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
	out := make([]mcpAgentOut, 0, len(list))
	for _, m := range list {
		out = append(out, mcpAgentOut{AgentMCP: m, AuthEffective: authEffective(m.Credentials)})
	}
	webx.WriteJSON(w, http.StatusOK, map[string]any{"servers": out})
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
	if err := d.validateMCPCreds(r.Context(), d.ws(r), m.Credentials); err != nil {
		writeErrReq(r, w, err)
		return
	}
	created, err := d.MCP.CreateAgentMCP(r.Context(), m)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusCreated, mcpAgentOut{AgentMCP: *created, AuthEffective: authEffective(created.Credentials)})
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
	if err := d.validateMCPCreds(r.Context(), d.ws(r), m.Credentials); err != nil {
		writeErrReq(r, w, err)
		return
	}
	updated, err := d.MCP.UpdateAgentMCP(r.Context(), m)
	if err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, mcpAgentOut{AgentMCP: *updated, AuthEffective: authEffective(updated.Credentials)})
}

func (d Deps) handleAgentMCPDelete(w http.ResponseWriter, r *http.Request) {
	if err := d.MCP.DeleteAgentMCP(r.Context(), d.ws(r), r.PathValue("agentId"), r.PathValue("serverId")); err != nil {
		writeErrReq(r, w, err)
		return
	}
	webx.WriteJSON(w, http.StatusOK, map[string]string{"result": "deleted"})
}

// mcpWSOut / mcpAgentOut：模型响应外再包一层补 auth_effective（P1-4）。
// 嵌入展平——原 JSON 形状不变，仅在 has_credentials 旁多 auth_effective 一键。
type mcpWSOut struct {
	orchapp.WorkspaceMCP
	AuthEffective bool `json:"auth_effective"`
}

type mcpAgentOut struct {
	orchapp.AgentMCP
	AuthEffective bool `json:"auth_effective"`
}

// authEffective：认证是否真正生效 = credentials 信封里 headers 子对象存在
// 且非空（引擎 mcpHeadersOf 只消费 headers；credential_id/api_key 之类的
// 键对 MCP 连接不生效——has_credentials 只说明"存了东西"，会误导排障）。
func authEffective(creds map[string]any) bool {
	h, ok := creds["headers"].(map[string]any)
	return ok && len(h) > 0
}

// headerTokenRe：合法 HTTP 头字段名字符集（RFC 9110 token）。net/http 未
// 导出 IsHeadersName 一类的判定，按 token 字符集自建（P2-D8）。
var headerTokenRe = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_|~-]+$`)

// validateMCPCreds 写面凭证信封校验（P1-4 / P2-D8，REST Create/Update 双侧、
// ws 与 agent 两级同口径）：
//   - credential_id 键存在 → 必须指向本工作区已存在的凭证（幽灵关联堵死：
//     引擎不消费该键，指向不存在的 id 只会在排障时误导）；
//   - headers 子对象的键必须是合法 HTTP token；值必须是字符串且不含
//     \r \n \0（HTTP 头值禁止换行注入；tab 在头值中合法故不拦）。
//
// creds == nil（Update 缺字段 = 保留库中现值）时不校验——校验对象是请求
// 显式携带的信封，不是存量。
func (d Deps) validateMCPCreds(ctx context.Context, ws string, creds map[string]any) error {
	if creds == nil {
		return nil
	}
	if _, ok := creds["credential_id"]; ok {
		id, _ := creds["credential_id"].(string)
		if d.Repo == nil {
			return apierr.New(apierr.Internal, "repo not wired (cannot validate credential_id)")
		}
		if _, _, found, err := d.Repo.GetCredential(ctx, ws, id); err != nil {
			return err
		} else if !found {
			return apierr.NewValidation("credential_id does not reference an existing credential",
				apierr.FieldError{Field: "credentials.credential_id", Problem: "credential not found in this workspace"})
		}
	}
	headers, _ := creds["headers"].(map[string]any)
	for k, v := range headers {
		if !headerTokenRe.MatchString(k) {
			return apierr.NewValidation("invalid header name",
				apierr.FieldError{Field: "credentials.headers", Problem: fmt.Sprintf("key %q is not a valid HTTP header name", k)})
		}
		val, ok := v.(string)
		if !ok {
			return apierr.NewValidation("invalid header value",
				apierr.FieldError{Field: "credentials.headers", Problem: fmt.Sprintf("value of %q must be a string", k)})
		}
		if strings.ContainsAny(val, "\r\n\x00") {
			return apierr.NewValidation("invalid header value",
				apierr.FieldError{Field: "credentials.headers", Problem: fmt.Sprintf("value of %q must not contain control characters (CR/LF/NUL)", k)})
		}
	}
	return nil
}

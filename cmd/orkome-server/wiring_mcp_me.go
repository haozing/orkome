package main

// 助理 MCP 端点（docs/assistant-access-design-2026-10-14.md v4 §3）：
// POST/GET /api/mcp/w/{workspaceId}/me——用户本机 AI 客户端以用户本人身份
// 接入。与 mountAgentMCP（工人/external runtime）并列的第二个 MCP 面：
//   - 只认用户 PAT：runtime credential PAT 挂虚拟用户且 principal.AgentID
//     非空，直接 403（agentBoundPAT 在认证链填充 AgentID，天然可判别）；
//   - workspace 走 URL path（复用 workspaceMW 成员闸先例，v4 §3.1）；
//   - 23 工具 = 读 7 + 写 3 + 任务协议 5 + 恢复 1 + 配置 7（§3.2 表格 +
//     知识库读 + 配置面；包级 pack 工具随能力包退役拆除，2026-10-16）。
//
// 跨域编排（list_my_claims / get_my_running_tasks 等）在本层完成：先经
// orchestration 解析 proxy 行，再调 work 域 List——HTTP handler 所在的 cmd
// 包可以两域都 import，域层保持零互依（v4 §3.2 裁决）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/haozing/ploykit/platform/pg"
	webx "github.com/haozing/ploykit/platform/webx"
	webhookapp "github.com/haozing/ploykit/webhooks/app"

	"github.com/haozing/ploykit/platform/egressx"
	autobhttp "orkome/internal/automation/adapters/http"
	contentapp "orkome/internal/content/app"
	"orkome/internal/contract/apierr"
	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	"orkome/internal/platform/eventbus"
	skillapp "orkome/internal/skills/app"
	workpgrepo "orkome/internal/work/adapters/pgrepo"
	workapp "orkome/internal/work/app"
)

// assistantMEDeps 助理端点的服务面。wiring 传入的 WorkItemService/
// CommentService/CatalogService 与 REST/workhttp 面是同一实例（wire 的
// workDeps 单例）——业务层一套，行为（事件/outbox/触发）天然同构。
// skillSvc：技能/资料库读取（skill 即知识库裁决——绑不绑 agent 是自然分野，
// 助理面按 workspace 级读，不感知绑定）。
type assistantMEDeps struct {
	orchRepo  *orchpgrepo.Repo
	assistant *orchapp.AssistantTaskService
	wiSvc     *workapp.WorkItemService
	catSvc    *workapp.CatalogService
	cmSvc     *workapp.CommentService
	skillSvc  *skillapp.SkillService
	// attachments 工单附件只读清单（get_work_item 响应面）；上传/删除走 REST。
	attachments *contentapp.AttachmentService
	roleOf      func(ctx context.Context, workspaceID, userID string) (string, error)
	// timeline work 域时间线读取（wiring 注入 wRepo.ListTimeline 绑定）。
	timeline func(ctx context.Context, wiID string, limit int, before string) ([]workapp.TimelineEntry, error)
	// ---- 配置面（集成页 parity）----
	// 与 REST/集成页同一批 service 实例：AI 能配置的与手动界面能做的完全同集。
	mcpSvc     *orchapp.MCPServerService
	credSvc    *orchapp.CredentialService
	uiEntrySvc *orchapp.PackUIEntryService
	webhookSvc *webhookapp.WebhookService
	// ---- 包级已退役（2026-10-16 裁决）：能力包安装向导/挂载自集成页终态
	// 退役后，pack 工具不再对助理面暴露（list_packs/register_pack/
	// uninstall_pack 整组拆除）；pack_id 归属参数一并撤下——遗留 pack 台账
	// 由 REST /api/packs 与包详情页管理。
	webOrigins  []string
	egressCIDRs []string
}

// mountAssistantMCP 装配 /api/mcp/w/{workspaceId}/me。rl 可空（路由契约
// 测试路径不挂限流）。
func mountAssistantMCP(api *http.ServeMux, auth func(http.Handler) http.Handler,
	db *pg.DB, bus *eventbus.Bus, assistant *orchapp.AssistantTaskService,
	wiSvc *workapp.WorkItemService, cmSvc *workapp.CommentService, catSvc *workapp.CatalogService,
	skillSvc *skillapp.SkillService, attachments *contentapp.AttachmentService,
	mcpSvc *orchapp.MCPServerService, credSvc *orchapp.CredentialService,
	uiEntrySvc *orchapp.PackUIEntryService, webhookSvc *webhookapp.WebhookService,
	webOrigins []string, egressCIDRs []string,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
	roleOf func(ctx context.Context, workspaceID, userID string) (string, error),
	rl *autobhttp.WebhookRateLimiter) {
	orchRepo := orchpgrepo.New(db)
	d := &assistantMEDeps{
		orchRepo:    orchRepo,
		assistant:   assistant,
		wiSvc:       wiSvc, catSvc: catSvc, cmSvc: cmSvc, skillSvc: skillSvc,
		attachments: attachments,
		roleOf:      roleOf,
		mcpSvc:      mcpSvc, credSvc: credSvc, uiEntrySvc: uiEntrySvc, webhookSvc: webhookSvc,
		webOrigins:  webOrigins, egressCIDRs: egressCIDRs,
		// workpgrepo 不进 deps：时间线是唯一读面，绑函数注入。
		timeline: (&workTimelineSrc{DB: db}).ListTimeline,
	}

	s := server.NewMCPServer("orkome-assistant", "0.1.0")
	d.addReadTools(s)
	d.addWriteTools(s)
	d.addTaskTools(s)
	d.addConfigTools(s)
	handler := server.NewStreamableHTTPServer(s)

	// 三段 guarded 与 mountAgentMCP 同构：auth → RequireAuth → workspaceMW
	//（成员闸 + role 填充）→ 助理段专属的用户 PAT 闸 → 限流。
	patOnly := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := webx.PrincipalFrom(r.Context())
			// runtime credential PAT 挂虚拟用户、AgentID 非空——工人凭证
			// 不得走助理面（v4 §3.1 拒绝条款）。
			if p == nil || p.AgentID != "" {
				webx.WriteError(w, http.StatusForbidden, apierr.Forbidden,
					"assistant endpoint requires a user PAT", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	// 限流（v4 §3.1）：WebhookRateLimiter 的 Allow 是固定 IP 分层 key，
	// 不支持自定义 key——外面按 PAT 维度包一层粗闸（memory store 复用其
	// 窗口语义；轮询防护目的已达）。
	limited := func(next http.Handler) http.Handler {
		if rl == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, retry := rl.Allow(r.Context(), r)
			if !ok {
				autobhttp.WriteRateLimited(w, retry)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	guarded := auth(webx.RequireAuth(d.workspaceMW(memberCheck)(patOnly(limited(handler)))))
	api.Handle("POST /api/mcp/w/{workspaceId}/me", guarded)
	api.Handle("GET /api/mcp/w/{workspaceId}/me", guarded)

	// 接入 Skill 文档（2026-10-15 裁决：接入即一份 skill 文档——用户把
	// 文档链接发给任意 AI 客户端，AI 读完自动注册 MCP 并学会全部工具）。
	// **公开只读**（2026-10-16 裁决）：文档里没有秘密——令牌永远是占位符，
	// 工作区 UUID 不是凭证（MCP 端点每次调用自校验 PAT+成员）。无需登录
	// 即可预览与分享；真正的大门在 MCP 端点本身。
	skillDoc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := requestBaseURLMe(r)
		doc := buildAssistantSkillDoc(base, r.PathValue("workspaceId"), s.ListTools())
		// Accept 协商（2026-10-15 修复）：浏览器导航带 text/html——原始
		// markdown 浏览器不渲染会白屏，给 HTML 预览页；AI 客户端默认
		// Accept: */* 拿原始 markdown，机器语义面不变。
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			w.Header().Set("Content-Security-Policy",
				"default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(skillDocHTMLPage(doc)))
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(doc))
	})
	api.Handle("GET /api/mcp/w/{workspaceId}/me/skill.md", skillDoc)

	// 全站统一接入文档（2026-10-16 裁决）：固定链接、与工作区无关——
	// 端点用 {workspaceId} 占位，文档教 AI 用用户令牌自查目标工作区
	// （GET /api/workspaces 对 PAT 可用）。README/官网/任何地方引这一条即可。
	// 与工作区版共用同一生成器与预览页（ws 传空 = universal 模式）。
	universalDoc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := requestBaseURLMe(r)
		doc := buildAssistantSkillDoc(base, "", s.ListTools())
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			w.Header().Set("Content-Security-Policy",
				"default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(skillDocHTMLPage(doc)))
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(doc))
	})
	api.Handle("GET /api/mcp/skill.md", universalDoc)
}

// requestBaseURLMe 还原到达本服务的公网 origin：优先反代头
//（X-Forwarded-Proto），否则按 TLS 判断，Host 取请求 Host（dev 经 vite
// 代理时即 localhost:5173）。用于文档内嵌端点 URL。
func requestBaseURLMe(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if xf := r.Header.Get("X-Forwarded-Proto"); xf == "https" || xf == "http" {
		scheme = xf
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

func (d *assistantMEDeps) workspaceMW(memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := webx.PrincipalFrom(r.Context())
			ws := r.PathValue("workspaceId")
			ok, err := memberCheck(r.Context(), ws, p.UserID)
			if err != nil || !ok {
				webx.WriteError(w, http.StatusForbidden, apierr.Forbidden, "workspace membership required", nil)
				return
			}
			p.WorkspaceID = ws
			if d.roleOf != nil {
				if role, rerr := d.roleOf(r.Context(), ws, p.UserID); rerr == nil {
					p.Role = role
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// principalOf 助理面的身份解析：me = PAT 归属用户本人（v4 §3.2 读工具表）。
func principalOf(ctx context.Context) (ws, userID string, err error) {
	p := webx.PrincipalFrom(ctx)
	if p == nil || p.UserID == "" || p.WorkspaceID == "" {
		return "", "", apierr.New(apierr.Forbidden, "user PAT required (assistant MCP endpoint)")
	}
	return p.WorkspaceID, p.UserID, nil
}

// workTimelineSrc ListTimeline 的方法值载体（workpgrepo.Repo 零值即用——
// 只绑 DB，无 Seal 依赖）。
type workTimelineSrc struct{ DB *pg.DB }

func (w *workTimelineSrc) ListTimeline(ctx context.Context, wiID string, limit int, before string) ([]workapp.TimelineEntry, error) {
	return (&workpgrepo.Repo{DB: w.DB}).ListTimeline(ctx, wiID, limit, before)
}

func jsonTextMe(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(b)), nil
}

func errResultMe(err error) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(err.Error()), nil
}

// ---------- 读（5）----------

func (d *assistantMEDeps) addReadTools(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("list_work_items",
		mcp.WithDescription("list work items (assignee='me' for your own, or filter by status/project; start here for conversational access)"),
		mcp.WithString("assignee", mcp.Description("'me' to list items assigned to you; omit for all")),
		mcp.WithString("status", mcp.Description("status key or category (todo/in_progress/done); see list_statuses")),
		mcp.WithString("project", mcp.Description("project id")),
		mcp.WithNumber("limit", mcp.Description("max items (default 20, max 200)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, userID, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		q := workapp.ListQuery{WorkspaceID: ws, Limit: int(req.GetFloat("limit", 20))}
		if req.GetString("assignee", "") == "me" {
			q.AssigneeID = userID
		}
		if st := req.GetString("status", ""); st != "" {
			q.StatusKeys = []string{st}
		}
		if p := req.GetString("project", ""); p != "" {
			q.ProjectID = p
		}
		items, _, err := d.wiSvc.List(ctx, q)
		if err != nil {
			return errResultMe(err)
		}
		type row struct {
			ID          string `json:"id"`
			Number      int64  `json:"number"`
			Title       string `json:"title"`
			Status      string `json:"status"`
			Assignee    string `json:"assignee_id,omitempty"`
			Outcome     string `json:"outcome,omitempty"`
			HasAIClaims bool   `json:"-"`
		}
		out := make([]row, 0, len(items))
		for _, wi := range items {
			o := ""
			if wi.Outcome != nil {
				o = wi.Outcome.Summary
			}
			out = append(out, row{ID: wi.ID, Number: wi.Number, Title: wi.Title,
				Status: wi.StatusKey, Assignee: wi.AssigneeID, Outcome: o})
		}
		return jsonTextMe(map[string]any{"work_items": out})
	})

	s.AddTool(mcp.NewTool("get_work_item",
		mcp.WithDescription("work item brief: entity + recent timeline + task conclusions + active AI task_id (single-item recovery data plane; call before claiming)"),
		mcp.WithString("work_item_id", mcp.Required(), mcp.Description("work item ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		wi, err := d.wiSvc.Get(ctx, ws, req.GetString("work_item_id", ""))
		if err != nil {
			return errResultMe(err)
		}
		tl, err := d.orchRepoTimeline(ctx, wi.ID)
		if err != nil {
			return errResultMe(err)
		}
		tasks, err := d.orchRepo.ListTasksByWorkItem(ctx, wi.ID, true)
		if err != nil {
			return errResultMe(err)
		}
		if len(tasks) > 5 {
			tasks = tasks[:5]
		}
		type taskRow struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Attempt int    `json:"attempt"`
			Summary string `json:"summary"`
		}
		tr := make([]taskRow, 0, len(tasks))
		var activeTaskID string
		for _, t := range tasks {
			tr = append(tr, taskRow{ID: t.ID, Status: t.Status, Attempt: t.Attempt, Summary: t.Summary})
			if t.Status == "running" && activeTaskID == "" {
				activeTaskID = t.ID
			}
		}
		outcome := map[string]any(nil)
		if wi.Outcome != nil {
			outcome = map[string]any{"summary": wi.Outcome.Summary, "task_id": wi.Outcome.TaskID}
		}
		// 附件清单（只读）：REST /api/attachments/{id}/content 与本端点同凭证
		// （PAT + X-Workspace-Id 头），外部客户端可直接按 URL 拉取。
		atts, err := d.attachments.ListByHost(ctx, ws, "work_item", wi.ID)
		if err != nil {
			return errResultMe(err)
		}
		type attRow struct {
			ID          string `json:"id"`
			Filename    string `json:"filename"`
			ContentType string `json:"content_type"`
			SizeBytes   int64  `json:"size_bytes"`
			ContentURL  string `json:"content_url"`
		}
		attOut := make([]attRow, 0, len(atts))
		for _, a := range atts {
			attOut = append(attOut, attRow{
				ID: a.ID, Filename: a.Filename, ContentType: a.ContentType,
				SizeBytes: a.SizeBytes, ContentURL: "/api/attachments/" + a.ID + "/content",
			})
		}
		return jsonTextMe(map[string]any{
			"work_item": map[string]any{
				"id": wi.ID, "number": wi.Number, "title": wi.Title,
				"description": wi.Description, "status": wi.StatusKey,
				"priority": wi.Priority, "assignee_id": wi.AssigneeID,
				"revision": wi.Revision, "outcome": outcome,
			},
			"active_task_id": activeTaskID,
			"recent_tasks":   tr,
			"timeline":       tl,
			"attachments":    attOut,
		})
	})

	s.AddTool(mcp.NewTool("list_projects",
		mcp.WithDescription("list projects in the workspace (value discovery for create/update)"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		items, err := d.catSvc.ListProjects(ctx, ws)
		if err != nil {
			return errResultMe(err)
		}
		type row struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		out := make([]row, 0, len(items))
		for _, p := range items {
			out = append(out, row{ID: p.ID, Name: p.Name})
		}
		return jsonTextMe(map[string]any{"projects": out})
	})

	s.AddTool(mcp.NewTool("list_labels",
		mcp.WithDescription("list labels in the workspace (value domain for update_work_item labels)"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		items, err := d.catSvc.ListLabels(ctx, ws)
		if err != nil {
			return errResultMe(err)
		}
		type row struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		out := make([]row, 0, len(items))
		for _, l := range items {
			out = append(out, row{ID: l.ID, Name: l.Name})
		}
		return jsonTextMe(map[string]any{"labels": out})
	})

	s.AddTool(mcp.NewTool("list_statuses",
		mcp.WithDescription("list status definitions (status_key is per-workspace configurable — never guess; key/category/position included)"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		defs, err := d.catSvc.ListStatuses(ctx, ws)
		if err != nil {
			return errResultMe(err)
		}
		type row struct {
			Key      string `json:"key"`
			Name     string `json:"name"`
			Category string `json:"category"`
			Position int    `json:"position"`
		}
		out := make([]row, 0, len(defs))
		for _, d := range defs {
			out = append(out, row{Key: d.Key, Name: d.Name, Category: d.Category, Position: d.Position})
		}
		return jsonTextMe(map[string]any{"statuses": out})
	})

	// —— 技能/资料库读取（skill 即知识库裁决）：workspace 级，与 agent 绑定无关。
	s.AddTool(mcp.NewTool("list_skills",
		mcp.WithDescription("list workspace skills & docs (markdown knowledge base: SOPs, manuals, reference docs; read_skill(name) for content)"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		skills, err := d.skillSvc.List(ctx, ws)
		if err != nil {
			return errResultMe(err)
		}
		type row struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		out := make([]row, 0, len(skills))
		for _, sk := range skills {
			out = append(out, row{Name: sk.Name, Description: sk.Description})
		}
		return jsonTextMe(map[string]any{"skills": out})
	})

	s.AddTool(mcp.NewTool("read_skill",
		mcp.WithDescription("read the SKILL.md main file of a workspace skill/doc by name (markdown knowledge base lookup)"),
		mcp.WithString("name", mcp.Required(), mcp.Description("skill name from list_skills")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		name := strings.TrimSpace(req.GetString("name", ""))
		if name == "" {
			return errResultMe(apierr.NewValidation("name required"))
		}
		content, err := d.skillSvc.MainByName(ctx, ws, name)
		if err != nil {
			return errResultMe(err)
		}
		return mcp.NewToolResultText(content), nil
	})
}

// orchRepoTimeline 时间线读取（work 的 timeline 面经注入函数，避免整只
// workpgrepo.Repo 进本 deps）。
func (d *assistantMEDeps) orchRepoTimeline(ctx context.Context, wiID string) ([]workapp.TimelineEntry, error) {
	if d.timeline == nil {
		return nil, nil
	}
	return d.timeline(ctx, wiID, 20, "")
}

// ---------- 写（3）----------

func (d *assistantMEDeps) addWriteTools(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("create_work_item",
		mcp.WithDescription("create a work item (author = you, the PAT owner); assignee can be 'me' or an agent (by id)"),
		mcp.WithString("title", mcp.Required(), mcp.Description("title (1-500 chars)")),
		mcp.WithString("description", mcp.Description("description")),
		mcp.WithString("project", mcp.Description("project id (see list_projects)")),
		mcp.WithString("assignee", mcp.Description("'me' to assign to yourself; omit for unassigned")),
		mcp.WithString("assignee_agent_id", mcp.Description("agent id to assign (validated against this workspace; mutually exclusive with assignee='me')")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, userID, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		in := workapp.NewWorkItem{
			WorkspaceID: ws,
			Title:       req.GetString("title", ""),
			Description: req.GetString("description", ""),
			ProjectID:   req.GetString("project", ""),
			CreatorType: "user", CreatorID: userID,
		}
		assigneeMe := req.GetString("assignee", "") == "me"
		assigneeAgent := req.GetString("assignee_agent_id", "")
		switch {
		case assigneeMe && assigneeAgent != "":
			return errResultMe(apierr.New(apierr.Validation, "assignee='me' and assignee_agent_id are mutually exclusive"))
		case assigneeMe:
			in.AssigneeType, in.AssigneeID = "user", userID
		case assigneeAgent != "":
			a, ok, err := d.orchRepo.GetAgent(ctx, ws, assigneeAgent)
			if err != nil {
				return errResultMe(err)
			}
			if !ok || a.Archived {
				return errResultMe(apierr.New(apierr.Validation, "assignee agent not found in this workspace or archived"))
			}
			in.AssigneeType, in.AssigneeID = "agent", assigneeAgent
		}
		wi, err := d.wiSvc.Create(ctx, in, userID)
		if err != nil {
			return errResultMe(err)
		}
		return jsonTextMe(map[string]any{
			"work_item": map[string]any{"id": wi.ID, "number": wi.Number, "title": wi.Title, "status": wi.StatusKey},
		})
	})

	s.AddTool(mcp.NewTool("update_work_item",
		mcp.WithDescription("update a work item (title/description/priority/status/labels). Internally reads current revision first then writes — concurrent edits surface as an error to retry"),
		mcp.WithString("work_item_id", mcp.Required(), mcp.Description("work item ID")),
		mcp.WithString("title", mcp.Description("new title")),
		mcp.WithString("description", mcp.Description("new description")),
		mcp.WithString("priority", mcp.Description("new priority (integer)")),
		mcp.WithString("status", mcp.Description("new status key (see list_statuses)")),
		mcp.WithString("labels", mcp.Description("comma-separated label ids to SET (full replace; see list_labels)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, userID, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		id := req.GetString("work_item_id", "")
		// 内置"先读拿 expected_revision 再写"（v4 §3.2：乐观锁在工具层包掉，
		// AI 不需要理解 revision 概念；并发编辑失败会以错误文本回给 AI 重试）。
		cur, err := d.wiSvc.Get(ctx, ws, id)
		if err != nil {
			return errResultMe(err)
		}
		var patch workapp.WorkItemPatch
		if v := req.GetString("title", ""); v != "" {
			patch.Title = &v
		}
		if v := req.GetString("description", ""); v != "" {
			patch.Description = &v
		}
		if v := req.GetString("status", ""); v != "" {
			patch.StatusKey = &v
		}
		if raw := req.GetString("priority", ""); raw != "" {
			var pr int
			if _, err := fmt.Sscanf(raw, "%d", &pr); err != nil {
				return errResultMe(apierr.NewValidation("priority must be an integer"))
			}
			patch.Priority = &pr
		}
		wi, err := d.wiSvc.Update(ctx, ws, id, patch, cur.Revision, userID)
		if err != nil {
			return errResultMe(err)
		}
		// labels 全量替换（WorkItemPatch 无 labels 字段，走显式 Attach/Detach
		// 面：先清后挂；label 校验由 AttachLabel 的存在性检查承担）。
		if raw := req.GetString("labels", ""); raw != "" {
			var ids []string
			for _, part := range strings.Split(raw, ",") {
				if p := strings.TrimSpace(part); p != "" {
					ids = append(ids, p)
				}
			}
			for _, l := range d.currentLabels(ctx, ws, id) {
				_, _ = d.wiSvc.DetachLabel(ctx, ws, id, l.ID)
			}
			for _, lid := range ids {
				if _, err := d.wiSvc.AttachLabel(ctx, ws, id, lid); err != nil {
					return errResultMe(err)
				}
			}
		}
		return jsonTextMe(map[string]any{
			"work_item": map[string]any{"id": wi.ID, "title": wi.Title, "status": wi.StatusKey, "revision": wi.Revision},
		})
	})

	s.AddTool(mcp.NewTool("add_comment",
		mcp.WithDescription("add a comment as yourself; agent auto-dispatch is suppressed (your AI comments never wake agents — ask the user to comment if re-triggering is intended)"),
		mcp.WithString("work_item_id", mcp.Required(), mcp.Description("work item ID")),
		mcp.WithString("content", mcp.Required(), mcp.Description("comment content")),
		mcp.WithString("parent_id", mcp.Description("parent comment id for replies")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, userID, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		c, _, err := d.cmSvc.Create(ctx, workapp.NewComment{
			WorkItemID: req.GetString("work_item_id", ""), WorkspaceID: ws,
			AuthorType: "user", AuthorID: userID,
			Content:  req.GetString("content", ""),
			ParentID: req.GetString("parent_id", ""),
			// skipTrigger（v4 §3.2）：AI 代用户评论不得意外触发 agent 派发；
			// 用户再求助的"评论重置"走 CommentCreated 事件谓词，与此互不干扰。
			SkipTrigger: true,
		})
		if err != nil {
			return errResultMe(err)
		}
		return jsonTextMe(map[string]any{"comment_id": c.ID, "trigger_skipped": true})
	})
}

func (d *assistantMEDeps) currentLabels(ctx context.Context, ws, wiID string) []workapp.Label {
	ls, err := d.wiSvc.Labels(ctx, ws, wiID)
	if err != nil {
		return nil
	}
	return ls
}

// ---------- 任务协议（5）+ 恢复（1）----------

func (d *assistantMEDeps) addTaskTools(s *server.MCPServer) {
	s.AddTool(mcp.NewTool("list_my_claims",
		mcp.WithDescription("work items assigned to me that are ready to claim (excludes ones my AI already has an active task on; poll this every >=30s to pick up new work)"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, userID, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		// 跨域编排在本层：先解析 proxy 行 id，再调 work 域 List 带排除谓词
		//（域层零互依——orchestration 不 import work，work 不 import
		// orchestration）。
		agentID, _, err := d.orchRepo.GetAssistantProxyAgent(ctx, ws, userID)
		if err != nil {
			return errResultMe(err)
		}
		// 待处理集 = 非 done/closed 类(per-workspace 可配目录,ActiveStatusKeys
		// 解析;complete 已把工单推进 done,stalled 交还回退 todo——都不漏)
		defs, err := d.catSvc.ListStatuses(ctx, ws)
		if err != nil {
			return errResultMe(err)
		}
		q := workapp.ListQuery{WorkspaceID: ws, AssigneeID: userID, Limit: 50,
			StatusKeys: workapp.ActiveStatusKeysFor(defs)}
		if agentID != "" {
			q.ExcludeAgentActiveTasks = agentID
			// fail/stalled 后 24h 暂列不出(v4 §4.2 再求助通路):用户在该
			// 工单评论一句即重置排除;口述工单 ID 直接 claim 兜底。
			q.ExcludeAgentRecentlyClaimedHours = 24
		}
		items, _, err := d.wiSvc.List(ctx, q)
		if err != nil {
			return errResultMe(err)
		}
		type row struct {
			ID     string `json:"id"`
			Number int64  `json:"number"`
			Title  string `json:"title"`
			Status string `json:"status"`
		}
		out := make([]row, 0, len(items))
		for _, wi := range items {
			out = append(out, row{ID: wi.ID, Number: wi.Number, Title: wi.Title, Status: wi.StatusKey})
		}
		return jsonTextMe(map[string]any{"work_items": out})
	})

	s.AddTool(mcp.NewTool("claim_work_item",
		mcp.WithDescription("claim a work item assigned to you (single transaction with row locking; creates a running task owned by your proxy agent identity; 409 if already claimed)"),
		mcp.WithString("work_item_id", mcp.Required(), mcp.Description("work item ID")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, userID, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		t, err := d.assistant.ClaimWorkItem(ctx, ws, userID, req.GetString("work_item_id", ""))
		if err != nil {
			return errResultMe(err)
		}
		return jsonTextMe(map[string]any{"task_id": t.ID, "status": t.Status})
	})

	s.AddTool(mcp.NewTool("report_progress",
		mcp.WithDescription("report execution progress (pure timeline message; no liveness semantics — report freely as you work)"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("task ID (from claim_work_item)")),
		mcp.WithString("content", mcp.Required(), mcp.Description("progress content")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, userID, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		seq, err := d.assistant.ReportProgress(ctx, ws, userID,
			req.GetString("task_id", ""), req.GetString("content", ""))
		if err != nil {
			return errResultMe(err)
		}
		return jsonTextMe(map[string]any{"seq": seq})
	})

	s.AddTool(mcp.NewTool("complete_work_item",
		mcp.WithDescription("complete the claimed task: result lands as the work item outcome (report tab) and the work item advances to the done status — unless it is already terminal or a concurrent edit wins"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("task ID")),
		mcp.WithString("result", mcp.Required(), mcp.Description("conclusion (what was done and the outcome)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, userID, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		if err := d.assistant.CompleteWorkItem(ctx, ws, userID,
			req.GetString("task_id", ""), req.GetString("result", "")); err != nil {
			return errResultMe(err)
		}
		return jsonTextMe(map[string]any{"result": "completed"})
	})

	s.AddTool(mcp.NewTool("fail_work_item",
		mcp.WithDescription("fail the claimed task (terminal, no retry); the work item stays with you — comment on it or say 'retry' to re-claim"),
		mcp.WithString("task_id", mcp.Required(), mcp.Description("task ID")),
		mcp.WithString("reason", mcp.Required(), mcp.Description("failure reason (short)")),
		mcp.WithString("detail", mcp.Description("failure detail")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, userID, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		if err := d.assistant.FailWorkItem(ctx, ws, userID,
			req.GetString("task_id", ""), req.GetString("reason", ""), req.GetString("detail", "")); err != nil {
			return errResultMe(err)
		}
		return jsonTextMe(map[string]any{"result": "failed"})
	})

	s.AddTool(mcp.NewTool("get_my_running_tasks",
		mcp.WithDescription("my AI's running tasks (id/work item/summary/start time) — call this first after (re)starting to recover activity context"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, userID, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		tasks, err := d.assistant.ListRunningTasks(ctx, ws, userID)
		if err != nil {
			return errResultMe(err)
		}
		type row struct {
			TaskID     string `json:"task_id"`
			WorkItemID string `json:"work_item_id,omitempty"`
			Title      string `json:"title,omitempty"`
			Status     string `json:"status"`
			Summary    string `json:"summary,omitempty"`
		}
		out := make([]row, 0, len(tasks))
		for _, t := range tasks {
			title := ""
			if t.WorkItemID != "" {
				if wi, err := d.wiSvc.Get(ctx, ws, t.WorkItemID); err == nil {
					title = wi.Title
				}
			}
			out = append(out, row{TaskID: t.ID, WorkItemID: t.WorkItemID, Title: title,
				Status: t.Status, Summary: t.Summary})
		}
		return jsonTextMe(map[string]any{"tasks": out, "count": len(out)})
	})
}

// ---------- 配置（集成页 parity：读 3 + 写 4）----------
//
//「AI 即安装器」第一期（2026-10-15 裁决）：集成页手动能做的配置，助理面全开。
// 读工具 member 可用；写工具 owner/admin 门槛（requireAdminMe，对齐 REST 侧
// mcp/webhooks/ui-entries 的 admin 闸）。与 REST 共用同一批 service 实例——
// 校验（egress 闸/事件目录/URL 模板/slot 冻结目录）全部复用域内实现。
// 包级 3 工具（list/register/uninstall_pack）随能力包退役拆除（2026-10-16）：
// 安装向导/挂载自集成页终态起退役，遗留台账归 REST /api/packs 与包详情页。

// requireAdminMe 配置写工具的 admin 闸：PAT 归属用户必须在当前工作区为 owner/admin。
func (d *assistantMEDeps) requireAdminMe(ctx context.Context) (ws, userID string, err error) {
	ws, userID, err = principalOf(ctx)
	if err != nil {
		return "", "", err
	}
	if d.roleOf != nil {
		role, rerr := d.roleOf(ctx, ws, userID)
		if rerr == nil && role != "owner" && role != "admin" {
			return "", "", apierr.New(apierr.Forbidden,
				"workspace owner or admin role required for configuration writes")
		}
	}
	return ws, userID, nil
}

// slugifyMe 生成手动页面入口的内部键（custom-<slug>；中文等非 ASCII 折叠掉后
// 空则时间戳回退）——与前端 UiEntriesSection 的命名规则同构。
func slugifyMe(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case !prevDash:
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = out[:60]
	}
	if out == "" {
		out = fmt.Sprintf("entry-%d", time.Now().Unix())
	}
	return "custom-" + out
}

// validAbsHTTPMe 页面入口 URL 模板的绝对 http(s) 校验（与 orchestration
// validateURLTemplate 同口径；防 javascript:/data: 模板进 iframe src）。
func validAbsHTTPMe(tpl string) bool {
	u, err := url.Parse(tpl)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (d *assistantMEDeps) addConfigTools(s *server.MCPServer) {
	// ---- 读 3 ----

	s.AddTool(mcp.NewTool("list_mcp_servers",
		mcp.WithDescription("list workspace MCP servers (the tool wiring board every agent uses; see also list_page_entries / list_webhook_subscriptions)"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		items, err := d.mcpSvc.List(ctx, ws)
		if err != nil {
			return errResultMe(err)
		}
		type row struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			Transport      string `json:"transport"`
			URL            string `json:"url"`
			HasCredentials bool   `json:"has_credentials"`
		}
		out := make([]row, 0, len(items))
		for _, m := range items {
			out = append(out, row{ID: m.ID, Name: m.Name, Transport: m.Transport,
				URL: m.URL, HasCredentials: m.HasCreds})
		}
		return jsonTextMe(map[string]any{"mcp_servers": out, "count": len(out)})
	})

	s.AddTool(mcp.NewTool("list_webhook_subscriptions",
		mcp.WithDescription("list workspace webhook subscriptions (platform events pushed to vendor receivers)"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		subs, err := d.webhookSvc.ListSubscriptions(ctx, ws)
		if err != nil {
			return errResultMe(err)
		}
		type row struct {
			ID          string   `json:"id"`
			URL         string   `json:"url"`
			EventTypes  []string `json:"event_types"`
			IsActive    bool     `json:"is_active"`
			Description string   `json:"description,omitempty"`
		}
		out := make([]row, 0, len(subs))
		for _, sub := range subs {
			out = append(out, row{ID: sub.ID, URL: sub.URL, EventTypes: sub.EventTypes,
				IsActive: sub.IsActive, Description: sub.Description})
		}
		return jsonTextMe(map[string]any{"subscriptions": out, "count": len(out)})
	})

	s.AddTool(mcp.NewTool("list_page_entries",
		mcp.WithDescription("list page entries (sidebar deep-links and work-item detail tabs embedded in Orkome)"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := principalOf(ctx)
		if err != nil {
			return errResultMe(err)
		}
		entries, err := d.uiEntrySvc.List(ctx, ws)
		if err != nil {
			return errResultMe(err)
		}
		type slotRow struct {
			Slot   string `json:"slot"`
			Label  string `json:"label"`
			Height int    `json:"height"`
		}
		type row struct {
			Pack        string    `json:"pack"`
			Label       string    `json:"label"`
			URLTemplate string    `json:"url_template"`
			FromPack    bool      `json:"from_pack"`
			Slots       []slotRow `json:"slots"`
		}
		out := make([]row, 0, len(entries))
		for _, e := range entries {
			r := row{Pack: e.Pack, Label: e.Label, URLTemplate: e.URLTemplate,
				FromPack: e.PackID != "", Slots: []slotRow{}}
			for _, sl := range e.Slots {
				r.Slots = append(r.Slots, slotRow{Slot: sl.Slot, Label: sl.Label, Height: sl.Height})
			}
			out = append(out, r)
		}
		return jsonTextMe(map[string]any{"page_entries": out, "count": len(out)})
	})

	// ---- 写 4（admin）----

	s.AddTool(mcp.NewTool("create_credential",
		mcp.WithDescription("store a workspace credential (API key / token) sealed at rest; returns the credential id which create_mcp_server can reference for audit linkage"),
		mcp.WithString("provider", mcp.Required(), mcp.Description("provider label, e.g. deepseek / vendor name")),
		mcp.WithString("secret", mcp.Required(), mcp.Description("the secret value (sealed at rest; never echoed back)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, userID, err := d.requireAdminMe(ctx)
		if err != nil {
			return errResultMe(err)
		}
		provider := strings.TrimSpace(req.GetString("provider", ""))
		secret := req.GetString("secret", "")
		if provider == "" || secret == "" {
			return errResultMe(apierr.NewValidation("provider and secret required"))
		}
		cred, err := d.credSvc.Create(ctx, ws, provider, secret, userID)
		if err != nil {
			return errResultMe(err)
		}
		return jsonTextMe(map[string]any{"id": cred.ID, "provider": cred.Provider})
	})

	s.AddTool(mcp.NewTool("create_mcp_server",
		mcp.WithDescription("register a workspace MCP server so every agent can use its tools. For auth pass bearer_token (written into the sealed credentials envelope, headers.Authorization) — credential_id alone is NOT used at auth time, it is audit linkage only"),
		mcp.WithString("name", mcp.Required(), mcp.Description("display name")),
		mcp.WithString("url", mcp.Required(), mcp.Description("MCP endpoint URL, e.g. https://host/mcp")),
		mcp.WithString("transport", mcp.Description("transport: http | sse | streamable_http (default http)")),
		mcp.WithString("bearer_token", mcp.Description("optional bearer token for the MCP endpoint")),
		mcp.WithString("credential_id", mcp.Description("optional credential id (from create_credential) for audit linkage")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := d.requireAdminMe(ctx)
		if err != nil {
			return errResultMe(err)
		}
		name := strings.TrimSpace(req.GetString("name", ""))
		u := strings.TrimSpace(req.GetString("url", ""))
		if name == "" || u == "" {
			return errResultMe(apierr.NewValidation("name and url required"))
		}
		transport := strings.TrimSpace(req.GetString("transport", ""))
		if transport == "" {
			transport = "http"
		}
		creds := map[string]any{}
		hasCreds := false
		if bt := strings.TrimSpace(req.GetString("bearer_token", "")); bt != "" {
			creds["headers"] = map[string]any{"Authorization": "Bearer " + bt}
			hasCreds = true
		}
		if cid := strings.TrimSpace(req.GetString("credential_id", "")); cid != "" {
			creds["credential_id"] = cid
			hasCreds = true
		}
		var credMap map[string]any
		if hasCreds {
			credMap = creds
		}
		m, err := d.mcpSvc.Create(ctx, orchapp.WorkspaceMCP{
			WorkspaceID: ws, Name: name, Transport: transport, URL: u, Credentials: credMap,
		})
		if err != nil {
			return errResultMe(err)
		}
		return jsonTextMe(map[string]any{"id": m.ID, "name": m.Name,
			"transport": m.Transport, "url": m.URL, "has_credentials": m.HasCreds})
	})

	s.AddTool(mcp.NewTool("create_webhook_subscription",
		mcp.WithDescription("create a webhook subscription pushing platform events to a receiver URL. The signing secret is returned ONCE here — persist it, it cannot be retrieved later"),
		mcp.WithString("url", mcp.Required(), mcp.Description("receiver endpoint URL")),
		mcp.WithArray("events", mcp.Required(), mcp.Description("event types to subscribe, e.g. work_item.created"),
			mcp.WithStringItems()),
		mcp.WithString("description", mcp.Description("optional label")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := d.requireAdminMe(ctx)
		if err != nil {
			return errResultMe(err)
		}
		u := strings.TrimSpace(req.GetString("url", ""))
		events := req.GetStringSlice("events", nil)
		if u == "" || len(events) == 0 {
			return errResultMe(apierr.NewValidation("url and events required"))
		}
		sub, secret, err := d.webhookSvc.CreateSubscription(ctx, ws, u,
			req.GetString("description", ""), events)
		if err != nil {
			return errResultMe(err)
		}
		return jsonTextMe(map[string]any{
			"id": sub.ID, "url": sub.URL, "event_types": sub.EventTypes,
			"secret": secret,
			"note":   "signing secret shown only once — hand it to the receiver and store it; it cannot be retrieved later",
		})
	})

	s.AddTool(mcp.NewTool("put_page_entry",
		mcp.WithDescription("create/update a page entry embedded in Orkome: a sidebar deep-link, and optionally a work-item detail tab (work_item_tab=true). The URL template may use {uiToken} (minted per open), {workspaceId} and (tabs only) {workItemId}"),
		mcp.WithString("label", mcp.Required(), mcp.Description("entry display name")),
		mcp.WithString("url_template", mcp.Required(), mcp.Description("absolute http(s) URL template, e.g. https://vendor.example.com/console?ws={workspaceId}&t={uiToken}")),
		mcp.WithString("key", mcp.Description("optional stable key (default custom-<slug of label>); re-using a key updates that entry")),
		mcp.WithBoolean("work_item_tab", mcp.Description("also show as a work-item detail tab (default false)")),
		mcp.WithNumber("tab_height", mcp.Description("tab iframe height in px, 1-4096 (default 480)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ws, _, err := d.requireAdminMe(ctx)
		if err != nil {
			return errResultMe(err)
		}
		label := strings.TrimSpace(req.GetString("label", ""))
		tpl := strings.TrimSpace(req.GetString("url_template", ""))
		if label == "" || tpl == "" {
			return errResultMe(apierr.NewValidation("label and url_template required"))
		}
		if !validAbsHTTPMe(tpl) {
			return errResultMe(apierr.NewValidation("url_template must be an absolute http(s) URL"))
		}
		key := strings.TrimSpace(req.GetString("key", ""))
		if key == "" {
			key = slugifyMe(label)
		}
			var slots []orchapp.PackUISlot
		cspWarn := ""
		if req.GetBool("work_item_tab", false) {
			h := int(req.GetFloat("tab_height", 480))
			if h <= 0 || h > 4096 {
				h = 480
			}
			// CSP frame-ancestors 放行探测（CLI 时代 slot 守卫搬服务端）：
			// 不合规只警告不拒绝——入口（侧栏深链）仍落库，页签可能被浏览器
			// 拦白屏由用户裁决（保守面：探测网络抖动不误杀入口）。
			if okProbe, perr := d.probeSlotCSP(ctx, tpl); perr != nil {
				cspWarn = "frame-ancestors probe failed: " + perr.Error()
			} else if !okProbe {
				cspWarn = "receiver does not allow the Orkome origin in CSP frame-ancestors; the tab may be blocked by the browser"
			}
			slots = append(slots, orchapp.PackUISlot{
				Slot: "workitem.detail.tab", Label: label, URLTemplate: tpl, Height: h,
			})
		}
		if err := d.uiEntrySvc.UpsertWithSlots(ctx, ws, key, "", label, tpl, slots); err != nil {
			return errResultMe(err)
		}
		out := map[string]any{"result": "upserted", "key": key,
			"work_item_tab": len(slots) > 0}
		if cspWarn != "" {
			out["warning"] = cspWarn
		}
		return jsonTextMe(out)
	})
}

// probeSlotCSP 页面入口的 CSP frame-ancestors 放行探测（CLI probeFrameAncestors
// 搬服务端）：模板变量换哑值 GET，响应头 frame-ancestors 含 * 或任一配置的
// web origin 即放行。egress 治理同 webhook 出口（私网目标需显式允许）。
func (d *assistantMEDeps) probeSlotCSP(ctx context.Context, urlTemplate string) (bool, error) {
	u := strings.NewReplacer(
		"{workspaceId}", "probe", "{uiToken}", "probe", "{workItemId}", "probe",
	).Replace(urlTemplate)
	hc, err := egressx.NewHTTPClient(egressx.Opts{Timeout: 10 * time.Second, AllowCIDRs: d.egressCIDRs})
	if err != nil {
		return false, err
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		return false, nil
	}
	for _, directive := range strings.Split(csp, ";") {
		dir := strings.TrimSpace(directive)
		if !strings.HasPrefix(dir, "frame-ancestors") {
			continue
		}
		for _, src := range strings.Fields(strings.TrimSpace(strings.TrimPrefix(dir, "frame-ancestors"))) {
			if src == "*" {
				return true, nil
			}
			for _, origin := range d.webOrigins {
				if src == origin {
					return true, nil
				}
			}
		}
		return false, nil
	}
	return false, nil
}

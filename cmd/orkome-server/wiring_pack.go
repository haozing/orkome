package main

// wiringPack 装配 pack 域（pack 一等公民化 P1，W1-2；能力包平台化增补安装
// 编排与双 binder）：pack 表自身的 repo + 跨域读端口 + 安装写端口的组合根
// 接线。各域 pgrepo 提供本域类型的查询方法（repo_packquery*.go——arch iron
// law rule 3：adapters 不跨域 import），这里的适配器做形状转换并接上端口
// ——模式同 wiring_skills 的 SkillLabelEndpoints 适配器。
//
//	main.go:  packhttp.Mount(api, wiringPack(db, cfg, log, webhookSvc, idMemberCheck(db), roleOf(db)), authGuardMw)

import (
	"context"
	"log/slog"

	"github.com/haozing/ploykit/platform/pg"
	webhookapp "github.com/haozing/ploykit/webhooks/app"

	orchpgrepo "orkome/internal/orchestration/adapters/pgrepo"
	orchapp "orkome/internal/orchestration/app"
	packhttp "orkome/internal/pack/adapters/http"
	packpgrepo "orkome/internal/pack/adapters/pgrepo"
	packapp "orkome/internal/pack/app"
	skillpgrepo "orkome/internal/skills/adapters/pgrepo"
	skillapp "orkome/internal/skills/app"
)

func wiringPack(
	db *pg.DB,
	cfg *Config,
	log *slog.Logger,
	hooks *webhookapp.WebhookService,
	memberCheck func(ctx context.Context, workspaceID, userID string) (bool, error),
	roleLookup func(ctx context.Context, workspaceID, userID string) (string, error),
) packhttp.Deps {
	repo := packpgrepo.New(db)
	orch := orchpgrepo.New(db)
	orch.Seal = credentialSealer(log, cfg) // 读面统一装配（ListWorkspaceMCPByPack 只判信封有无，不解封）
	skills := skillpgrepo.New(db)

	// 安装编排的跨域写端口（orchestration 域服务——本文件内共享同一 repo，
	// 密封器与 egress 闸口径一致）。
	credSvc := orchapp.NewCredentialService(orch)
	mcpSvc := orchapp.NewMCPServerService(orch)
	mcpSvc.Egress = egressGuardOf(cfg)
	uiSvc := orchapp.NewPackUIEntryService(orch)

	packSvc := packapp.NewPackService(repo, packapp.Ports{
		MCP:         packMCPServerReader{orch},
		Tools:       packToolSchemaReader{orch},
		ToolDetails: packToolSchemaDetailReader{orch},
		Agents:      packAgentReader{orch},
		Skills:      packSkillReader{skills},
		UIEntry:     packUIEntryReader{orch},
		Subs:        repo, // pack_subscription 连接表归 pack 域自身，repo 直接实现端口
	}).WithInstallPorts(packapp.InstallPorts{
		Credentials: packCredentialsPort{credSvc},
		MCP:         packMCPPort{mcpSvc},
		Webhooks:    packWebhooksPort{hooks},
		UIEntry:     packUIEntryPort{uiSvc},
	})

	return packhttp.Deps{
		Packs:       packSvc,
		MemberCheck: memberCheck,
		RoleOf:      roleLookup,
		AgentBinder: packAgentBinder{orchSvcBinder{repo: orch}},
		SkillBinder: packSkillBinder{svc: skillapp.NewSkillService(skills)},
		// 端口装配完成 → 对应端点活化（否则 503 短路）
		InstallWired:     hooks != nil,
		ToolDetailsWired: true,
	}
}

// ---- 端口适配器：域间形状解耦（各域返回本域类型，这里转成 pack 域引用） ----

type packMCPServerReader struct{ repo *orchpgrepo.Repo }

func (a packMCPServerReader) ListByPack(ctx context.Context, ws, packID string) ([]packapp.MCPServerRef, error) {
	list, err := a.repo.ListWorkspaceMCPByPack(ctx, ws, packID)
	if err != nil {
		return nil, err
	}
	out := make([]packapp.MCPServerRef, 0, len(list))
	for _, m := range list {
		out = append(out, packapp.MCPServerRef{Name: m.Name, Transport: m.Transport, URL: m.URL, HasCreds: m.HasCreds})
	}
	return out, nil
}

type packToolSchemaReader struct{ repo *orchpgrepo.Repo }

func (a packToolSchemaReader) ListByPack(ctx context.Context, ws, packID string) ([]packapp.ToolSchemaRef, error) {
	list, err := a.repo.ListPackToolSchemasByPack(ctx, ws, packID)
	if err != nil {
		return nil, err
	}
	out := make([]packapp.ToolSchemaRef, 0, len(list))
	for _, it := range list {
		out = append(out, packapp.ToolSchemaRef{Server: it.Server, ToolName: it.ToolName, Source: it.Source, InstalledAt: it.InstalledAt})
	}
	return out, nil
}

type packToolSchemaDetailReader struct{ repo *orchpgrepo.Repo }

func (a packToolSchemaDetailReader) ListDetailedByPack(ctx context.Context, ws, packID string) ([]packapp.ToolSchemaDetail, error) {
	list, err := a.repo.ListPackToolSchemasDetailed(ctx, ws, packID)
	if err != nil {
		return nil, err
	}
	out := make([]packapp.ToolSchemaDetail, 0, len(list))
	for _, it := range list {
		out = append(out, packapp.ToolSchemaDetail{Server: it.Server, ToolName: it.ToolName, Source: it.Source, OutputSchemaJSON: it.OutputSchemaJSON})
	}
	return out, nil
}

type packAgentReader struct{ repo *orchpgrepo.Repo }

func (a packAgentReader) ListByPack(ctx context.Context, ws, packID string) ([]packapp.AgentRef, error) {
	list, err := a.repo.ListAgentsByPack(ctx, ws, packID)
	if err != nil {
		return nil, err
	}
	out := make([]packapp.AgentRef, 0, len(list))
	for _, ag := range list {
		out = append(out, packapp.AgentRef{ID: ag.ID, Name: ag.Name, Archived: ag.Archived})
	}
	return out, nil
}

type packSkillReader struct{ repo *skillpgrepo.Repo }

func (a packSkillReader) ListByPack(ctx context.Context, ws, packID string) ([]packapp.SkillRef, error) {
	// 双源（装机 origin 键 ∪ 挂载 pack_id 键）——平台化后两类都要出现在
	// 详情页资产区块（迁移 1045）。
	list, err := a.repo.ListByPack(ctx, ws, packID)
	if err != nil {
		return nil, err
	}
	out := make([]packapp.SkillRef, 0, len(list))
	for _, s := range list {
		out = append(out, packapp.SkillRef{ID: s.ID, Name: s.Name})
	}
	return out, nil
}

type packUIEntryReader struct{ repo *orchpgrepo.Repo }

func (a packUIEntryReader) GetByPack(ctx context.Context, ws, packID string) (*packapp.UIEntryInfo, error) {
	e, err := a.repo.GetPackUIEntryByPack(ctx, ws, packID)
	if err != nil || e == nil {
		return nil, err
	}
	out := &packapp.UIEntryInfo{ID: e.ID, Label: e.Label, URLTemplate: e.URLTemplate,
		Slots: make([]packapp.UISlotInfo, 0, len(e.Slots))}
	for _, s := range e.Slots {
		out.Slots = append(out.Slots, packapp.UISlotInfo{Slot: s.Slot, Label: s.Label, URLTemplate: s.URLTemplate, Height: s.Height})
	}
	return out, nil
}

// ---- 安装编排写端口适配（pack 域窄接口 → 各域服务） ----

// packCredentialsPort 把 orchestration CredentialService 适配成 installer 的
// 凭证端口（返回 id 字符串，pack 域不见 Credential 结构）。
type packCredentialsPort struct{ svc *orchapp.CredentialService }

func (a packCredentialsPort) Create(ctx context.Context, ws, provider, secret, createdBy string) (string, error) {
	c, err := a.svc.Create(ctx, ws, provider, secret, createdBy)
	if err != nil {
		return "", err
	}
	return c.ID, nil
}

// packMCPPort 把 MCPServerService.Create 适配成 installer 的 MCP 端口：
// bearerSecret 非空时构造 {"headers":{"Authorization":"Bearer <secret>"}} 信封
//（引擎 mcpHeadersOf 只认 headers 键——MCP 链路上 {"credential_id":id} 引用
// 无解引用点，真机 payload pack 探针 401 实证；凭证本体仍照常入密封库留作
// 审计/轮换底账）。credentials 是 map（pg 层 sealCreds 密封成 {"sealed":...} 信封）。
type packMCPPort struct{ svc *orchapp.MCPServerService }

func (a packMCPPort) Create(ctx context.Context, ws string, m packapp.InstallMCP, credID, bearerSecret, packID string) error {
	var creds map[string]any
	if bearerSecret != "" {
		creds = map[string]any{
			"credential_id": credID,
			"headers":       map[string]any{"Authorization": "Bearer " + bearerSecret},
		}
	}
	_, err := a.svc.Create(ctx, orchapp.WorkspaceMCP{
		WorkspaceID: ws, Name: m.Name, Transport: m.Transport, URL: m.URL,
		Credentials: creds, PackID: packID,
	})
	return err
}

// packWebhooksPort 把 framework WebhookService 适配成 installer 的订阅端口
// （CreateSubscription 返回 (Subscription, secret, error)——pack 域只见 id+secret）。
type packWebhooksPort struct{ svc *webhookapp.WebhookService }

func (a packWebhooksPort) CreateSubscription(ctx context.Context, ws, url, desc string, types []string) (string, string, error) {
	sub, secret, err := a.svc.CreateSubscription(ctx, ws, url, desc, types)
	if err != nil {
		return "", "", err
	}
	return sub.ID, secret, nil
}

// packUIEntryPort 把 PackUIEntryService.UpsertWithSlots 适配成 installer 的
// UI 端口（形状一一对应；slot 冻结校验在 orchestration 服务内）。
type packUIEntryPort struct{ svc *orchapp.PackUIEntryService }

func (a packUIEntryPort) UpsertWithSlots(ctx context.Context, ws, pack, packID, label, urlTemplate string, slots []packapp.UISlotInfo) error {
	ss := make([]orchapp.PackUISlot, 0, len(slots))
	for _, s := range slots {
		ss = append(ss, orchapp.PackUISlot{Slot: s.Slot, Label: s.Label, URLTemplate: s.URLTemplate, Height: s.Height})
	}
	return a.svc.UpsertWithSlots(ctx, ws, pack, packID, label, urlTemplate, ss)
}

// ---- agent/skill 归属写 binder 适配 ----

// packAgentBinder：orchestration Repo.SetAgentPack 的窄端口透出（AgentService
// 的 Get 归属闸 + repo ws 限定收口已在域内，适配层零逻辑）。
type packAgentBinder struct{ orchSvcBinder }

func (b packAgentBinder) SetAgentPack(ctx context.Context, ws, agentID string, packID *string) error {
	return b.repo.SetAgentPack(ctx, ws, agentID, packID)
}

type orchSvcBinder struct{ repo *orchpgrepo.Repo }

// packSkillBinder：skills SkillService.SetSkillPack 的窄端口透出。
type packSkillBinder struct{ svc *skillapp.SkillService }

func (b packSkillBinder) SetSkillPack(ctx context.Context, ws, skillID string, packID *string) error {
	return b.svc.SetSkillPack(ctx, ws, skillID, packID)
}

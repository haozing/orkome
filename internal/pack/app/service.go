// Package app 是 pack 域的服务层（pack 一等公民化 P1，W1-2）。
//
// Pack 是生态单元的聚合根：一次 provision 安装在 6 张表落行（MCP 注册表、
// 工具 schema 存档、agent、skill、webhook 订阅、UI 入口）。本服务持有 pack
// 表自身的 CRUD，其余五域经端口接口聚合读（组合根接线，模式同
// notification 的 SubscriberReader / MCPServerService.Resolved）——
// 不做跨模块 SQL 直查（仓内既有纪律，设计文档 §1.3）。
package app

import (
	"context"
	"time"

	"orkome/internal/contract/apierr"
)

// Pack 是 pack 聚合根行（表 pack，迁移 1042）。
type Pack struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	InstalledAt time.Time `json:"installed_at"`
}

// PackSummary 是列表页行：pack 本体 + 资产计数（/packs 页：名/版本/安装日期/资产计数）。
type PackSummary struct {
	Pack
	ServerCount int `json:"server_count"`
	AgentCount  int `json:"agent_count"`
	SkillCount  int `json:"skill_count"`
}

// MCPServerRef 是详情页"工具接线"区块的 MCP 服务器引用（workspace_mcp_server.pack_id 命中行）。
type MCPServerRef struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	URL       string `json:"url"`
	HasCreds  bool   `json:"has_credentials"`
}

// ToolSchemaRef 是详情页"工具"区块的存档行引用（pack_tool_schema，按 pack_id 命中）。
type ToolSchemaRef struct {
	Server      string    `json:"server"`
	ToolName    string    `json:"tool_name"`
	Source      string    `json:"source"` // protocol | pack_yaml | builtin_default
	InstalledAt time.Time `json:"installed_at"`
}

// UISlotInfo 是 ui.slots 落库行的一个 slot（v0 目录冻结：workitem.detail.tab）。
type UISlotInfo struct {
	Slot        string `json:"slot"`
	Label       string `json:"label"`
	URLTemplate string `json:"url_template"`
	Height      int    `json:"height"`
}

// UIEntryInfo 是详情页"界面"区块：控制台入口 + 工单 tab slots。
type UIEntryInfo struct {
	ID          string       `json:"id"`
	Label       string       `json:"label"`
	URLTemplate string       `json:"url_template"`
	Slots       []UISlotInfo `json:"slots"`
}

// SubRef 是详情页"事件"区块的订阅引用（pack_subscription → vendor webhook_subscription）。
type SubRef struct {
	SubscriptionID string   `json:"subscription_id"`
	URL            string   `json:"url"`
	EventTypes     []string `json:"event_types"`
	IsActive       bool     `json:"is_active"`
}

// AgentRef 是详情页"资产"区块的 agent 引用（agent.pack_id 命中行）。
type AgentRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}

// SkillRef 是详情页"资产"区块的 skill 引用（skill.origin='pack' AND origin_ref=pack_id）。
type SkillRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PackDetail 是详情页聚合（/packs/{packId} 五区块的数据面）。
type PackDetail struct {
	Pack

	Servers     []MCPServerRef `json:"servers"`
	ToolSchemas []ToolSchemaRef `json:"tool_schemas"`
	UIEntry     *UIEntryInfo   `json:"ui_entry,omitempty"`
	Subscriptions []SubRef     `json:"subscriptions"`
	Agents      []AgentRef     `json:"agents"`
	Skills      []SkillRef     `json:"skills"`
}

// Repo 是 pack 表自身的存储端口。
type Repo interface {
	// Upsert 按 (workspace_id, name) 幂等写入：存在则覆盖 version（重装即升级），
	// 不存在则插入。返回含 ID 的完整行。
	Upsert(ctx context.Context, ws, name, version string) (Pack, error)
	GetByID(ctx context.Context, ws, packID string) (Pack, bool, error)
	Delete(ctx context.Context, ws, packID string) error
	List(ctx context.Context, ws string) ([]Pack, error)
}

// ---- 跨域读端口（各自域的 pgrepo 提供实现，组合根接线） ----

// MCPServerReader 读 workspace_mcp_server.pack_id 命中的 MCP 注册表行（orchestration 域）。
type MCPServerReader interface {
	ListByPack(ctx context.Context, ws, packID string) ([]MCPServerRef, error)
}

// ToolSchemaReader 读 pack_tool_schema 按 pack_id 命中的存档行（orchestration 域）。
type ToolSchemaReader interface {
	ListByPack(ctx context.Context, ws, packID string) ([]ToolSchemaRef, error)
}

// AgentReader 读 agent.pack_id 命中的行（orchestration 域）。
type AgentReader interface {
	ListByPack(ctx context.Context, ws, packID string) ([]AgentRef, error)
}

// SkillReader 读 skill.origin='pack' AND origin_ref=pack_id 的行（skills 域）。
type SkillReader interface {
	ListByPack(ctx context.Context, ws, packID string) ([]SkillRef, error)
}

// UIEntryReader 读 pack_ui_entry.pack_id 命中的入口行（orchestration 域）。
type UIEntryReader interface {
	GetByPack(ctx context.Context, ws, packID string) (*UIEntryInfo, error)
}

// SubscriptionLinker 维护 pack_subscription 连接表 + 聚合读订阅详情。
// 订阅本体在 vendor webhook_subscription 表（不可加列）；Link/Unlink 只动
// orkome 侧连接表，本体删除仍走 vendor API。
type SubscriptionLinker interface {
	Link(ctx context.Context, packID, subscriptionID string) error
	UnlinkAll(ctx context.Context, packID string) error
	ListByPack(ctx context.Context, ws, packID string) ([]SubRef, error)
}

// Ports 聚合全部跨域端口（组合根一次性注入；nil 端口的区块返回空，不报错——
// 详情页五区块可独立缺失）。
type Ports struct {
	MCP      MCPServerReader
	Tools    ToolSchemaReader
	Agents   AgentReader
	Skills   SkillReader
	UIEntry  UIEntryReader
	Subs     SubscriptionLinker
}

// PackService 是 pack 聚合根的服务面。
type PackService struct {
	repo  Repo
	ports Ports
}

func NewPackService(repo Repo, ports Ports) *PackService { return &PackService{repo: repo, ports: ports} }

// List 返回工作区全部 pack 及资产计数（列表页）。
func (s *PackService) List(ctx context.Context, ws string) ([]PackSummary, error) {
	packs, err := s.repo.List(ctx, ws)
	if err != nil {
		return nil, err
	}
	out := make([]PackSummary, 0, len(packs))
	for _, p := range packs {
		sum := PackSummary{Pack: p}
		if s.ports.MCP != nil {
			if refs, err := s.ports.MCP.ListByPack(ctx, ws, p.ID); err == nil {
				sum.ServerCount = len(refs)
			}
		}
		if s.ports.Agents != nil {
			if refs, err := s.ports.Agents.ListByPack(ctx, ws, p.ID); err == nil {
				sum.AgentCount = len(refs)
			}
		}
		if s.ports.Skills != nil {
			if refs, err := s.ports.Skills.ListByPack(ctx, ws, p.ID); err == nil {
				sum.SkillCount = len(refs)
			}
		}
		out = append(out, sum)
	}
	return out, nil
}

// Get 返回详情聚合（五区块；区块端口未接线或区块为空时给空切片/nil，不报错）。
func (s *PackService) Get(ctx context.Context, ws, packID string) (PackDetail, error) {
	p, ok, err := s.repo.GetByID(ctx, ws, packID)
	if err != nil {
		return PackDetail{}, err
	}
	if !ok {
		return PackDetail{}, apierr.New(apierr.NotFound, "pack not found")
	}
	d := PackDetail{Pack: p}
	if s.ports.MCP != nil {
		if d.Servers, err = s.ports.MCP.ListByPack(ctx, ws, p.ID); err != nil {
			return PackDetail{}, err
		}
	}
	if s.ports.Tools != nil {
		if d.ToolSchemas, err = s.ports.Tools.ListByPack(ctx, ws, p.ID); err != nil {
			return PackDetail{}, err
		}
	}
	if s.ports.UIEntry != nil {
		entry, err := s.ports.UIEntry.GetByPack(ctx, ws, p.ID)
		if err != nil {
			return PackDetail{}, err
		}
		d.UIEntry = entry
	}
	if s.ports.Subs != nil {
		if d.Subscriptions, err = s.ports.Subs.ListByPack(ctx, ws, p.ID); err != nil {
			return PackDetail{}, err
		}
	}
	if s.ports.Agents != nil {
		if d.Agents, err = s.ports.Agents.ListByPack(ctx, ws, p.ID); err != nil {
			return PackDetail{}, err
		}
	}
	if s.ports.Skills != nil {
		if d.Skills, err = s.ports.Skills.ListByPack(ctx, ws, p.ID); err != nil {
			return PackDetail{}, err
		}
	}
	return d, nil
}

// Upsert 幂等写入 pack 行（provision 第 0 步）：存在则覆盖 version，返回含 ID。
func (s *PackService) Upsert(ctx context.Context, ws, name, version string) (Pack, error) {
	if name == "" {
		return Pack{}, apierr.NewValidation("name required")
	}
	return s.repo.Upsert(ctx, ws, name, version)
}

// Delete 删除 pack 行（P1 最小语义）：派生物（schema 存档/UI 入口/连接表）
// 由 FK CASCADE 跟删，MCP server / agent 由 FK SET NULL 置空来源（行保留）。
// 订阅本体的清理是 P2 uninstall 的显式动作，不在本服务内。
func (s *PackService) Delete(ctx context.Context, ws, packID string) error {
	return s.repo.Delete(ctx, ws, packID)
}

// Owns 校验 pack 属于该 workspace（连接表写入前的归属闸——防止跨区关联）。
func (s *PackService) Owns(ctx context.Context, ws, packID string) error {
	_, ok, err := s.repo.GetByID(ctx, ws, packID)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.New(apierr.NotFound, "pack not found")
	}
	return nil
}

// LinkSubscription 写连接表（provision 订阅创建后关联）。归属校验由
// HTTP 层先走 Owns；重复 Link 幂等（连接表 UNIQUE ON CONFLICT DO NOTHING）。
func (s *PackService) LinkSubscription(ctx context.Context, packID, subscriptionID string) error {
	if s.ports.Subs == nil {
		return apierr.New(apierr.Internal, "subscription linker not wired")
	}
	return s.ports.Subs.Link(ctx, packID, subscriptionID)
}

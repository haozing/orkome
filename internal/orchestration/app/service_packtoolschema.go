package app

import (
	"context"
	"time"

	"orkome/internal/contract/apierr"
)

// PackToolSchema 是 pack-provision 安装期存档的工具输出契约（迁移 1035，
// 表 pack_tool_schema）。B1（pack-provision 三闸门）写入；C1（统一 Tool 注册
// 表）在运行期桥接层读取，为协议未自描述 outputSchema 的 MCP 工具补输出契约。
// 接口冻结见 docs/interfaces-job-handle-2026-10-11.md §5/§6。
type PackToolSchema struct {
	WorkspaceID  string         `json:"workspace_id"`
	Pack         string         `json:"pack"`
	Server       string         `json:"server"`
	ToolName     string         `json:"tool_name"` // 含前缀全名 = 运行期桥接层的 toolName 键
	OutputSchema map[string]any `json:"output_schema"`
	Source       string         `json:"source"` // protocol | pack_yaml（builtin_default 为预留值，B1 不写）
	InstalledAt  time.Time      `json:"installed_at"`
}

// PackToolSchemaSource 的合法取值（与迁移 1035 的 CHECK 约束一致）。
const (
	PackToolSchemaSourceProtocol = "protocol"
	PackToolSchemaSourcePackYAML = "pack_yaml"
)

// PackToolSchemaDetail 是 pack 维度的 schema 明细读面（能力包平台化：
// usage-document 渲染"工具契约"区块用）。OutputSchemaJSON 保留 jsonb 原文，
// 不反序列化成 map——渲染层自行决定格式化或原样降级，避免有损往返。
type PackToolSchemaDetail struct {
	Server           string    `json:"server"`
	ToolName         string    `json:"tool_name"`
	Source           string    `json:"source"`
	OutputSchemaJSON string    `json:"output_schema_json"`
	InstalledAt      time.Time `json:"installed_at"`
}

type PackToolSchemaRepo interface {
	// ReplacePackToolSchemas 幂等覆盖一个 pack（workspace+pack 维度）的全部存档行：
	// 先删旧行再插入新行，以最新一次安装握手为准。
	ReplacePackToolSchemas(ctx context.Context, ws, pack string, items []PackToolSchema) error
	ListPackToolSchemas(ctx context.Context, ws string) ([]PackToolSchema, error)
	GetPackToolSchema(ctx context.Context, ws, toolName string) (PackToolSchema, bool, error)
	DeletePackToolSchemas(ctx context.Context, ws, pack string) error
}

type PackToolSchemaService struct{ repo PackToolSchemaRepo }

func NewPackToolSchemaService(repo PackToolSchemaRepo) *PackToolSchemaService {
	return &PackToolSchemaService{repo: repo}
}

// Replace 存档一个 pack 的全部工具 schema（provision 安装期调用，闸门全过后）。
func (s *PackToolSchemaService) Replace(ctx context.Context, ws, pack string, items []PackToolSchema) error {
	if ws == "" || pack == "" {
		return apierr.NewValidation("workspace and pack required")
	}
	for _, it := range items {
		if it.ToolName == "" || it.OutputSchema == nil {
			return apierr.NewValidation("tool_name and output_schema required")
		}
		if it.Source != PackToolSchemaSourceProtocol && it.Source != PackToolSchemaSourcePackYAML {
			return apierr.NewValidation("source must be protocol or pack_yaml")
		}
	}
	return s.repo.ReplacePackToolSchemas(ctx, ws, pack, items)
}

// ListPackToolSchemas 返回 workspace 下全部 pack 工具 schema 存档。
// C1（运行期桥接层）的读取入口：组装期一次拉全量，自建 toolName→schema map；
// 无存档返回空切片不报错（回落协议自描述/结构化探测）。
func (s *PackToolSchemaService) ListPackToolSchemas(ctx context.Context, ws string) ([]PackToolSchema, error) {
	return s.repo.ListPackToolSchemas(ctx, ws)
}

// GetPackToolSchema 按 toolName（含 pack 前缀全名）精确取一条存档。
// ok=false 表示未存档（该工具协议自描述或 v0 pack），调用方自行回落。
func (s *PackToolSchemaService) GetPackToolSchema(ctx context.Context, ws, toolName string) (PackToolSchema, bool, error) {
	return s.repo.GetPackToolSchema(ctx, ws, toolName)
}

// Delete 删除一个 pack 的全部存档行（provision uninstall 调用）。
func (s *PackToolSchemaService) Delete(ctx context.Context, ws, pack string) error {
	return s.repo.DeletePackToolSchemas(ctx, ws, pack)
}

import { api } from '@ploykit/client'

// Packs 与集成页的数据封装（docs/pack-frontend-design-2026-10-11.md §3）。
// 两块数据源：
//  1) GET /api/ui-entries —— 已安装 pack 只读列表（pack_ui_entry 表，迁移 1033）；
//     handler: internal/orchestration/adapters/http/handlers_uientry.go（X-Workspace-Id 头）
//  2) /api/workspaces/{id}/mcp-servers —— 工作区级 MCP 服务器注册表
//     （internal/orchestration/adapters/http/routes.go:162-165，workspace 在 URL path，
//      非 X-Workspace-Id 头；此前前端零调用，本文件为首次封装）。

/**
 * pack slot 声明（pack.yaml ui.slots[] 的落库形状，迁移 1036）。
 * v0 slot 目录冻结：slot 标识仅 workitem.detail.tab（前端设计 §1.4）。
 */
export interface PackUISlot {
  slot: string
  label: string
  url_template: string
  /** 声明高度 px（resize 协议兜底前的默认值） */
  height: number
}

/** pack_ui_entry 行（GET /api/ui-entries 的 items[] 元素） */
export interface PackUIEntry {
  id: string
  workspace_id: string
  pack: string
  label: string
  url_template: string
  /** C2 起随行返回；后端保证为 []（非 null）——老版本后端无此字段，消费方按 [] 兜底 */
  slots?: PackUISlot[]
  installed_at: string
}

export async function listPackEntries(): Promise<PackUIEntry[]> {
  const res = await api.get<{ items: PackUIEntry[] | null }>('/api/ui-entries')
  return res.items ?? []
}

/**
 * 工作区级 MCP 服务器（workspace_mcp_server 表）。
 * 后端 WorkspaceMCP 的 credentials 字段 json:"-" 不回传，只回 has_credentials 布尔。
 * pack_id（迁移 1042，Pack 一等公民化）：非空 = 该行由 pack-provision 安装产出，
 * 来源能力包详情见 /packs/{pack_id}；空/缺省 = 手工注册。
 */
export interface WorkspaceMCPServer {
  id: string
  workspace_id: string
  name: string
  transport: string
  url: string
  has_credentials: boolean
  created_at: string
  pack_id?: string
}

/**
 * 后端 mcpWire（adapters/http/handlers_mcp.go）；transport 空串时服务端默认 "http"。
 * 三态凭证（W2-3 / FP-09 P1-4 裁决，两级统一）：
 *  - 不带 credentials 字段（undefined，序列化后键不存在）= 保留库中现值；
 *  - credentials: null / {} = 显式清空；
 *  - credentials: {…} = 覆盖（后端 sealx 加密落库）。
 */
export interface WorkspaceMCPInput {
  name: string
  transport?: string
  url: string
  credentials?: Record<string, unknown> | null
}

/** GET /api/workspaces/{id}/mcp-servers → { servers: [...] } */
export async function listWorkspaceMcp(workspaceId: string): Promise<WorkspaceMCPServer[]> {
  const res = await api.get<{ servers: WorkspaceMCPServer[] | null }>(
    `/api/workspaces/${workspaceId}/mcp-servers`,
  )
  return res.servers ?? []
}

/** POST /api/workspaces/{id}/mcp-servers → 201 + WorkspaceMCPServer */
export function createWorkspaceMcp(workspaceId: string, input: WorkspaceMCPInput) {
  return api.post<WorkspaceMCPServer>(`/api/workspaces/${workspaceId}/mcp-servers`, input)
}

/**
 * PUT /api/workspaces/{id}/mcp-servers/{serverId} → 200 + WorkspaceMCPServer
 * 三态凭证：credentials 不传（键不存在）= 保留已存凭证；null/{} = 清空；值 = 覆盖
 * （W2-3 与 agent 级统一后的语义，页面侧 WorkspaceMcpDialog 按此口径提交）。
 */
export function updateWorkspaceMcp(
  workspaceId: string,
  serverId: string,
  input: WorkspaceMCPInput,
) {
  return api.put<WorkspaceMCPServer>(
    `/api/workspaces/${workspaceId}/mcp-servers/${serverId}`,
    input,
  )
}

/** DELETE /api/workspaces/{id}/mcp-servers/{serverId} → { result: "deleted" } */
export function deleteWorkspaceMcp(workspaceId: string, serverId: string) {
  return api.delete<{ result: string }>(
    `/api/workspaces/${workspaceId}/mcp-servers/${serverId}`,
  )
}

/** 单台 MCP 服务器的工具探针结果（POST probe-tools，实时建连非缓存）。 */
export interface McpProbeTool {
  name: string
  description?: string
  output_schema?: Record<string, unknown>
}

/**
 * POST /api/workspaces/{id}/mcp-servers/{serverId}/probe-tools
 * 实时建连目标服务器 ListTools（SSRF 校验 / 10s 预算 / 32 工具上限与运行期同参）。
 */
export function probeWorkspaceMcpTools(workspaceId: string, serverId: string) {
  return api.post<{ tools: McpProbeTool[] }>(
    `/api/workspaces/${workspaceId}/mcp-servers/${serverId}/probe-tools`,
    {},
  )
}

// ---------------------------------------------------------------------------
// Pack 聚合根（docs/pack-first-class-design-2026-10-13.md §1.3，迁移 1042）。
// pack 从"散落 5 张表的字符串键"升为一等实体；以下两个端点是 P1 前端（/packs
// 列表 + 详情五区块）的唯一新数据源。workspace 经 X-Workspace-Id 头传递
// （apiFetch 的 workspaceIdProvider 自动附带，与 listPackEntries 同参）。
// ---------------------------------------------------------------------------

/** GET /api/packs 的 items[] 元素：pack 行 + 三个资产计数。 */
export interface PackEntry {
  id: string
  name: string
  version: string
  installed_at: string
  server_count: number
  agent_count: number
  skill_count: number
}

/** GET /api/packs → { items: [...] } */
export async function listPacks(): Promise<PackEntry[]> {
  const res = await api.get<{ items: PackEntry[] | null }>('/api/packs')
  return res.items ?? []
}

/** 详情聚合：pack_tool_schema 存档清单的一行。 */
export interface PackToolSchema {
  server: string
  tool_name: string
  /** protocol = 握手自描述；pack_yaml = pack.yaml 声明（契约边界，B1） */
  source: string
  installed_at: string
}

/** 详情聚合：pack_ui_entry + slots（控制台入口与工单 tab）。 */
export interface PackDetailUIEntry {
  label: string
  url_template: string
  slots: { slot: string; label: string; height: number }[]
}

/** 详情聚合：事件订阅（pack_subscription 连接表命中 ploykit webhook_subscription）。 */
export interface PackSubscription {
  subscription_id: string
  url: string
  event_types: string[]
  is_active: boolean
}

/** 详情聚合：本包创建的智能体（pack_id 命中，archived 软删）。 */
export interface PackAgent {
  id: string
  name: string
  archived: boolean
}

/** 详情聚合：本包创建的技能（origin='pack' AND origin_ref=pack_id，D1 修复）。 */
export interface PackSkill {
  id: string
  name: string
}

/** GET /api/packs/{packId} 的聚合详情（五区块的数据形状）。 */
export interface PackDetail {
  pack: { id: string; name: string; version: string; installed_at: string }
  servers: { name: string; transport: string; url: string; has_credentials: boolean }[]
  tool_schemas: PackToolSchema[]
  ui_entry: PackDetailUIEntry | null
  subscriptions: PackSubscription[]
  agents: PackAgent[]
  skills: PackSkill[]
}

/** GET /api/packs/{packId} → PackDetail 聚合 */
export function getPack(packId: string) {
  return api.get<PackDetail>(`/api/packs/${packId}`)
}

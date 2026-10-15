import { api } from '@ploykit/client'

// 审计日志端点封装（settings 域设计 docs/settings-domain-design-2026-10-14.md §3.7，
// wire 形状从 ploykit audit/adapters/http/routes.go + audit.go 冻结提取）。
// 路由挂 /api/audit（workspace 经 X-Workspace-Id 头，apiFetch 自动附带）；
// 权限 audit:read 仅 owner/admin——member 调用 403，组件 catch 显权限空态。

/** actor_snapshot（快照冗余：操作发生时的身份信息；role/workspace_id 可缺省）。 */
export interface AuditActorSnapshot {
  email: string
  role?: string
  workspace_id?: string
}

/** audit_event 行（GET /api/audit 的 items[] 元素；Recorder.Query map 形状冻结）。 */
export interface AuditEntry {
  id: string
  workspace_id: string
  actor_type: 'user' | 'pat' | 'system'
  actor_id?: string | null
  actor_snapshot: AuditActorSnapshot
  action: string
  resource_type: string
  resource_id?: string | null
  metadata: Record<string, unknown>
  created_at: string
}

/** 列表/导出共用的过滤参数（空串字段不拼进 query；from/to 须 RFC3339）。 */
export interface AuditFilters {
  actor_id?: string
  action?: string
  resource_type?: string
  from?: string
  to?: string
}

/** 把过滤参数拼成 query 串（空值跳过；调用方保证 RFC3339 已就绪）。 */
function buildQuery(filters: AuditFilters, extra: Record<string, string> = {}): string {
  const params = new URLSearchParams()
  for (const key of ['actor_id', 'action', 'resource_type', 'from', 'to'] as const) {
    const v = filters[key]
    if (v) params.set(key, v)
  }
  for (const [k, v] of Object.entries(extra)) params.set(k, v)
  return params.toString()
}

/** GET /api/audit?…&limit&offset → 信封 {items,total}（items 归一 [];total 计数含过滤）。
 *  workspaceId 参数按任务书签名保留：workspace 实际经 apiFetch 的 X-Workspace-Id 头
 *  传递（/api 前缀自动附带），它同时参与 query key 区分工作区缓存。 */
export async function listAudit(
  _workspaceId: string,
  filters: AuditFilters,
  limit = 50,
  offset = 0,
): Promise<{ items: AuditEntry[]; total: number }> {
  const qs = buildQuery(filters, { limit: String(limit), offset: String(offset) })
  const page = await api.get<{ items: AuditEntry[] | null; total?: number }>(
    `/api/audit${qs ? `?${qs}` : ''}`,
  )
  return { items: page.items ?? [], total: page.total ?? 0 }
}

/**
 * 导出 CSV 并触发浏览器下载：apiFetch 拉全文（同源 cookie + X-Workspace-Id 头）
 * 后转 blob 保存。不用 window.open 直下——服务端 WorkspaceHeaderCtx 只认
 * X-Workspace-Id 头（vendor workspace/adapters/http/routes.go:169），导航请求
 * 带不上该头会 400 "X-Workspace-Id header required"。
 */
export async function auditCsvDownload(workspaceId: string, filters: AuditFilters): Promise<void> {
  void workspaceId // workspace 经 apiFetch 自动附带的 X-Workspace-Id 头传递
  const qs = buildQuery(filters)
  const res = await fetch(`/api/audit/export.csv${qs ? `?${qs}` : ''}`, {
    method: 'GET',
    credentials: 'same-origin',
    headers: { 'X-Workspace-Id': exportWorkspaceId() },
  })
  if (!res.ok) throw new Error(`export failed (${res.status})`)
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `audit-${new Date().toISOString().slice(0, 10)}.csv`
  a.click()
  URL.revokeObjectURL(url)
}

/** 导出时的 workspace 头取值（由 main.tsx 注入 workspace store，非组件上下文可用）。 */
let exportWorkspaceId: () => string = () => ''
export function setAuditExportWorkspaceProvider(provider: () => string): void {
  exportWorkspaceId = provider
}

import { api, isApiError } from '@ploykit/client'

// 快捷动作（Quick Actions）Web 面数据封装（W1-2）。
// wire 逐字段照抄后端：internal/conversation/adapters/http/routes.go:488-559
// （handlers）与 internal/conversation/app/service_quickaction.go:19-27（结构体）。
// workspace 经 X-Workspace-Id 头传递（apiFetch 的 workspaceIdProvider 自动附带）。
// 每用户上限 3 条（服务端 MaxQuickActions=3），label ≤80 字符。

/** quick_actions 表行（后端 app.QuickAction 的 JSON wire）。 */
export interface QuickAction {
  id: string
  workspace_id: string
  user_id: string
  label: string
  prompt: string
  /** 服务端固定 true（个人私有，Create 强制覆写，客户端传值无效） */
  is_private: boolean
  created_at: string
}

/** 新建/编辑入参：仅 label/prompt 两字段，其余均由服务端定。 */
export interface QuickActionInput {
  label: string
  prompt: string
}

/** GET /api/quick-actions → { quick_actions }（后端已兜 null→[]，仍按 [] 兜底）。 */
export async function listQuickActions(): Promise<QuickAction[]> {
  const res = await api.get<{ quick_actions: QuickAction[] | null }>('/api/quick-actions')
  return res.quick_actions ?? []
}

/** POST /api/quick-actions → 201 + QuickAction 单对象；400 校验失败（label 必填 ≤80、prompt 必填）。 */
export function createQuickAction(input: QuickActionInput) {
  return api.post<QuickAction>('/api/quick-actions', input)
}

/** PATCH /api/quick-actions/{quickActionId} → 200 + QuickAction；404 非本人/不存在。 */
export function updateQuickAction(id: string, input: QuickActionInput) {
  return api.patch<QuickAction>(`/api/quick-actions/${encodeURIComponent(id)}`, input)
}

/** DELETE /api/quick-actions/{quickActionId} → 200 { result: "deleted" }。 */
export function removeQuickAction(id: string) {
  return api.delete<{ result: string }>(`/api/quick-actions/${encodeURIComponent(id)}`)
}

/**
 * POST /api/work-items/{workItemId}/quick-actions/{quickActionId}/run
 * → 201 { task_id }。失败两型：
 *  - 409 E_ACTIVE_DUPLICATE：error.details = [{field:"active_task_id",problem:"<任务id>"},{field:"reason",problem:"<原因>"}]
 *    （FieldError JSON 键为小写 field/problem，webx.WriteError 包在 error.details 下）；
 *  - 400 E_VALIDATION："work item has no agent assignee"。
 */
export function runQuickAction(workItemId: string, id: string) {
  return api.post<{ task_id: string }>(
    `/api/work-items/${encodeURIComponent(workItemId)}/quick-actions/${encodeURIComponent(id)}/run`,
  )
}

/** POST /api/work-items/{workItemId}/quick-actions/{quickActionId}/render → 200 { prompt }。 */
export function renderQuickAction(workItemId: string, id: string) {
  return api.post<{ prompt: string }>(
    `/api/work-items/${encodeURIComponent(workItemId)}/quick-actions/${encodeURIComponent(id)}/render`,
  )
}

/**
 * 从 ApiError.details 提取 409 ActiveDuplicate 的在途任务 id
 * （details 元素形如 { field: string; problem: string }，取 active_task_id 的 problem）。
 *
 * wire 核对（2026-10-18 勘误）：webx.ErrorBody.Code 序列化为扁平字符串信封
 * `{error:"E_CODE",message,details}`（vendor respond.go:14 `json:"error"`），
 * 客户端 `typeof body?.error === 'string'` 命中 → ApiError 完整保留
 * message/details——本函数即主路径。曾有"对象形 envelope 失配"的误报，
 * 系测试 mock 写错所致，已勘误。getWorkItemActiveTask 兜底仅防非标准响应。
 */
export function activeTaskIdFromError(err: unknown): string | null {
  if (!isApiError(err) || err.status !== 409) return null
  if (!Array.isArray(err.details)) return null
  for (const d of err.details) {
    if (
      d &&
      typeof d === 'object' &&
      (d as { field?: unknown }).field === 'active_task_id' &&
      typeof (d as { problem?: unknown }).problem === 'string'
    ) {
      return (d as { problem: string }).problem
    }
  }
  return null
}

/**
 * GET /api/work-items/{workItemId}/active-task → { task | null }
 * （internal/orchestration/adapters/http/handlers_taskview.go:187，与 409 Details
 * 的 active_task_id 同一 GetActiveTask 数据源——仅作 Details 缺失时的防御性兜底）。
 */
export function getWorkItemActiveTask(workItemId: string): Promise<{ id: string } | null> {
  return api.get<{ task: { id: string } | null }>(
    `/api/work-items/${encodeURIComponent(workItemId)}/active-task`,
  ).then((res) => res.task)
}

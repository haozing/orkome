import { api } from '@ploykit/client'
export interface Autopilot {
  id: string
  workspace_id: string
  name: string
  exec_mode: 'create_work_item' | 'task_only' | string
  template: Record<string, unknown>
  target_agent_id: string
  enabled: boolean
  daily_limit: number
  paused_reason?: string
}
export interface AutopilotRun {
  id: string
  autopilot_id: string
  source: string
  status: string
  reason_code?: string
  planned_at?: string
  triggered_at?: string
  work_item_id?: string
  task_id?: string
}
/**
 * 唤醒规则（internal/automation/app/service.go Wakeup）。
 * kind 三态：time|event|condition（system_rule 非空的是平台托管的 system 行，
 * 不可删除/编辑——后端 DELETE 会 400）。
 */
export interface Wakeup {
  id: string
  workspace_id: string
  work_item_id: string
  agent_id?: string
  kind: 'time' | 'event' | 'condition' | 'system' | string
  instruction: string
  interval_seconds?: number
  cron_expression?: string
  timezone: string
  next_fire_at?: string
  enabled: boolean
  fire_count: number
  max_fires?: number
  event_types?: string[]
  system_rule?: string
  paused_reason?: string
  expires_at?: string
}
/**
 * autopilot 触发器（service.go Trigger）。注意契约缺口：routes.go 只有
 * POST/PATCH/DELETE triggers，没有 GET 列表端点，前端无法枚举既有触发器。
 */
export interface AutopilotTrigger {
  id: string
  autopilot_id: string
  kind: 'schedule' | 'webhook' | 'manual' | string
  cron_expression?: string
  timezone: string
  next_run_at?: string
  enabled: boolean
}
export async function listAutopilots(): Promise<Autopilot[]> {
  const items = await api.get<Autopilot[] | null>('/api/autopilots')
  return items ?? []
}
export interface AutopilotCreateInput {
  name: string
  exec_mode: string
  template: Record<string, unknown>
  target_agent_id: string
  /** 空串=仅手动触发（后端会建一个空 cron 的惰性兜底 trigger，不挡删除）。 */
  cron_expression: string
  /** IANA 名称或空串（=UTC）；"Local" 会被后端拒绝。 */
  timezone: string
}
export function createAutopilot(input: AutopilotCreateInput): Promise<Autopilot> {
  return api.post<Autopilot>('/api/autopilots', input)
}
export function setAutopilotEnabled(id: string, enabled: boolean): Promise<Autopilot> {
  return api.patch<Autopilot>(`/api/autopilots/${id}`, { enabled })
}
export function triggerAutopilot(id: string): Promise<AutopilotRun> {
  return api.post<AutopilotRun>(`/api/autopilots/${id}/trigger`)
}
export function deleteAutopilot(id: string): Promise<void> {
  return api.delete<void>(`/api/autopilots/${id}`)
}
/**
 * POST /api/autopilots/cron-preview → {next_runs: string[]}。
 * 后端 createAutopilot 对坏 cron 返回 500（非 400），所以前端在创建前
 * 先用这个端点做真源校验，坏 cron 时不给用户提交。
 */
export async function cronPreview(
  cronExpression: string,
  timezone: string,
  count = 1,
): Promise<string[]> {
  const res = await api.post<{ next_runs: string[] | null }>('/api/autopilots/cron-preview', {
    cron_expression: cronExpression,
    timezone,
    count,
  })
  return res.next_runs ?? []
}
export async function listWorkspaceWakeups(): Promise<Wakeup[]> {
  const items = await api.get<Wakeup[] | null>('/api/work-item-wakeups')
  return items ?? []
}
export interface WakeupCreateInput {
  kind: 'time'
  instruction: string
  /** 一次性唤醒：datetime-local 就地转 5 段 cron（分钟 时 日 月 *），配 max_fires=1。 */
  cron_expression: string
  /** 浏览器 IANA 时区；cron 字段按它解释。 */
  timezone: string
  max_fires: number
}
export function createWakeup(workItemId: string, input: WakeupCreateInput): Promise<Wakeup> {
  return api.post<Wakeup>(`/api/work-items/${workItemId}/wakeups`, input)
}
export function deleteWakeup(workItemId: string, wakeupId: string): Promise<void> {
  return api.delete<void>(`/api/work-items/${workItemId}/wakeups/${wakeupId}`)
}

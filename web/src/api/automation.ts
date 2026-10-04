import { api } from '@/lib/api'
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
  triggered_at?: string
}
export async function listAutopilots(): Promise<Autopilot[]> {
  const items = await api.get<Autopilot[] | null>('/api/autopilots')
  return items ?? []
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

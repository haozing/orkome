import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'
import { listAgents } from '@/api/orchestration'
import { Badge } from '@/components/ui/badge'
import { useTranslation } from 'react-i18next'
export const TASK_STATUS: Record<
  string,
  {
    labelKey: string
    tone: 'neutral' | 'warning' | 'success' | 'danger'
  }
> = {
  queued: { labelKey: 'taskStatus.queued', tone: 'neutral' },
  deferred: { labelKey: 'taskStatus.deferred', tone: 'neutral' },
  dispatched: { labelKey: 'taskStatus.dispatched', tone: 'warning' },
  running: { labelKey: 'taskStatus.running', tone: 'warning' },
  awaiting_input: { labelKey: 'taskStatus.awaitingInput', tone: 'warning' },
  completed: { labelKey: 'taskStatus.completed', tone: 'success' },
  failed: { labelKey: 'taskStatus.failed', tone: 'danger' },
  cancelled: { labelKey: 'taskStatus.cancelled', tone: 'danger' },
  // 助理任务新增终态（设计 v4 §4.3）：AI 处理超时 4h 后平台把工单交还用户，
  // 任务行留档。语义是"回到人手里"而非失败，故 neutral 灰、不进 danger。
  stalled: { labelKey: 'taskStatus.stalled', tone: 'neutral' },
}
export const ACTIVE_TASK_STATUSES = new Set([
  'queued',
  'deferred',
  'dispatched',
  'running',
  'awaiting_input',
])
export function TaskStatusBadge({ status }: { status: string }) {
  const { t } = useTranslation()
  const meta = TASK_STATUS[status] ?? { labelKey: status, tone: 'neutral' as const }
  return <Badge variant={meta.tone}>{t(meta.labelKey)}</Badge>
}
export function useAgentNameMap() {
  // 名字映射必须看到 assistant_proxy 代理行（设计 v4 §4.1）：代理任务/代理评论
  // 的作者就是这行 agent，名字映射缺了它就只剩 ID 前缀 fallback。
  const agents = useQuery({
    queryKey: ['agents'],
    queryFn: () => listAgents({ includeAssistantProxy: true }),
    staleTime: 60000,
  })
  return useMemo(() => {
    const map = new Map<string, string>()
    for (const a of agents.data ?? []) map.set(a.id, a.name)
    return map
  }, [agents.data])
}
export function agentDisplayName(map: Map<string, string>, agentId: string): string {
  return map.get(agentId) ?? `agent ${agentId.slice(0, 8)}…`
}

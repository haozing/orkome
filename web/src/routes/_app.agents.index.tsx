import { useQuery } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { Bot, Plus } from 'lucide-react'
import { useState } from 'react'
import { listAgents, listWorkingAgents } from '@/api/orchestration'
import { CreateAgentDialog } from '@/components/agent/create-agent-dialog'
import { visibilityMeta } from '@/components/agent/visibility'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { cn } from '@/lib/utils'
import { useTranslation } from 'react-i18next'
export const Route = createFileRoute('/_app/agents/')({
  component: AgentsPage,
})
type Filter = 'all' | 'active' | 'archived'
export function AgentsPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [filter, setFilter] = useState<Filter>('all')
  const [createOpen, setCreateOpen] = useState(false)
  const agents = useQuery({ queryKey: ['agents'], queryFn: () => listAgents() })
  const working = useQuery({ queryKey: ['working-agents'], queryFn: listWorkingAgents })
  const activeCount = new Map((working.data ?? []).map((w) => [w.agent_id, w.active]))
  const rows = (agents.data ?? []).filter((a) =>
    filter === 'all' ? true : filter === 'archived' ? a.archived : !a.archived,
  )
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">{t('nav.agents')}</h1>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" aria-hidden="true" />
          {t('agentsList.newAgent')}
        </Button>
      </div>

      {/* 筛选是切换按钮组（aria-pressed），不是 tablist——无对应 tabpanel 的
          role=tab 会被读屏报语义错（ui-sweep 裁决）。 */}
      <div className="flex gap-1" role="group" aria-label={t('agentsList.filterAria')}>
        {(
          [
            ['all', 'agentsList.all'],
            ['active', 'agentsList.active'],
            ['archived', 'agentsList.archived'],
          ] as const
        ).map(([key, label]) => (
          <button
            key={key}
            type="button"
            aria-pressed={filter === key}
            className={
              'cursor-default rounded-md px-3 py-1.5 text-sm outline-none ' +
              (filter === key
                ? 'bg-zinc-100 font-medium text-zinc-900 dark:bg-zinc-800 dark:text-zinc-100'
                : 'text-zinc-500 hover:bg-zinc-100 hover:text-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-200')
            }
            onClick={() => setFilter(key)}
          >
            {t(label)}
          </button>
        ))}
      </div>

      {agents.isPending && <PageLoading label={t('agentsList.loading')} />}

      {agents.isError && (
        <PageError
          title={t('common.error')}
          message={agents.error instanceof Error ? agents.error.message : undefined}
          onRetry={() => void agents.refetch()}
        />
      )}

      {agents.isSuccess && rows.length === 0 && (
        <PageEmpty
          title={
            filter === 'archived'
              ? t('agentsList.emptyArchived')
              : filter === 'active'
                ? t('agentsList.emptyActive')
                : t('agentsList.empty')
          }
          description={t('agentsList.emptyDesc')}
        />
      )}

      {rows.length > 0 && (
        <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
          {rows.map((a) => {
            const model = [a.runtime_config?.provider, a.runtime_config?.model]
              .filter(Boolean)
              .join('·')
            const active = activeCount.get(a.id) ?? 0
            return (
              <li key={a.id}>
                <Link
                  to="/agents/$agentId"
                  params={{ agentId: a.id }}
                  className={cn(
                    'flex items-center gap-3 px-3 py-2.5 text-sm outline-none hover:bg-zinc-50 dark:hover:bg-zinc-900/50',
                    a.archived && 'opacity-55',
                  )}
                >
                  <Bot className="size-4 shrink-0 text-zinc-400" aria-hidden="true" />
                  <span className="min-w-0 flex-1 truncate font-medium">{a.name}</span>
                  {model && (
                    <span className="hidden shrink-0 font-mono text-xs text-zinc-400 sm:inline">
                      {model}
                    </span>
                  )}
                  {(() => {
                    const vis = visibilityMeta(a.visibility)
                    return <Badge variant={vis.tone}>{t(vis.labelKey)}</Badge>
                  })()}
                  {a.archived ? (
                    <Badge variant="neutral">{t('agents.archived')}</Badge>
                  ) : active > 0 ? (
                    <Badge variant="success">{t('agentsList.activeCount', { n: active })}</Badge>
                  ) : null}
                </Link>
              </li>
            )
          })}
        </ul>
      )}

      <CreateAgentDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(a) => void navigate({ to: '/agents/$agentId', params: { agentId: a.id } })}
      />
    </div>
  )
}

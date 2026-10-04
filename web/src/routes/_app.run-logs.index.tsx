import { Dialog } from '@base-ui/react/dialog'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { createFileRoute, Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import {
  listAgents,
  listTaskMessages,
  listWorkspaceTasks,
  type WorkspaceTaskRow,
} from '@/api/orchestration'
import { TaskMessageList } from '@/components/task-messages'
import { TaskStatusBadge, agentDisplayName, useAgentNameMap } from '@/components/task-status-badge'
import { Button } from '@/components/ui/button'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { cn } from '@/lib/utils'
const searchSchema = z.object({
  status: z.enum(['active', 'failed', 'completed', 'all']).optional(),
  agent: z.string().optional(),
})
type RunLogSearch = z.infer<typeof searchSchema>
export const Route = createFileRoute('/_app/run-logs/')({
  validateSearch: (search) => {
    const parsed = searchSchema.safeParse(search)
    return parsed.success ? parsed.data : {}
  },
  component: RunLogsPage,
})
const STATUS_FILTERS = [
  { key: 'active', label: 'runLogs.filterActive' },
  { key: 'failed', label: 'runLogs.filterFailed' },
  { key: 'completed', label: 'runLogs.filterCompleted' },
  { key: 'all', label: 'runLogs.filterAll' },
] as const
const TRIGGER_KEYS: Record<string, string> = {
  assignment: 'agents.triggerAssignment',
  member_comment: 'agents.triggerMemberComment',
  chat: 'agents.triggerChat',
  squad: 'agents.triggerSquad',
  leader_dispatch: 'agents.triggerLeaderDispatch',
  rule: 'agents.triggerRule',
  rerun: 'agents.triggerRerun',
}
function RunLogsPage() {
  const { t } = useTranslation()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const status = search.status ?? 'active'
  const agentId = search.agent ?? ''
  const [openTask, setOpenTask] = useState<WorkspaceTaskRow | null>(null)
  const patchSearch = (patch: RunLogSearch) =>
    void navigate({ search: (prev) => ({ ...prev, ...patch }), replace: true })
  const agents = useQuery({ queryKey: ['agents'], queryFn: listAgents, staleTime: 60000 })
  const list = useInfiniteQuery({
    queryKey: ['workspace-tasks', { status, agent_id: agentId }],
    queryFn: ({ pageParam }: { pageParam?: string }) =>
      listWorkspaceTasks({
        status: status === 'all' ? undefined : status,
        agent_id: agentId || undefined,
        cursor: pageParam || undefined,
      }),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })
  const rows = list.data?.pages.flatMap((p) => p.tasks) ?? []
  const hasMore = list.hasNextPage
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h1 className="text-lg font-semibold">{t('runLogs.title')}</h1>
        <Button variant="outline" size="sm" onClick={() => void list.refetch()}>
          {t('agents.refresh')}
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div
          className="flex gap-1 whitespace-nowrap"
          role="tablist"
          aria-label={t('runLogs.statusFilter')}
        >
          {STATUS_FILTERS.map((f) => (
            <button
              key={f.key}
              type="button"
              role="tab"
              aria-selected={status === f.key}
              className={
                'cursor-default rounded-md px-3 py-1.5 text-sm outline-none ' +
                (status === f.key
                  ? 'bg-zinc-100 font-medium text-zinc-900 dark:bg-zinc-800 dark:text-zinc-100'
                  : 'text-zinc-500 hover:bg-zinc-100 hover:text-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-200')
              }
              onClick={() => patchSearch({ status: f.key === 'active' ? undefined : f.key })}
            >
              {t(f.label)}
            </button>
          ))}
        </div>
        <select
          aria-label={t('runLogs.agentFilter')}
          value={agentId}
          onChange={(e) => patchSearch({ agent: e.target.value || undefined })}
          className="h-8 max-w-full rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none dark:border-zinc-700 dark:bg-zinc-900"
        >
          <option value="">{t('runLogs.allAgents')}</option>
          {(agents.data ?? []).map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}
            </option>
          ))}
        </select>
      </div>

      {list.isPending && <PageLoading label={t('runLogs.loading')} />}

      {list.isError && (
        <PageError
          title={t('common.error')}
          message={list.error instanceof Error ? list.error.message : undefined}
          onRetry={() => void list.refetch()}
        />
      )}

      {list.isSuccess && rows.length === 0 && (
        <PageEmpty
          title={
            agentId || status !== 'active'
              ? t('runLogs.emptyFilteredTitle')
              : t('runLogs.emptyActiveTitle')
          }
          description={
            agentId || status !== 'active'
              ? t('runLogs.emptyFilteredDesc')
              : t('runLogs.emptyActiveDesc')
          }
        />
      )}

      {rows.length > 0 && (
        <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
          {rows.map((task) => (
            <li key={task.id}>
              <TaskRowView task={task} onOpen={() => setOpenTask(task)} />
            </li>
          ))}
        </ul>
      )}

      {hasMore && (
        <div className="flex justify-center">
          <Button
            variant="outline"
            size="sm"
            disabled={list.isFetchingNextPage}
            onClick={() => void list.fetchNextPage()}
          >
            {list.isFetchingNextPage ? t('common.loading') : t('runLogs.loadMore')}
          </Button>
        </div>
      )}

      {openTask && <TaskTranscriptDrawer task={openTask} onClose={() => setOpenTask(null)} />}
    </div>
  )
}
function TaskRowView({ task, onOpen }: { task: WorkspaceTaskRow; onOpen: () => void }) {
  const { t, i18n } = useTranslation()
  const formatTime = (iso: string) =>
    new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  return (
    <div className="space-y-1 px-3 py-2.5">
      <button
        type="button"
        onClick={onOpen}
        className="flex w-full cursor-default items-center gap-3 py-1 text-left text-sm outline-none"
        title={t('runLogs.viewTranscript')}
      >
        <TaskStatusBadge status={task.status} />
        <span className="hidden shrink-0 text-sm sm:inline">{task.agent_name}</span>
        <span className="min-w-0 flex-1 truncate text-xs text-zinc-500 dark:text-zinc-400">
          {task.work_item_title
            ? task.work_item_title
            : task.work_item_id
              ? t('runLogs.workItemDeleted')
              : t('agents.directTask')}
        </span>
        {task.trigger_evidence_kind && (
          <span className="hidden shrink-0 text-xs text-zinc-400 sm:inline">
            {TRIGGER_KEYS[task.trigger_evidence_kind]
              ? t(TRIGGER_KEYS[task.trigger_evidence_kind])
              : task.trigger_evidence_kind}
          </span>
        )}
        {task.attempt > 1 && (
          <span className="shrink-0 text-xs text-amber-600 dark:text-amber-400">
            {t('agents.retryAttempt', { attempt: task.attempt, max: task.max_attempts })}
          </span>
        )}
        <span className="hidden shrink-0 text-xs text-zinc-400 sm:inline">
          {formatTime(task.created_at)}
        </span>
      </button>
      {task.failure_reason && (
        <p className="pl-1 font-mono text-xs text-red-600 dark:text-red-400">
          {task.failure_reason}
        </p>
      )}
      {task.work_item_id && (
        <Link
          to="/work-items/$id"
          params={{ id: task.work_item_id }}
          className="inline-block py-0.5 pl-1 text-xs text-zinc-500 underline-offset-2 outline-none hover:text-zinc-700 hover:underline dark:text-zinc-400 dark:hover:text-zinc-200"
        >
          {t('agents.viewWorkItem')}
        </Link>
      )}
    </div>
  )
}
function TaskTranscriptDrawer({ task, onClose }: { task: WorkspaceTaskRow; onClose: () => void }) {
  const { t, i18n } = useTranslation()
  const formatTime = (iso: string) =>
    new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  const agentNames = useAgentNameMap()
  const messages = useQuery({
    queryKey: ['task', task.id, 'messages'],
    queryFn: () => listTaskMessages(task.id),
  })
  return (
    <Dialog.Root open onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Popup className="fixed top-0 right-0 z-50 h-full w-full max-w-lg overflow-y-auto border-l border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
          <Dialog.Title className="mb-3 flex flex-wrap items-center gap-2 text-sm font-semibold">
            <TaskStatusBadge status={task.status} />
            <span>{agentDisplayName(agentNames, task.agent_id)}</span>
            <span className="text-xs font-normal text-zinc-400">{formatTime(task.created_at)}</span>
            <Dialog.Close className="ml-auto rounded-md px-2 py-1 text-xs text-zinc-400 hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-300">
              {t('common.close')}
            </Dialog.Close>
          </Dialog.Title>

          {task.work_item_id && (
            <Link
              to="/work-items/$id"
              params={{ id: task.work_item_id }}
              className="mb-3 block truncate text-xs text-zinc-500 underline-offset-2 outline-none hover:text-zinc-700 hover:underline dark:text-zinc-400"
            >
              {t('runLogs.workItemLink', {
                title: task.work_item_title || task.work_item_id,
              })}
            </Link>
          )}

          {(task.failure_reason || task.error) && (
            <div className="mb-3 rounded-md border border-red-200 bg-red-50 p-3 dark:border-red-900/50 dark:bg-red-950/40">
              {task.failure_reason && (
                <p className="font-mono text-xs font-medium text-red-700 dark:text-red-300">
                  {task.failure_reason}
                </p>
              )}
              {task.error && (
                <pre className="mt-1 font-mono text-xs break-all whitespace-pre-wrap text-red-600 dark:text-red-400">
                  {task.error}
                </pre>
              )}
            </div>
          )}

          {task.result && (
            <div className="mb-3 rounded-md border border-zinc-200 p-3 dark:border-zinc-800">
              <p className="mb-1 text-xs font-medium text-zinc-400">{t('runLogs.result')}</p>
              <pre className={cn('max-h-40 overflow-auto font-mono text-xs whitespace-pre-wrap')}>
                {task.result}
              </pre>
            </div>
          )}

          <p className="mb-2 text-xs font-medium text-zinc-400">{t('runLogs.transcript')}</p>
          {messages.isPending && (
            <p className="text-xs text-zinc-400">{t('runLogs.loadingMessages')}</p>
          )}
          {messages.isError && (
            <p className="text-xs text-red-600 dark:text-red-400">{t('runLogs.messagesFailed')}</p>
          )}
          {messages.data && messages.data.length === 0 && (
            <p className="text-xs text-zinc-400">{t('runLogs.noMessages')}</p>
          )}
          <TaskMessageList messages={messages.data ?? []} />
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

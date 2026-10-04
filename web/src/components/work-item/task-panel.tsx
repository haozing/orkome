import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, ChevronDown, Loader2, X } from 'lucide-react'
import {
  cancelWorkItemTask,
  listTaskMessages,
  listWorkItemTasks,
  settleTask,
  type Task,
} from '@/api/orchestration'
import { Button } from '@/components/ui/button'
import { PageEmpty, PageLoading } from '@/components/ui/states'
import { Markdown } from '@/components/markdown'
import { TaskMessageList } from '@/components/task-messages'
import {
  ACTIVE_TASK_STATUSES,
  TaskStatusBadge,
  agentDisplayName,
  useAgentNameMap,
} from '@/components/task-status-badge'
import { useWsScope } from '@/hooks/ws'
import { useTranslation } from 'react-i18next'
function formatTime(iso: string) {
  return new Date(iso).toLocaleString(document.documentElement.lang === 'zh' ? 'zh-CN' : 'en-US', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
export function TaskPanel({
  workItemId,
  expandedTaskId,
  onToggleTask,
}: {
  workItemId: string
  expandedTaskId: string | null
  onToggleTask: (taskId: string) => void
}) {
  const { t } = useTranslation()
  const tasks = useQuery({
    queryKey: ['work-item', workItemId, 'tasks'],
    queryFn: () => listWorkItemTasks(workItemId),
  })
  if (tasks.isPending) return <PageLoading label={t('work.loadingTasks')} className="pt-8" />
  if (tasks.isError) {
    return <PageEmpty title={t('work.tasksLoadFailed')} description={String(tasks.error.message)} />
  }
  if (!tasks.data.length) {
    return (
      <PageEmpty
        title={t('work.noTasksTitle')}
        description={t('work.noTasksDesc')}
        className="pt-8"
      />
    )
  }
  const ordered = [...tasks.data].reverse()
  return (
    <div className="space-y-3">
      {ordered.map((task) => (
        <TaskCard
          key={task.id}
          task={task}
          expanded={task.id === expandedTaskId}
          onToggle={() => onToggleTask(task.id)}
        />
      ))}
    </div>
  )
}
function TaskCard({
  task,
  expanded,
  onToggle,
}: {
  task: Task
  expanded: boolean
  onToggle: () => void
}) {
  const { t } = useTranslation()
  const agentNames = useAgentNameMap()
  return (
    <div className="rounded-lg border border-zinc-200 dark:border-zinc-800">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={expanded}
        className="flex w-full cursor-default items-center gap-3 px-3 py-2.5 text-left text-sm outline-none hover:bg-zinc-50 dark:hover:bg-zinc-900/50"
      >
        <TaskStatusBadge status={task.status} />
        <span className="font-mono text-xs text-zinc-400">
          {agentDisplayName(agentNames, task.agent_id)}
        </span>
        {task.attempt > 1 && (
          <span className="text-xs text-amber-600 dark:text-amber-400">
            {t('agents.retryAttempt', { attempt: task.attempt, max: task.max_attempts })}
          </span>
        )}
        <span className="ml-auto text-xs text-zinc-400">{formatTime(task.created_at)}</span>
        <ChevronDown
          className={
            'size-4 shrink-0 text-zinc-400 transition-transform ' + (expanded ? 'rotate-180' : '')
          }
          aria-hidden="true"
        />
      </button>
      {task.error && (
        <p className="border-t border-zinc-100 px-3 py-2 text-xs text-red-600 dark:border-zinc-800/60 dark:text-red-400">
          {task.error}
        </p>
      )}
      {expanded && <TaskDetail task={task} />}
    </div>
  )
}
function TaskDetail({ task }: { task: Task }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  useWsScope('task', task.id)
  const messages = useQuery({
    queryKey: ['task', task.id, 'messages'],
    queryFn: () => listTaskMessages(task.id),
  })
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['work-item', task.work_item_id, 'tasks'] })
    void queryClient.invalidateQueries({ queryKey: ['task', task.id, 'messages'] })
  }
  const settle = useMutation({
    mutationFn: (approved: boolean) => settleTask(task.id, approved),
    onSuccess: invalidate,
  })
  const cancel = useMutation({
    mutationFn: () => cancelWorkItemTask(task.work_item_id!, task.id),
    onSuccess: invalidate,
  })
  const docked = task.status === 'awaiting_input'
  const active = ACTIVE_TASK_STATUSES.has(task.status)
  return (
    <div className="border-t border-zinc-200 dark:border-zinc-800">
      {(docked || active) && (
        <div className="flex flex-wrap items-center gap-2 bg-zinc-50 px-3 py-2 dark:bg-zinc-900/50">
          {docked && (
            <>
              <span className="text-xs text-zinc-500">{t('work.awaitingYourDecision')}</span>
              <Button size="sm" disabled={settle.isPending} onClick={() => settle.mutate(true)}>
                <Check className="size-3.5" aria-hidden="true" />
                {t('work.approve')}
              </Button>
              <Button
                size="sm"
                variant="danger"
                disabled={settle.isPending}
                onClick={() => settle.mutate(false)}
              >
                <X className="size-3.5" aria-hidden="true" />
                {t('work.reject')}
              </Button>
            </>
          )}
          {active && !docked && (
            <Button
              size="sm"
              variant="outline"
              disabled={cancel.isPending}
              onClick={() => cancel.mutate()}
            >
              {t('work.cancelTask')}
            </Button>
          )}
          {settle.isPending && (
            <Loader2 className="size-3.5 animate-spin text-zinc-400" aria-hidden="true" />
          )}
        </div>
      )}

      {task.result && (
        <div className="border-t border-zinc-100 px-3 py-2 dark:border-zinc-800/60">
          <p className="mb-1 text-xs font-medium text-zinc-400">{t('work.resultLabel')}</p>
          <Markdown>{task.result}</Markdown>
        </div>
      )}

      <div className="px-3 py-2">
        <p className="mb-2 text-xs font-medium text-zinc-400">{t('work.runMessages')}</p>
        {messages.isPending && (
          <p className="text-xs text-zinc-400">{t('runLogs.loadingMessages')}</p>
        )}
        {messages.data && messages.data.length === 0 && (
          <p className="text-xs text-zinc-400">{t('runLogs.noMessages')}</p>
        )}
        <TaskMessageList messages={messages.data ?? []} />
      </div>
    </div>
  )
}

import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { membersQueryOptions } from '@/api/identity'
import { listAgents } from '@/api/orchestration'
import type { WorkItem } from '@/api/work'
import { Badge } from '@/components/ui/badge'
import { statusMetaOf, PRIORITY_META } from '@/components/work-item/meta'
import { useWorkspaceStore } from '@/stores/workspace'
import { cn } from '@/lib/utils'
const COL_NUMBER = 'w-10 shrink-0'
const COL_TITLE = 'min-w-0 flex-1'
const COL_PRIORITY = 'w-10 shrink-0 text-center'
const COL_ASSIGNEE = 'w-20 shrink-0 truncate'
const COL_STATUS = 'w-[4.5rem] shrink-0'
const COL_DATE = 'w-14 shrink-0'
const COL_TIME = 'w-[5.5rem] shrink-0 text-right'
function useStatusBadge() {
  const { t } = useTranslation()
  return (item: WorkItem) => {
    const meta = statusMetaOf(item.status)
    return <Badge variant={meta.tone}>{t(meta.labelKey)}</Badge>
  }
}
function useFormatTime() {
  const { i18n } = useTranslation()
  return (iso: string) =>
    new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
}
function formatDate(iso?: string) {
  if (!iso) return null
  return iso.slice(5).replace('-', '/')
}
function useAssigneeLabel() {
  const { t } = useTranslation()
  const workspaceId = useWorkspaceStore((s) => s.currentId) ?? ''
  const members = useQuery(membersQueryOptions(workspaceId))
  const agents = useQuery({ queryKey: ['agents'], queryFn: listAgents, staleTime: 60000 })
  return (item: WorkItem): string => {
    if (item.assignee_type === 'user' && item.assignee_id) {
      const m = members.data?.find((x) => x.user_id === item.assignee_id)
      return m ? m.display_name || m.email : item.assignee_id.slice(0, 8) + '…'
    }
    if (item.assignee_type === 'agent' && item.assignee_id) {
      return (
        agents.data?.find((a) => a.id === item.assignee_id)?.name ??
        `Agent ${item.assignee_id.slice(0, 8)}…`
      )
    }
    return t('work.unassigned')
  }
}
export function WorkItemTableHeader({ className }: { className?: string }) {
  const { t } = useTranslation()
  return (
    <div
      className={cn('flex items-center gap-3 px-3 text-xs text-zinc-400', className)}
      aria-hidden="true"
    >
      <span className={COL_NUMBER}>{t('work.colNumber')}</span>
      <span className={COL_TITLE}>{t('work.colTitle')}</span>
      <span className={COL_PRIORITY}>{t('work.colPriority')}</span>
      <span className={COL_ASSIGNEE}>{t('work.colAssignee')}</span>
      <span className={COL_STATUS}>{t('work.colStatus')}</span>
      <span className={COL_DATE}>{t('work.colStart')}</span>
      <span className={COL_DATE}>{t('work.colDue')}</span>
      <span className={COL_TIME}>{t('work.colActivity')}</span>
    </div>
  )
}
export function WorkItemRow({ item }: { item: WorkItem }) {
  const { t } = useTranslation()
  const assigneeLabel = useAssigneeLabel()
  const statusBadge = useStatusBadge()
  const formatTime = useFormatTime()
  const prio = PRIORITY_META[item.priority] ?? PRIORITY_META[0]!
  const start = formatDate(item.start_date)
  const due = formatDate(item.due_date)
  return (
    <li>
      <Link
        to="/work-items/$id"
        params={{ id: item.id }}
        search={{}}
        className="flex items-center gap-3 px-3 py-2.5 hover:bg-zinc-50 dark:hover:bg-zinc-900"
      >
        <span className={cn(COL_NUMBER, 'font-mono text-xs text-zinc-400')}>#{item.number}</span>
        <span className={cn(COL_TITLE, 'truncate text-sm')}>{item.title}</span>
        <span className={cn(COL_PRIORITY, 'text-xs', prio.className)}>{t(prio.labelKey)}</span>
        <span className={cn(COL_ASSIGNEE, 'text-xs text-zinc-500 dark:text-zinc-400')}>
          {assigneeLabel(item)}
        </span>
        <span className={COL_STATUS}>{statusBadge(item)}</span>
        <span
          className={cn(
            COL_DATE,
            'text-xs',
            start ? 'text-zinc-500 dark:text-zinc-400' : 'text-zinc-300 dark:text-zinc-600',
          )}
        >
          {start ?? '—'}
        </span>
        <span
          className={cn(
            COL_DATE,
            'text-xs',
            due ? 'text-zinc-500 dark:text-zinc-400' : 'text-zinc-300 dark:text-zinc-600',
          )}
        >
          {due ?? '—'}
        </span>
        <span className={cn(COL_TIME, 'text-xs text-zinc-400')}>
          {formatTime(item.last_activity_at)}
        </span>
      </Link>
    </li>
  )
}
export function WorkItemList({ items }: { items: WorkItem[] }) {
  return (
    <div className="rounded-lg border border-zinc-200 dark:border-zinc-800">
      <div className="py-2">
        <WorkItemTableHeader />
      </div>
      <ul className="divide-y divide-zinc-200 border-t border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
        {items.map((item) => (
          <WorkItemRow key={item.id} item={item} />
        ))}
      </ul>
    </div>
  )
}

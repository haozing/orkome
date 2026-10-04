import { useQuery } from '@tanstack/react-query'
import { membersQueryOptions } from '@/api/identity'
import type { WorkItem } from '@/api/work'
import {
  AssigneeRow,
  DateRow,
  ParentRow,
  PriorityRow,
  StatusRow,
} from '@/components/work-item/property-rows'
import { useWorkspaceStore } from '@/stores/workspace'
import { useTranslation } from 'react-i18next'
function formatDateTime(iso: string) {
  return new Date(iso).toLocaleString(document.documentElement.lang === 'zh' ? 'zh-CN' : 'en-US', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
export function InfoPanel({ item }: { item: WorkItem }) {
  const { t } = useTranslation()
  const workspaceId = useWorkspaceStore((s) => s.currentId) ?? ''
  const members = useQuery(membersQueryOptions(workspaceId))
  const resolveUser = (userId?: string) => {
    if (!userId) return undefined
    const m = members.data?.find((x) => x.user_id === userId)
    return m ? m.display_name || m.email : userId.slice(0, 8) + '…'
  }
  const creatorLabel =
    item.creator_type === 'user'
      ? (resolveUser(item.creator_id) ?? t('work.unknown'))
      : `Agent ${item.creator_id.slice(0, 8)}…`
  return (
    <aside className="w-[350px] shrink-0 space-y-1 border-l border-zinc-200 pl-5 dark:border-zinc-800">
      <h2 className="px-2 pb-1 text-xs font-medium text-zinc-400">{t('work.properties')}</h2>
      <StatusRow item={item} />
      <PriorityRow item={item} />
      <AssigneeRow item={item} />
      <DateRow item={item} field="start_date" label={t('work.startDate')} />
      <DateRow item={item} field="due_date" label={t('work.dueDate')} />
      <ParentRow item={item} />

      <InfoRow label={t('work.projectRow')}>
        <span className="text-zinc-400">
          {item.project_id ? item.project_id.slice(0, 8) + '…' : t('work.noProject')}
        </span>
      </InfoRow>

      <h2 className="px-2 pt-4 pb-1 text-xs font-medium text-zinc-400">{t('work.details')}</h2>
      <InfoRow label={t('work.creator')}>{creatorLabel}</InfoRow>
      <InfoRow label={t('work.createdAt')}>{formatDateTime(item.created_at)}</InfoRow>
      <InfoRow label={t('work.updatedAt')}>{formatDateTime(item.updated_at)}</InfoRow>
    </aside>
  )
}
function InfoRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between rounded-md px-2 py-1.5 text-sm">
      <span className="text-zinc-500 dark:text-zinc-400">{label}</span>
      <span className="text-right">{children}</span>
    </div>
  )
}

import { useQuery } from '@tanstack/react-query'
import { membersQueryOptions } from '@/api/identity'
import type { WorkItem } from '@/api/work'
import {
  AssigneeRow,
  DateRow,
  ParentRow,
  PriorityRow,
  ProjectRow,
  StatusRow,
} from '@/components/work-item/property-rows'
import { WorkflowPanel } from '@/components/workflow/workflow-panel'
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
export function InfoPanel({ item, workItemId, projectId }: { item: WorkItem; workItemId?: string; projectId?: string }) {
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
    <aside className="w-[300px] shrink-0 space-y-1 border-l border-zinc-200 pl-5 dark:border-zinc-800">
      {/* 流程光标卡置顶（用户裁决：单侧栏，流程最上，属性/详情随后）。
          流程卡只在有 state 时渲染（空态也占位会推挤属性区），分隔线与属性区隔开。 */}
      {workItemId && <WorkflowPanel workItemId={workItemId} projectId={projectId ?? ''} />}
      <div className="mt-1 border-t border-zinc-200 pt-3 dark:border-zinc-800">
        <h2 className="px-2 pb-1 text-xs font-medium text-zinc-400">{t('work.properties')}</h2>
      <ProjectRow item={item} />
      <StatusRow item={item} />
      <PriorityRow item={item} />
      <AssigneeRow item={item} />
      <DateRow item={item} field="start_date" label={t('work.startDate')} />
      <DateRow item={item} field="due_date" label={t('work.dueDate')} />
      <ParentRow item={item} />

      <h2 className="px-2 pt-4 pb-1 text-xs font-medium text-zinc-400">{t('work.details')}</h2>
      <InfoRow label={t('work.creator')}>{creatorLabel}</InfoRow>
      <InfoRow label={t('work.createdAt')}>{formatDateTime(item.created_at)}</InfoRow>
      <InfoRow label={t('work.updatedAt')}>{formatDateTime(item.updated_at)}</InfoRow>
      </div>
    </aside>
  )
}
function InfoRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-2 rounded-md px-2 py-1.5 text-sm">
      <span className="shrink-0 text-zinc-500 dark:text-zinc-400">{label}</span>
      <span className="min-w-0 truncate text-right">{children}</span>
    </div>
  )
}

import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { listChildWorkItems, type WorkItem } from '@/api/work'
import { PageEmpty, PageLoading } from '@/components/ui/states'
import { statusMetaOf } from '@/components/work-item/meta'
import { BadgeDot } from '@/components/work-item/property-rows'
export function SubWorkItemsList({ workItemId }: { workItemId: string }) {
  const { t, i18n } = useTranslation()
  const formatTime = (iso: string) =>
    new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  const children = useQuery({
    queryKey: ['work-item', workItemId, 'children'],
    queryFn: () => listChildWorkItems(workItemId),
  })
  if (children.isPending) return <PageLoading label={t('work.loadingChildren')} className="pt-8" />
  if (!children.data?.length) {
    return (
      <PageEmpty
        title={t('work.noChildrenTitle')}
        description={t('work.noChildrenDesc')}
        className="pt-8"
      />
    )
  }
  return (
    <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
      {children.data.map((item: WorkItem) => {
        const meta = statusMetaOf(item.status)
        return (
          <li key={item.id}>
            <Link
              to="/work-items/$id"
              params={{ id: item.id }}
              search={{}}
              className="flex items-center gap-3 px-3 py-2.5 hover:bg-zinc-50 dark:hover:bg-zinc-900"
            >
              <span className="w-12 shrink-0 font-mono text-xs text-zinc-400">#{item.number}</span>
              <span className="min-w-0 flex-1 truncate text-sm">{item.title}</span>
              <span className="flex shrink-0 items-center gap-1 text-xs text-zinc-500 dark:text-zinc-400">
                <BadgeDot status={item.status} />
                {t(meta.labelKey)}
              </span>
              <span className="w-24 shrink-0 text-right text-xs text-zinc-400">
                {formatTime(item.last_activity_at)}
              </span>
            </Link>
          </li>
        )
      })}
    </ul>
  )
}

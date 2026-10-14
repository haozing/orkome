import { useQuery } from '@tanstack/react-query'
import { MessageSquare, Activity } from 'lucide-react'
import { listTimeline } from '@/api/work'
import { PageLoading } from '@/components/ui/states'
import { useAgentNameMap } from '@/components/task-status-badge'
import { useTranslation } from 'react-i18next'
function formatTime(iso: string) {
  return new Date(iso).toLocaleString(document.documentElement.lang === 'zh' ? 'zh-CN' : 'en-US', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
export function TimelineList({ workItemId }: { workItemId: string }) {
  const { t } = useTranslation()
  // 时间线 actor 可能是助理代理行（assistant_proxy，"张三(AI 代)"）——名字映射需含代理行。
  const agentNames = useAgentNameMap()
  const timeline = useQuery({
    queryKey: ['work-item', workItemId, 'timeline'],
    queryFn: () => listTimeline(workItemId),
  })
  if (timeline.isPending) return <PageLoading label={t('timeline.loading')} className="pt-8" />
  if (!timeline.data?.length) {
    return <p className="pt-4 text-center text-sm text-zinc-400">{t('timeline.empty')}</p>
  }
  return (
    <ol className="space-y-2 border-l border-zinc-200 pl-4 dark:border-zinc-800">
      {timeline.data.map((entry) => (
        <li key={entry.id} className="relative">
          <span
            className="absolute top-1.5 -left-[21px] flex size-2.5 items-center justify-center rounded-full bg-zinc-300 dark:bg-zinc-600"
            aria-hidden="true"
          />
          <p className="flex flex-wrap items-center gap-2 text-xs text-zinc-400">
            {entry.kind === 'comment' ? (
              <MessageSquare className="size-3" aria-hidden="true" />
            ) : (
              <Activity className="size-3" aria-hidden="true" />
            )}
            <span className="font-medium text-zinc-600 dark:text-zinc-300">
              {/* actor 名 fallback 链：user→「用户」；agent→名字映射（含代理行
                  可读名）→「Agent」+ID 前 6 位（映射缺失时的兜底）。 */}
              {entry.actor_type === 'user'
                ? t('comment.user')
                : entry.actor_type === 'agent'
                  ? `Agent ${agentNames.get(entry.actor_id ?? '') ?? `${(entry.actor_id ?? '').slice(0, 6)}…`}`
                  : entry.actor_type || t('timeline.system')}
            </span>
            <span>{formatTime(entry.created_at)}</span>
          </p>
          {entry.kind === 'comment' ? (
            <p className="mt-0.5 line-clamp-2 text-sm text-zinc-700 dark:text-zinc-300">
              {entry.content}
            </p>
          ) : (
            <p className="mt-0.5 line-clamp-2 text-sm text-zinc-700 dark:text-zinc-300">
              {entry.content || (entry.payload ? JSON.stringify(entry.payload) : '')}
            </p>
          )}
        </li>
      ))}
    </ol>
  )
}

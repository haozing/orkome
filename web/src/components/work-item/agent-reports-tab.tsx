import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { PageEmpty, PageLoading } from '@/components/ui/states'
import { Markdown } from '@/components/markdown'
import { formatTime } from '@/components/work-item/comment-thread'
import { useAgentNameMap } from '@/components/task-status-badge'
import { listComments } from '@/api/work'

// AgentReportsTab 「汇报」tab（只读）：只呈现 agent 的汇报评论（author_type=agent），
// 按时间正序。完整的评论区（编辑器 + 人与 agent 的全部交流）仍在详情 tab 底部——
// 两个面各司其职：汇报是产出流，评论是交流流。
export function AgentReportsTab({ workItemId }: { workItemId: string }) {
  const { t } = useTranslation()
  // 汇报作者可能是助理代理行（assistant_proxy，"张三(AI 代)"）——名字映射需含代理行。
  const agentNames = useAgentNameMap()
  const comments = useQuery({
    queryKey: ['comments', workItemId],
    queryFn: () => listComments(workItemId),
  })

  const reports = useMemo(() => {
    const items = comments.data ?? []
    return items
      .filter((c) => c.author_type === 'agent' && !c.deleted)
      .sort((a, b) => a.created_at.localeCompare(b.created_at))
  }, [comments.data])

  if (comments.isPending) return <PageLoading label={t('reports.loading')} className="pt-8" />

  if (reports.length === 0) {
    return (
      <PageEmpty
        title={t('reports.emptyTitle')}
        description={t('reports.emptyDesc')}
        className="pt-4"
      />
    )
  }

  return (
    <div className="space-y-3">
      {reports.map((c) => (
        <div key={c.id} className="rounded-md border border-zinc-200 px-3 py-2 dark:border-zinc-800">
          <p className="mb-1 flex items-center gap-2 text-xs text-zinc-400">
            <span className="font-medium text-zinc-600 dark:text-zinc-300">
              {t('reports.byAgent')}
              {/* 作者名 fallback 链：名字映射（含代理行可读名）→ ID 前 6 位（后端
                  W1 给 proxy 行落可读 name，正常路径不会落到 ID 前缀）。 */}
              {c.author_id ? ` ${agentNames.get(c.author_id) ?? `${c.author_id.slice(0, 6)}…`}` : ''}
            </span>
            <span>{formatTime(c.created_at)}</span>
          </p>
          <div className="max-w-none space-y-2 text-sm leading-relaxed">
            <Markdown>{c.content}</Markdown>
          </div>
        </div>
      ))}
    </div>
  )
}

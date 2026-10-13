import { useQueryClient } from '@tanstack/react-query'
import { Sparkles } from 'lucide-react'
import { Suspense, lazy, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Markdown } from '@/components/markdown'
import { Button } from '@/components/ui/button'
import { useFieldUpdate } from '@/components/work-item/use-field-update'
import type { WorkItem } from '@/api/work'

// 与评论区同款 markdown 编辑器（RichEditor，懒加载同 comment-thread）。
const RichEditor = lazy(() =>
  import('@/components/editor/rich-editor').then((m) => ({ default: m.RichEditor })),
)

// OutcomeBlock 工单产出（"需求→产出"一等公民，迁移 1039）。
// 三态：有 outcome → Markdown 渲染 + 溯源底注 + 可人工编辑；
// 工单已完成但无 outcome → 空态引导；其余（未完成且无产出）不渲染。
export function OutcomeBlock({ item }: { item: WorkItem }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const field = useFieldUpdate(item)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState('')

  const completed = item.status_category === 'done' || item.status_category === 'closed'
  if (!item.outcome && !completed) return null

  const startEdit = () => {
    setDraft(item.outcome?.summary ?? '')
    setEditing(true)
  }
  const save = () => {
    field.mutate({ outcome: { summary: draft, task_id: item.outcome?.task_id, completed_at: item.outcome?.completed_at ?? new Date().toISOString() } }, {
      onSuccess: () => {
        setEditing(false)
        qc.invalidateQueries({ queryKey: ['work-item', item.id] })
      },
    })
  }

  return (
    <div className="mt-4 rounded-md border border-zinc-200 p-3 dark:border-zinc-800">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="flex items-center gap-1.5 text-sm font-semibold">
          <Sparkles className="size-3.5 text-amber-500" aria-hidden="true" />
          {t('outcome.title')}
        </h3>
        {item.outcome && !editing && (
          <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={startEdit}>
            {t('outcome.edit')}
          </Button>
        )}
      </div>

      {editing ? (
        <div className="space-y-2">
          <div className="rounded-md border border-zinc-300 bg-white px-3 py-2 outline-none dark:border-zinc-700 dark:bg-zinc-900">
            <Suspense
              fallback={
                <div className="py-4 text-sm text-zinc-400">{t('work.editorLoading')}</div>
              }
            >
              <RichEditor
                content={draft}
                onChange={setDraft}
                ariaLabel={t('outcome.title')}
                placeholder={t('outcome.placeholder')}
                minRows={6}
              />
            </Suspense>
          </div>
          <div className="flex gap-2">
            <Button size="sm" onClick={save} disabled={field.isPending}>
              {t('outcome.save')}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>
              {t('outcome.cancel')}
            </Button>
          </div>
        </div>
      ) : item.outcome ? (
        <>
          <div className="max-w-none space-y-2 text-sm leading-relaxed">
            <Markdown>{item.outcome.summary}</Markdown>
          </div>
          <p className="mt-2 text-xs text-zinc-400">
            {item.outcome.task_id && (
              <span>
                {t('outcome.fromTask', { task: item.outcome.task_id.slice(0, 8) })} ·{' '}
              </span>
            )}
            {t('outcome.latestWins')}
          </p>
        </>
      ) : (
        <p className="text-sm text-zinc-400">{t('outcome.emptyGuide')}</p>
      )}
    </div>
  )
}

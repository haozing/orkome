import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Send } from 'lucide-react'
import { Suspense, lazy, useEffect, useRef, useState } from 'react'
import {
  createComment,
  listComments,
  previewCommentTrigger,
  type Comment,
  type TriggerPreview,
} from '@/api/work'
import { Button } from '@/components/ui/button'
import { PageEmpty, PageLoading } from '@/components/ui/states'
import { Markdown } from '@/components/markdown'
import type { MentionItem } from '@/components/editor/mention'
import { listAgents } from '@/api/orchestration'
import { membersQueryOptions } from '@/api/identity'
import { useWorkspaceStore } from '@/stores/workspace'
import { useTranslation } from 'react-i18next'
const RichEditor = lazy(() =>
  import('@/components/editor/rich-editor').then((m) => ({ default: m.RichEditor })),
)
const PREVIEW_DEBOUNCE_MS = 500
export function formatTime(iso: string) {
  return new Date(iso).toLocaleString(document.documentElement.lang === 'zh' ? 'zh-CN' : 'en-US', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
export function CommentThread({ workItemId }: { workItemId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState('')
  const [preview, setPreview] = useState<TriggerPreview | null>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const latest = useRef({ workItemId, draft })
  useEffect(() => {
    latest.current = { workItemId, draft }
  })
  const workspaceId = useWorkspaceStore((s) => s.currentId) ?? ''
  const members = useQuery(membersQueryOptions(workspaceId))
  const agents = useQuery({ queryKey: ['agents'], queryFn: () => listAgents(), staleTime: 60000 })
  const mentionItems = (): MentionItem[] => [
    ...(members.data ?? []).map((m) => ({
      id: m.user_id,
      label: m.display_name || m.email,
      type: 'user' as const,
    })),
    ...(agents.data ?? [])
      .filter((a) => !a.archived)
      .map((a) => ({ id: a.id, label: a.name, type: 'agent' as const })),
  ]
  const agentName = (agentId: string) =>
    agents.data?.find((a) => a.id === agentId)?.name ?? agentId.slice(0, 8) + '…'
  const comments = useQuery({
    queryKey: ['work-item', workItemId, 'comments'],
    queryFn: () => listComments(workItemId),
  })
  const previewSeq = useRef(0)
  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current)
    const seq = ++previewSeq.current
    debounceRef.current = setTimeout(async () => {
      if (seq !== previewSeq.current) return
      if (!draft.trim()) {
        setPreview(null)
        return
      }
      try {
        const res = await previewCommentTrigger(workItemId, draft)
        if (seq === previewSeq.current) setPreview(res)
      } catch {
        if (seq === previewSeq.current) setPreview(null)
      }
    }, PREVIEW_DEBOUNCE_MS)
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [draft, workItemId])
  const post = useMutation({
    mutationFn: () => createComment(workItemId, draft.trim()),
    onSuccess: () => {
      setDraft('')
      setPreview(null)
      void queryClient.invalidateQueries({ queryKey: ['work-item', workItemId] })
    },
  })
  const submit = () => {
    if (!draft.trim() || post.isPending) return
    post.mutate()
  }
  const items = comments.data ?? []
  const repliesOf = (id: string) => items.filter((c) => c.parent_id === id && !c.deleted)
  // 评论流纯时间序（用户裁决）：汇报的家在「汇报」tab 与 Outcome 块，
  // 评论区不再置顶提升——避免同一内容两处突出。
  const roots = items.filter((c) => !c.parent_id && !c.deleted)
  const previewTriggers = preview?.result === 'would_trigger' ? (preview.triggers ?? []) : []
  const suppressions = preview?.result === 'blocked' ? (preview.suppressions ?? []) : []
  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <div className="rounded-md border border-zinc-300 bg-white px-3 py-2 outline-none dark:border-zinc-700 dark:bg-zinc-900">
          <Suspense
            fallback={<div className="py-4 text-sm text-zinc-400">{t('work.editorLoading')}</div>}
          >
            <RichEditor
              content={draft}
              onChange={setDraft}
              ariaLabel={t('comment.aria')}
              placeholder={t('comment.placeholder')}
              minRows={3}
              mentionItems={mentionItems}
            />
          </Suspense>
        </div>

        {previewTriggers.length > 0 && (
          <p className="flex flex-wrap items-center gap-1 text-xs text-zinc-500 dark:text-zinc-400">
            {t('comment.willWake')}
            {previewTriggers.map((t) => (
              <span
                key={t.agent_id}
                className="rounded bg-violet-100 px-1.5 py-0.5 font-medium text-violet-700 dark:bg-violet-950 dark:text-violet-300"
              >
                @{agentName(t.agent_id)}
              </span>
            ))}
          </p>
        )}
        {preview?.result === 'blocked' && suppressions.length > 0 && (
          <p className="text-xs text-zinc-400">
            {t('comment.suppressed')}{' '}
            {suppressions.map((s) => `@${agentName(s.agent_id)}`).join(', ')} (
            {suppressions[0]?.reason})
          </p>
        )}

        {post.error && (
          <p role="alert" className="text-xs text-red-600 dark:text-red-400">
            {post.error instanceof Error ? post.error.message : t('comment.postFailed')}
          </p>
        )}
        <div className="flex justify-end">
          <Button size="sm" disabled={!draft.trim() || post.isPending} onClick={submit}>
            <Send className="size-3.5" aria-hidden="true" />
            {post.isPending ? t('comment.posting') : t('comment.post')}
          </Button>
        </div>
      </div>

      {comments.isPending && <PageLoading label={t('comment.loading')} className="pt-8" />}
      {comments.isSuccess && items.length === 0 && (
        <PageEmpty
          title={t('comment.emptyTitle')}
          description={t('comment.emptyDesc')}
          className="pt-4"
        />
      )}

      <div className="space-y-2">
        {roots.map((c) => (
          <div key={c.id} className="space-y-2">
            <CommentRow comment={c} />
            {repliesOf(c.id).length > 0 && (
              <div className="ml-6 space-y-2 border-l-2 border-zinc-100 pl-3 dark:border-zinc-800">
                {repliesOf(c.id).map((r) => (
                  <CommentRow key={r.id} comment={r} />
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
function CommentRow({ comment }: { comment: Comment }) {
  const { t } = useTranslation()
  const [expanded, setExpanded] = useState(false)
  const bodyRef = useRef<HTMLDivElement>(null)
  const [overflows, setOverflows] = useState(false)
  // 长评论默认收起到 ~5 行（用户裁决），点击展开；
  // 短评论不渲染折叠控件。挂载/内容变化后按实际高度判定是否溢出。
  useEffect(() => {
    const el = bodyRef.current
    if (el) setOverflows(el.scrollHeight > el.clientHeight + 4)
  }, [comment.content])
  const author =
    comment.author_type === 'user'
      ? t('comment.user')
      : comment.author_type === 'agent'
        ? 'Agent'
        : comment.author_type
  return (
    <div className="rounded-md border border-zinc-200 px-3 py-2 dark:border-zinc-800">
      <p className="mb-1 flex items-center gap-2 text-xs text-zinc-400">
        <span className="font-medium text-zinc-600 dark:text-zinc-300">
          {author}
          {comment.author_id ? ` ${comment.author_id.slice(0, 6)}…` : ''}
        </span>
        <span>{formatTime(comment.created_at)}</span>
        {comment.resolved && (
          <span className="text-emerald-600 dark:text-emerald-400">{t('comment.resolved')}</span>
        )}
      </p>
      <div
        ref={bodyRef}
        className={
          expanded
            ? 'max-w-none space-y-2 text-sm leading-relaxed'
            : 'max-h-24 overflow-hidden'
        }
      >
        <Markdown>{comment.content}</Markdown>
      </div>
      {overflows && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="mt-1 text-xs text-zinc-500 hover:text-zinc-700 dark:text-zinc-400 dark:hover:text-zinc-200"
        >
          {expanded ? t('comment.collapse') : t('comment.expand')}
        </button>
      )}
    </div>
  )
}

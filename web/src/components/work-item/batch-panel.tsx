import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, Loader2, Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  cancelBatch,
  createBatch,
  getBatch,
  listBatchItems,
  listBatches,
  pauseBatch,
  resumeBatch,
  retryBatchFailed,
  type Batch,
} from '@/api/batch'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { cn } from '@/lib/utils'

// 工单详情「批量」tab 面板：批次列表 + 创建对话框 + 批次卡片（四态计数 /
// 状态徽标 / 展开条目 / 条件操作按钮）。数据源见 web/src/api/batch.ts，
// 后端契约 internal/orchestration/adapters/http/handlers_batch.go。
//
// 轮询策略：存在非终态批次、或某批次被展开时，对应查询 refetchInterval 5000ms，
// 全部终态且未展开则不轮询（React Query）。
//
// i18n：文案统一走 batchPanel.* 键（translation 命名空间下，键清单见实现报告）；
// 未知 status（后端新增枚举时）经 statusText 回退显示原始字符串。

/** 批次终态（domain/batch.go BatchTerminal）：终态不再轮询，操作仅剩重试。 */
const BATCH_TERMINAL = new Set(['completed', 'failed', 'cancelled'])

/** 批次状态 → Badge variant（badge.tsx 的变体目录）。 */
const BATCH_STATUS_VARIANT: Record<string, 'secondary' | 'default' | 'warning' | 'success' | 'danger' | 'neutral'> = {
  pending: 'secondary',
  running: 'default',
  paused: 'warning',
  completed: 'success',
  failed: 'danger',
  cancelled: 'neutral',
}

/** 条目状态 → Badge variant。 */
const ITEM_STATUS_VARIANT: Record<string, 'secondary' | 'default' | 'warning' | 'success' | 'danger' | 'neutral'> = {
  pending: 'secondary',
  claimed: 'warning',
  processing: 'default',
  succeeded: 'success',
  failed: 'danger',
  skipped: 'neutral',
}

/**
 * kind 目录：v0 仅 llm_comment 一项；后续 kind 直接追加数组项，
 * 下拉与卡片 kind 显示自动跟随。
 */
const KIND_OPTIONS = [{ value: 'llm_comment', labelKey: 'batchPanel.kind.llmComment' }] as const

const SELECT_CLASS =
  'h-8 rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900'

const TEXTAREA_CLASS =
  'w-full rounded-md border border-zinc-300 bg-white p-2 font-mono text-xs outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900'

type BatchActionKind = 'pause' | 'resume' | 'cancel' | 'retry'

export function BatchPanel({ workItemId }: { workItemId: string }) {
  const { t } = useTranslation()
  const [createOpen, setCreateOpen] = useState(false)
  const batches = useQuery({
    queryKey: ['work-item-batches', workItemId],
    queryFn: () => listBatches(workItemId),
    // 存在非终态批次时 5s 轮询，全部终态则停止
    refetchInterval: (query) =>
      query.state.data?.some((b) => !BATCH_TERMINAL.has(b.status)) ? 5000 : false,
  })

  if (batches.isPending) return <PageLoading className="pt-8" />
  if (batches.isError) {
    return (
      <PageError
        title={t('batchPanel.loadFailed')}
        message={batches.error instanceof Error ? batches.error.message : String(batches.error)}
        onRetry={() => void batches.refetch()}
        retryLabel={t('common.retry')}
        className="pt-8"
      />
    )
  }
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">{t('batchPanel.title')}</h2>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus aria-hidden="true" />
          {t('batchPanel.create')}
        </Button>
      </div>
      {createOpen && (
        <CreateBatchDialog workItemId={workItemId} onOpenChange={setCreateOpen} />
      )}
      {batches.data.length === 0 ? (
        <PageEmpty
          title={t('batchPanel.emptyTitle')}
          description={t('batchPanel.emptyDesc')}
          className="pt-8"
          action={
            <Button size="sm" variant="outline" onClick={() => setCreateOpen(true)}>
              <Plus aria-hidden="true" />
              {t('batchPanel.create')}
            </Button>
          }
        />
      ) : (
        <ul className="space-y-2">
          {batches.data.map((b) => (
            <BatchCard key={b.id} batch={b} workItemId={workItemId} />
          ))}
        </ul>
      )}
    </div>
  )
}

/** 创建批次对话框：kind 下拉 + payloads（每行一个 JSON 对象）+ 并发数。 */
function CreateBatchDialog({
  workItemId,
  onOpenChange,
}: {
  workItemId: string
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [kind, setKind] = useState<string>(KIND_OPTIONS[0].value)
  const [payloadsText, setPayloadsText] = useState(
    '{"seed":"主题一"}\n{"seed":"主题二"}\n{"seed":"主题三"}',
  )
  const [concurrency, setConcurrency] = useState('3')
  const [badLines, setBadLines] = useState<number[]>([])

  const close = () => onOpenChange(false)

  const create = useMutation({
    mutationFn: (input: Parameters<typeof createBatch>[1]) => createBatch(workItemId, input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['work-item-batches', workItemId] })
      close()
    },
  })

  // 逐行 parse；空行跳过；失败行号收集用于标红提示
  const parsePayloads = (): Record<string, unknown>[] | null => {
    const bad: number[] = []
    const payloads: Record<string, unknown>[] = []
    payloadsText.split('\n').forEach((line, i) => {
      const s = line.trim()
      if (!s) return
      try {
        const obj = JSON.parse(s) as unknown
        if (obj === null || typeof obj !== 'object' || Array.isArray(obj)) throw new Error('not an object')
        payloads.push(obj as Record<string, unknown>)
      } catch {
        bad.push(i + 1)
      }
    })
    setBadLines(bad)
    return bad.length > 0 || payloads.length === 0 ? null : payloads
  }

  const submit = () => {
    if (create.isPending) return
    const payloads = parsePayloads()
    if (!payloads) return
    const n = Number(concurrency)
    create.mutate({
      kind,
      payloads,
      concurrency: Number.isFinite(n) && n >= 1 ? Math.min(Math.trunc(n), 64) : 3,
    })
  }

  return (
    <Dialog open onOpenChange={(o) => (o ? undefined : close())}>
      <DialogContent className="flex max-h-[85dvh] flex-col gap-3 overflow-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('batchPanel.createTitle')}</DialogTitle>
        </DialogHeader>

        <label className="flex items-center gap-2 text-sm">
          <span className="shrink-0">{t('batchPanel.kindLabel')}</span>
          <select
            value={kind}
            aria-label={t('batchPanel.kindLabel')}
            onChange={(e) => setKind(e.target.value)}
            className={SELECT_CLASS}
          >
            {KIND_OPTIONS.map((k) => (
              <option key={k.value} value={k.value}>
                {t(k.labelKey)}
              </option>
            ))}
          </select>
        </label>

        <div className="space-y-1">
          <label htmlFor="batch-payloads" className="block text-sm">
            {t('batchPanel.payloadsLabel')}
          </label>
          <textarea
            id="batch-payloads"
            rows={5}
            value={payloadsText}
            onChange={(e) => {
              setPayloadsText(e.target.value)
              if (badLines.length > 0) setBadLines([])
            }}
            placeholder={t('batchPanel.payloadsPlaceholder')}
            aria-invalid={badLines.length > 0}
            className={cn(TEXTAREA_CLASS, badLines.length > 0 && 'border-red-500 dark:border-red-500')}
          />
          {badLines.length > 0 && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {t('batchPanel.payloadsInvalid', { lines: badLines.join(', ') })}
            </p>
          )}
        </div>

        <label className="flex items-center gap-2 text-sm">
          <span className="shrink-0">{t('batchPanel.concurrencyLabel')}</span>
          <Input
            type="number"
            min={1}
            max={64}
            value={concurrency}
            aria-label={t('batchPanel.concurrencyLabel')}
            onChange={(e) => setConcurrency(e.target.value)}
            className="h-8 w-20"
          />
        </label>

        {create.error && (
          <p role="alert" className="text-xs text-red-600 dark:text-red-400">
            {create.error instanceof Error
              ? create.error.message
              : t('batchPanel.createFailed')}
          </p>
        )}

        <DialogFooter>
          <Button size="sm" onClick={submit} disabled={create.isPending}>
            {create.isPending ? t('batchPanel.createSubmitting') : t('common.create')}
          </Button>
          <Button variant="ghost" size="sm" onClick={close} disabled={create.isPending}>
            {t('common.cancel')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** 批次卡片：头部（toggle 展开）+ 四态计数 + 状态徽标 + 条件操作按钮。 */
function BatchCard({ batch, workItemId }: { batch: Batch; workItemId: string }) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const [expanded, setExpanded] = useState(false)
  const [retryNote, setRetryNote] = useState<number | null>(null)

  // 未知状态（后端新增枚举）回退显示原始字符串
  const statusText = (s: string) => {
    const key = `batchPanel.status.${s}`
    return i18n.exists(key) ? t(key) : s
  }
  const kindText = (k: string) => {
    const opt = KIND_OPTIONS.find((o) => o.value === k)
    return opt ? t(opt.labelKey) : k
  }
  const formatTime = (iso: string) =>
    new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US')

  // 详情（四态计数）：批次非终态时 5s 轮询
  const detail = useQuery({
    queryKey: ['batch-detail', batch.id],
    queryFn: () => getBatch(batch.id),
    refetchInterval: (query) =>
      query.state.data && !BATCH_TERMINAL.has(query.state.data.batch.status) ? 5000 : false,
  })

  const action = useMutation({
    mutationFn: async (kind: BatchActionKind) => {
      if (kind === 'pause') return pauseBatch(batch.id)
      if (kind === 'resume') return resumeBatch(batch.id)
      if (kind === 'cancel') return cancelBatch(batch.id)
      return retryBatchFailed(batch.id)
    },
    onSuccess: (data, kind) => {
      setRetryNote(null)
      void queryClient.invalidateQueries({ queryKey: ['batch-detail', batch.id] })
      void queryClient.invalidateQueries({ queryKey: ['batch-items', batch.id] })
      void queryClient.invalidateQueries({ queryKey: ['work-item-batches', workItemId] })
      if (kind === 'retry' && 'requeued' in data) setRetryNote(data.requeued)
    },
  })

  const status = batch.status
  const active = !BATCH_TERMINAL.has(status)
  const counts = [
    { key: 'batchPanel.count.pending', value: detail.data?.pending },
    { key: 'batchPanel.count.claimed', value: detail.data?.claimed },
    { key: 'batchPanel.count.processing', value: detail.data?.processing },
    { key: 'batchPanel.count.done', value: detail.data?.done },
  ]

  return (
    <li className="rounded-lg border border-zinc-200 dark:border-zinc-800">
      <div className="flex items-center gap-2 px-3 py-2.5">
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          aria-expanded={expanded}
          className="flex min-w-0 flex-1 cursor-default items-center gap-3 text-left text-sm outline-none hover:bg-zinc-50 dark:hover:bg-zinc-900/50"
        >
          <Badge variant={BATCH_STATUS_VARIANT[status] ?? 'secondary'}>{statusText(status)}</Badge>
          <span className="truncate font-medium">{kindText(batch.kind)}</span>
          <span className="shrink-0 text-xs text-zinc-400">
            {t('batchPanel.total', { total: batch.total })}
          </span>
          <span className="ml-auto shrink-0 text-xs text-zinc-400">
            {formatTime(batch.created_at)}
          </span>
          <ChevronDown
            aria-hidden="true"
            className={cn('size-4 shrink-0 text-zinc-400 transition-transform', expanded && 'rotate-180')}
          />
        </button>
      </div>

      <div className="flex flex-wrap items-center gap-1.5 px-3 pb-2.5">
        {counts.map((c) => (
          <span
            key={c.key}
            className="rounded bg-zinc-100 px-1.5 py-0.5 text-xs text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300"
          >
            {t(c.key)} {c.value ?? '–'}
          </span>
        ))}
        <div className="ml-auto flex items-center gap-1.5">
          {retryNote !== null && (
            <span role="status" className="text-xs text-emerald-600 dark:text-emerald-400">
              {t('batchPanel.retryDone', { n: retryNote })}
            </span>
          )}
          {status === 'running' && (
            <Button
              size="sm"
              variant="outline"
              disabled={action.isPending}
              onClick={() => action.mutate('pause')}
            >
              {action.isPending && action.variables === 'pause' && (
                <Loader2 className="size-3 animate-spin" aria-hidden="true" />
              )}
              {t('batchPanel.action.pause')}
            </Button>
          )}
          {status === 'paused' && (
            <Button
              size="sm"
              variant="outline"
              disabled={action.isPending}
              onClick={() => action.mutate('resume')}
            >
              {action.isPending && action.variables === 'resume' && (
                <Loader2 className="size-3 animate-spin" aria-hidden="true" />
              )}
              {t('batchPanel.action.resume')}
            </Button>
          )}
          {active && (
            <Button
              size="sm"
              variant="outline"
              disabled={action.isPending}
              onClick={() => action.mutate('cancel')}
            >
              {action.isPending && action.variables === 'cancel' && (
                <Loader2 className="size-3 animate-spin" aria-hidden="true" />
              )}
              {t('batchPanel.action.cancel')}
            </Button>
          )}
          {status !== 'completed' && (
            <Button
              size="sm"
              variant="ghost"
              disabled={action.isPending}
              onClick={() => action.mutate('retry')}
            >
              {action.isPending && action.variables === 'retry' && (
                <Loader2 className="size-3 animate-spin" aria-hidden="true" />
              )}
              {t('batchPanel.action.retry')}
            </Button>
          )}
        </div>
      </div>

      {action.isError && (
        <p role="alert" className="px-3 pb-2 text-xs text-red-600 dark:text-red-400">
          {t('batchPanel.actionFailed')}
          {action.error instanceof Error ? `: ${action.error.message}` : ''}
        </p>
      )}

      {expanded && <BatchItems batchId={batch.id} active={active} />}
    </li>
  )
}

/** 展开区：条目列表（status + 截断 error）。展开且批次非终态时 5s 轮询。 */
function BatchItems({ batchId, active }: { batchId: string; active: boolean }) {
  const { t, i18n } = useTranslation()
  const items = useQuery({
    queryKey: ['batch-items', batchId],
    queryFn: () => listBatchItems(batchId, { limit: 200 }),
    refetchInterval: active ? 5000 : false,
  })
  const statusText = (s: string) => {
    const key = `batchPanel.status.${s}`
    return i18n.exists(key) ? t(key) : s
  }
  return (
    <div className="border-t border-zinc-200 px-3 py-2 dark:border-zinc-800">
      {items.isPending ? (
        <p className="py-1 text-xs text-zinc-400">{t('common.loading')}</p>
      ) : items.isError ? (
        <p role="alert" className="py-1 text-xs text-red-600 dark:text-red-400">
          {items.error instanceof Error ? items.error.message : t('batchPanel.itemsFailed')}
        </p>
      ) : items.data.length === 0 ? (
        <p className="py-1 text-xs text-zinc-400">{t('batchPanel.itemsEmpty')}</p>
      ) : (
        <ul className="divide-y divide-zinc-100 dark:divide-zinc-800/60">
          {items.data.map((item) => (
            <li key={item.id} className="flex min-w-0 items-center gap-2 py-1.5 text-xs">
              <Badge variant={ITEM_STATUS_VARIANT[item.status] ?? 'secondary'}>
                {statusText(item.status)}
              </Badge>
              <span className="shrink-0 font-mono text-zinc-400">#{item.seq}</span>
              <span className="truncate text-zinc-500 dark:text-zinc-400" title={item.error}>
                {item.error ?? ''}
              </span>
              <span className="ml-auto shrink-0 text-zinc-400">
                {t('batchPanel.attempts', { attempt: item.attempts, max: item.max_attempts })}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

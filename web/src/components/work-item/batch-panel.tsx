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
  startBatch,
  type Batch,
  type BatchCreateSourceResult,
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
  // draft：source 通道已创建但未开跑（物化预览待确认）
  draft: 'warning',
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

type BatchActionKind = 'pause' | 'resume' | 'cancel' | 'retry' | 'start'

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

/**
 * 创建批次对话框：双模式。
 *  - manual：kind 下拉 + payloads（每行一个 JSON 对象）+ 并发数（v1 行为不变）；
 *  - source：数据源工具 + args/pick + payload_template + required，
 *    创建后先弹物化预览（数据源 N → 物化 M · 跳过 K + sample），确认后才开跑。
 */
function CreateBatchDialog({
  workItemId,
  onOpenChange,
}: {
  workItemId: string
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const [mode, setMode] = useState<'manual' | 'source'>('manual')

  return (
    <Dialog open onOpenChange={(o) => (o ? undefined : onOpenChange(false))}>
      <DialogContent className="flex max-h-[85dvh] flex-col gap-3 overflow-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('batchPanel.createTitle')}</DialogTitle>
        </DialogHeader>

        {/* 模式切换：manual = 现有 payloads 表单；source = 数据源生成（v2.0） */}
        <div role="radiogroup" aria-label={t('batchPanel.modeLabel')} className="flex gap-1.5">
          {(
            [
              ['manual', 'batchPanel.mode.manual'],
              ['source', 'batchPanel.mode.source'],
            ] as const
          ).map(([value, labelKey]) => (
            <button
              key={value}
              type="button"
              role="radio"
              aria-checked={mode === value}
              onClick={() => setMode(value)}
              className={cn(
                'rounded-md border px-2.5 py-1 text-xs outline-none focus-visible:ring-2 focus-visible:ring-zinc-400',
                mode === value
                  ? 'border-zinc-900 bg-zinc-900 text-white dark:border-zinc-100 dark:bg-zinc-100 dark:text-zinc-900'
                  : 'border-zinc-300 bg-white text-zinc-600 hover:bg-zinc-50 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-300 dark:hover:bg-zinc-800',
              )}
            >
              {t(labelKey)}
            </button>
          ))}
        </div>

        {mode === 'manual' ? (
          <ManualCreateForm workItemId={workItemId} onOpenChange={onOpenChange} />
        ) : (
          <SourceCreateForm workItemId={workItemId} onOpenChange={onOpenChange} />
        )}
      </DialogContent>
    </Dialog>
  )
}

/** 通用：kind 下拉 + 并发数（两模式共用）。 */
function KindConcurrencyFields({
  kind,
  onKindChange,
  concurrency,
  onConcurrencyChange,
}: {
  kind: string
  onKindChange: (v: string) => void
  concurrency: string
  onConcurrencyChange: (v: string) => void
}) {
  const { t } = useTranslation()
  return (
    <>
      <label className="flex items-center gap-2 text-sm">
        <span className="shrink-0">{t('batchPanel.kindLabel')}</span>
        <select
          value={kind}
          aria-label={t('batchPanel.kindLabel')}
          onChange={(e) => onKindChange(e.target.value)}
          className={SELECT_CLASS}
        >
          {KIND_OPTIONS.map((k) => (
            <option key={k.value} value={k.value}>
              {t(k.labelKey)}
            </option>
          ))}
        </select>
      </label>
      <label className="flex items-center gap-2 text-sm">
        <span className="shrink-0">{t('batchPanel.concurrencyLabel')}</span>
        <Input
          type="number"
          min={1}
          max={64}
          value={concurrency}
          aria-label={t('batchPanel.concurrencyLabel')}
          onChange={(e) => onConcurrencyChange(e.target.value)}
          className="h-8 w-20"
        />
      </label>
    </>
  )
}

/** 解析单个 JSON 对象输入；空串→null（合法），否则解析失败抛错由调用方提示。 */
function parseJsonObject(text: string): Record<string, unknown> | null | undefined {
  const s = text.trim()
  if (!s) return null
  try {
    const obj = JSON.parse(s) as unknown
    if (obj === null || typeof obj !== 'object' || Array.isArray(obj)) return undefined
    return obj as Record<string, unknown>
  } catch {
    return undefined
  }
}

/** 归一并发数（与 manual 模式同规则）。 */
function normalizeConcurrency(v: string): number {
  const n = Number(v)
  return Number.isFinite(n) && n >= 1 ? Math.min(Math.trunc(n), 64) : 3
}

/** 手动条目模式（原 v1 表单，行为不变）。 */
function ManualCreateForm({
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
    create.mutate({
      kind,
      payloads,
      concurrency: normalizeConcurrency(concurrency),
    })
  }

  return (
    <>
      <KindConcurrencyFields
        kind={kind}
        onKindChange={setKind}
        concurrency={concurrency}
        onConcurrencyChange={setConcurrency}
      />

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
    </>
  )
}

/** 物化预览的数据：对账计数 + sample。创建响应带预览字段则直接用，否则 GET 批次补拉。 */
interface MaterializationPreview {
  sourceCount: number
  materialized: number
  skipped: number
  sample: Record<string, unknown>[]
}

/**
 * 从创建响应提取预览：source 通道响应 = Batch（含 source_materialization）+ sample。
 * 对账字段缺失（旧形态/非 source）→ null，由调用方决定补拉或直接关闭。
 */
function previewFromCreate(res: Batch | BatchCreateSourceResult): MaterializationPreview | null {
  const mat = res.source_materialization
  const sample = hasPreviewFields(res) && Array.isArray(res.sample) ? res.sample : []
  if (!mat) return null
  return {
    sourceCount: mat.source_count,
    materialized: mat.materialized,
    skipped: mat.skipped,
    sample,
  }
}

/** 从 GET 批次响应提取预览（创建响应只有 Batch 时的补拉路径）。 */
function previewFromDetail(
  res: Awaited<ReturnType<typeof getBatch>>,
): MaterializationPreview | null {
  const mat = res.batch.source_materialization
  if (!mat) return null
  return {
    sourceCount: mat.source_count,
    materialized: mat.materialized,
    skipped: mat.skipped,
    sample: [],
  }
}

/** 窄化：创建响应是否带预览 sample 字段（BatchCreateSourceResult 形态）。 */
function hasPreviewFields(
  res: Batch | BatchCreateSourceResult,
): res is BatchCreateSourceResult {
  return 'sample' in res
}

/** 从数据源生成模式（v2.0）：表单 → 创建（status=draft）→ 物化预览 → 开始执行 / 留在草稿。 */
function SourceCreateForm({
  workItemId,
  onOpenChange,
}: {
  workItemId: string
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [kind, setKind] = useState<string>(KIND_OPTIONS[0].value)
  const [sourceTool, setSourceTool] = useState('list_attachments')
  const [argsText, setArgsText] = useState('')
  const [pick, setPick] = useState('')
  const [templateText, setTemplateText] = useState('{"image_ref":"$.file_id","lang":"zh"}')
  const [required, setRequired] = useState('')
  const [concurrency, setConcurrency] = useState('3')
  const [fieldErrors, setFieldErrors] = useState<{ args?: boolean; template?: boolean }>({})

  // 创建成功后的物化预览（非空 → 切换到预览视图）
  const [preview, setPreview] = useState<MaterializationPreview | null>(null)
  const [draftBatchId, setDraftBatchId] = useState<string | null>(null)

  const close = () => onOpenChange(false)
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['work-item-batches', workItemId] })
    if (draftBatchId) void queryClient.invalidateQueries({ queryKey: ['batch-detail', draftBatchId] })
  }

  // source 模式创建：响应带预览字段直接用；否则 GET 一次批次补拉对账。
  // 两路都拿不到预览（如后端未返回 source_materialization）→ 直接关闭，回落到列表。
  const create = useMutation({
    mutationFn: async (input: Parameters<typeof createBatch>[1]) => {
      const res = await createBatch(workItemId, input)
      setDraftBatchId(res.id)
      const p = hasPreviewFields(res) ? previewFromCreate(res) : null
      if (p) return p
      const detail = await getBatch(res.id)
      return previewFromDetail(detail)
    },
    onSuccess: (p) => {
      void queryClient.invalidateQueries({ queryKey: ['work-item-batches', workItemId] })
      if (p) setPreview(p)
      else close()
    },
  })

  const start = useMutation({
    mutationFn: (batchId: string) => startBatch(batchId),
    onSuccess: () => {
      invalidate()
      close()
    },
  })

  const submit = () => {
    if (create.isPending) return
    if (!sourceTool.trim()) return
    const errors: typeof fieldErrors = {}
    const args = parseJsonObject(argsText)
    if (args === undefined) errors.args = true
    const template = parseJsonObject(templateText)
    if (template === undefined || template === null) errors.template = true
    setFieldErrors(errors)
    if (errors.args || errors.template) return
    const requiredFields = required
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean)
    create.mutate({
      kind,
      source: {
        tool: sourceTool.trim(),
        args: args ?? undefined,
        pick: pick.trim() || undefined,
      },
      payload_template: template as Record<string, unknown>,
      ...(requiredFields.length > 0 ? { required: requiredFields } : {}),
      concurrency: normalizeConcurrency(concurrency),
    })
  }

  // ── 预览视图：对账 + sample 前 3 条 + 开始执行 / 留在草稿 ──
  if (preview && draftBatchId) {
    return (
      <>
        <div className="space-y-2">
          <p className="text-sm font-semibold">{t('batchPanel.matPreview')}</p>
          <p className="rounded bg-zinc-100 px-2 py-1 font-mono text-xs text-zinc-700 dark:bg-zinc-800 dark:text-zinc-300">
            {t('batchPanel.matAccount', {
              source: preview.sourceCount,
              materialized: preview.materialized,
              skipped: preview.skipped,
            })}
          </p>
          {preview.sample.length > 0 && (
            <div className="space-y-1">
              <p className="text-xs text-zinc-500 dark:text-zinc-400">
                {t('batchPanel.sampleTitle')}
              </p>
              {preview.sample.slice(0, 3).map((s, i) => (
                <pre
                  key={i}
                  className="overflow-auto rounded bg-zinc-50 p-1.5 font-mono text-[11px] text-zinc-600 dark:bg-zinc-800/60 dark:text-zinc-300"
                >
                  {JSON.stringify(s, null, 2)}
                </pre>
              ))}
            </div>
          )}
        </div>

        {start.isError && (
          <p role="alert" className="text-xs text-red-600 dark:text-red-400">
            {start.error instanceof Error ? start.error.message : t('batchPanel.startFailed')}
          </p>
        )}

        <DialogFooter>
          <Button
            size="sm"
            disabled={start.isPending}
            onClick={() => start.mutate(draftBatchId)}
          >
            {start.isPending && (
              <Loader2 className="size-3 animate-spin" aria-hidden="true" />
            )}
            {t('batchPanel.action.start')}
          </Button>
          <Button variant="ghost" size="sm" onClick={close}>
            {t('batchPanel.stayDraft')}
          </Button>
        </DialogFooter>
      </>
    )
  }

  // ── 表单视图 ──
  return (
    <>
      <KindConcurrencyFields
        kind={kind}
        onKindChange={setKind}
        concurrency={concurrency}
        onConcurrencyChange={setConcurrency}
      />

      <div className="space-y-1">
        <label htmlFor="batch-source-tool" className="block text-sm">
          {t('batchPanel.sourceTool')}
        </label>
        <Input
          id="batch-source-tool"
          value={sourceTool}
          onChange={(e) => setSourceTool(e.target.value)}
          placeholder="list_attachments"
          className="h-8 font-mono"
        />
        <p className="text-xs text-zinc-400">{t('batchPanel.sourceToolHint')}</p>
      </div>

      <div className="space-y-1">
        <label htmlFor="batch-source-args" className="block text-sm">
          {t('batchPanel.sourceArgs')}
        </label>
        <textarea
          id="batch-source-args"
          rows={3}
          value={argsText}
          onChange={(e) => {
            setArgsText(e.target.value)
            if (fieldErrors.args) setFieldErrors((f) => ({ ...f, args: false }))
          }}
          placeholder='{"status":"pending"}'
          aria-invalid={fieldErrors.args}
          className={cn(TEXTAREA_CLASS, fieldErrors.args && 'border-red-500 dark:border-red-500')}
        />
      </div>

      <div className="space-y-1">
        <label htmlFor="batch-source-pick" className="block text-sm">
          {t('batchPanel.sourcePick')}
        </label>
        <Input
          id="batch-source-pick"
          value={pick}
          onChange={(e) => setPick(e.target.value)}
          placeholder="$.attachments[*]"
          className="h-8 font-mono"
        />
      </div>

      <div className="space-y-1">
        <label htmlFor="batch-payload-template" className="block text-sm">
          {t('batchPanel.payloadTemplate')}
        </label>
        <textarea
          id="batch-payload-template"
          rows={3}
          value={templateText}
          onChange={(e) => {
            setTemplateText(e.target.value)
            if (fieldErrors.template) setFieldErrors((f) => ({ ...f, template: false }))
          }}
          placeholder='{"image_ref":"$.file_id","lang":"zh"}'
          aria-invalid={fieldErrors.template}
          className={cn(TEXTAREA_CLASS, fieldErrors.template && 'border-red-500 dark:border-red-500')}
        />
      </div>

      <div className="space-y-1">
        <label htmlFor="batch-required" className="block text-sm">
          {t('batchPanel.requiredFields')}
        </label>
        <Input
          id="batch-required"
          value={required}
          onChange={(e) => setRequired(e.target.value)}
          placeholder="image_ref"
          className="h-8 font-mono"
        />
      </div>

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
    </>
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
      if (kind === 'start') return startBatch(batch.id)
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
  const isDraft = status === 'draft'
  const active = !BATCH_TERMINAL.has(status)
  const mat = batch.source_materialization
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
        {mat && (
          <span
            className="rounded bg-zinc-100 px-1.5 py-0.5 font-mono text-xs text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300"
            title={t('batchPanel.matAccount', {
              source: mat.source_count,
              materialized: mat.materialized,
              skipped: mat.skipped,
            })}
          >
            {t('batchPanel.matAccount', {
              source: mat.source_count,
              materialized: mat.materialized,
              skipped: mat.skipped,
            })}
          </span>
        )}
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
          {isDraft && (
            <Button
              size="sm"
              variant="default"
              disabled={action.isPending}
              onClick={() => action.mutate('start')}
            >
              {action.isPending && action.variables === 'start' && (
                <Loader2 className="size-3 animate-spin" aria-hidden="true" />
              )}
              {t('batchPanel.action.start')}
            </Button>
          )}
          {!isDraft && active && (
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
          {!isDraft && status !== 'completed' && (
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
              {/* failed → 红色 error；skipped → 灰色 skip_reason（两者来源不同） */}
              {item.status === 'skipped' ? (
                <span
                  className="truncate text-zinc-400 dark:text-zinc-500"
                  title={item.skip_reason}
                >
                  {item.skip_reason ?? ''}
                </span>
              ) : (
                <span className="truncate text-red-600 dark:text-red-400" title={item.error}>
                  {item.error ?? ''}
                </span>
              )}
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

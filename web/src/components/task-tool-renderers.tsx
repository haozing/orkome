import { useMutation, useQuery } from '@tanstack/react-query'
import { api } from '@ploykit/client'
import { Check, Copy, ExternalLink } from 'lucide-react'
import { Fragment, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { listPackEntries, type PackUIEntry } from '@/api/packs'
import type { JobHandle, JobState, JobStatus, TaskMessage } from '@/api/orchestration'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useWorkspaceStore } from '@/stores/workspace'

// 任务流工具结果渲染器注册表（B2 / F1+F5，docs/pack-frontend-design-2026-10-11.md §2.1）。
//
// 设计约束（审计核实，勿破坏）：
//  1. TaskMessage 无工具名字段——三张卡全部按 result 形状触发，不依赖工具名；
//     工具名（由 task-messages.tsx 用 call_id 回溯 tool_use 首 token 配对传入）只用于
//     JobHandleCard 的"在 pack 控制台打开"深链（查 ui-entries 的 pack entry）。
//  2. 截断兜底硬规则：truncated === true 或 JSON.parse 失败 → 一律默认灰框
//     （与 A1 的"非契约退化"激励自洽），任何卡都不得因脏数据崩。
//  3. MCP 工具结果经 eino-ext 序列化为 CallToolResult 包装串：
//     {"content":[{"type":"text","text":"…"}],"structuredContent":{…}}——
//     判定先解包装再看内层；所有形状判定 fail-safe：匹配不上 → 默认灰框。
//  4. 违规态不是三卡之一：A1 冻结的标准违规错误是纯文本（一行 summary + 一行
//     JSON，code 固定 TOOL_OUTPUT_CONTRACT_VIOLATION），单独给轻量呈现。
//  5. 渲染顺序即优先级：JobStatus（字段最全）→ JobHandle → Metrics → 默认灰框。

const VIOLATION_CODE = 'TOOL_OUTPUT_CONTRACT_VIOLATION'
const JOB_STATES: readonly JobState[] = [
  'queued',
  'running',
  'awaiting_review',
  'published',
  'failed',
]
// 进度条只画正向生命周期；failed 不在轨道上（失败详情在 error 块红态呈现）
const PROGRESS_STATES: readonly JobState[] = ['queued', 'running', 'awaiting_review', 'published']

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

interface ContractViolation {
  tool: string
  reason: string
  excerpt: string
}

// A1 冻结格式：`tool output contract violation: <原因短语>` + JSON 行。
// 逐行找以 { 开头且能解析的行，code 匹配才算违规；解析不出 → null（走默认灰框）。
function parseContractViolation(raw: string): ContractViolation | null {
  for (const line of raw.split('\n')) {
    const s = line.trim()
    if (!s.startsWith('{')) continue
    try {
      const v: unknown = JSON.parse(s)
      if (isPlainObject(v) && v.code === VIOLATION_CODE && typeof v.reason === 'string') {
        return {
          tool: typeof v.tool === 'string' ? v.tool : '',
          reason: v.reason,
          excerpt: typeof v.output_excerpt === 'string' ? v.output_excerpt : '',
        }
      }
    } catch {
      // 非 JSON 行，继续找下一行
    }
  }
  return null
}

// 解 CallToolResult 包装层（A1 接口文档 §2）：
//   structuredContent 存在 → 直接用它；否则 content[0].text 再解一层 JSON；
//   包装在但内层解不动 → 返回 null（落默认灰框）；非包装对象 → 原样返回（防御
//   未包装的结构化结果）。
function unwrapToolResult(parsed: unknown): unknown {
  if (!isPlainObject(parsed)) return parsed
  if (!Array.isArray(parsed.content)) return parsed
  if (isPlainObject(parsed.structuredContent)) return parsed.structuredContent
  const first = parsed.content[0]
  if (isPlainObject(first) && typeof first.text === 'string') {
    try {
      return JSON.parse(first.text)
    } catch {
      return null
    }
  }
  return null
}

interface JobHandleDoc extends JobHandle {
  state?: JobState
}

// JobStatus 判定（§2.1 固定 schema 的必填三件套）：job_id + state ∈ 五态 + stage 字符串。
// result/metrics/error 为可选，内层形状异常时按缺省处理，不判整体失败。
function parseJobStatus(doc: unknown): JobStatus | null {
  if (!isPlainObject(doc)) return null
  if (typeof doc.job_id !== 'string' || !doc.job_id) return null
  if (!JOB_STATES.includes(doc.state as JobState)) return null
  if (typeof doc.stage !== 'string') return null
  const status: JobStatus = { job_id: doc.job_id, state: doc.state as JobState, stage: doc.stage }
  if (isPlainObject(doc.result)) {
    status.result = {}
    if (typeof doc.result.url === 'string') status.result.url = doc.result.url
    if (typeof doc.result.summary === 'string') status.result.summary = doc.result.summary
  }
  if (isPlainObject(doc.metrics)) status.metrics = doc.metrics as Record<string, unknown>
  if (isPlainObject(doc.error) && typeof doc.error.code === 'string') {
    status.error = {
      code: doc.error.code,
      retryable: doc.error.retryable === true,
      stage_failed:
        doc.error.stage_failed === 'pre_publish' || doc.error.stage_failed === 'post_publish'
          ? doc.error.stage_failed
          : null,
    }
  }
  return status
}

// JobHandle 判定：契约形状是恰好 {job_id}；容忍一个可选 state 提示（卡上显示徽章）。
// 出现其他键就不认——那属于别的卡或非契约输出，落默认灰框。
function parseJobHandle(doc: unknown): JobHandleDoc | null {
  if (!isPlainObject(doc)) return null
  if (typeof doc.job_id !== 'string' || !doc.job_id) return null
  for (const key of Object.keys(doc)) {
    if (key !== 'job_id' && key !== 'state') return null
  }
  if ('state' in doc && !JOB_STATES.includes(doc.state as JobState)) return null
  return {
    job_id: doc.job_id,
    ...(JOB_STATES.includes(doc.state as JobState) ? { state: doc.state as JobState } : {}),
  }
}

// Metrics 判定：result 含非空 metrics 对象（空对象没有可渲染内容，不算命中）
function parseMetrics(doc: unknown): Record<string, unknown> | null {
  if (!isPlainObject(doc) || !isPlainObject(doc.metrics)) return null
  if (Object.keys(doc.metrics).length === 0) return null
  return doc.metrics as Record<string, unknown>
}

// 深链 URL 展开：与 pack-entries.tsx renderURL 同风格；{jobId} 为本批（F5）新增变量。
function renderConsoleUrl(template: string, uiToken: string, ws: string, jobId: string): string {
  return template
    .replaceAll('{uiToken}', encodeURIComponent(uiToken))
    .replaceAll('{workspaceId}', encodeURIComponent(ws))
    .replaceAll('{jobId}', encodeURIComponent(jobId))
}

// 工具名带 <pack>_ 前缀；多个 pack 前缀都能匹配时取最长者（避免 a / a_b 二义）
function findPackEntry(toolName: string | undefined, entries: PackUIEntry[]): PackUIEntry | null {
  if (!toolName) return null
  let best: PackUIEntry | null = null
  for (const e of entries) {
    if (toolName.startsWith(e.pack + '_') && (!best || e.pack.length > best.pack.length)) best = e
  }
  return best
}

function stateBadgeVariant(state: JobState) {
  if (state === 'published') return 'success' as const
  if (state === 'failed') return 'danger' as const
  if (state === 'awaiting_review') return 'warning' as const
  if (state === 'running') return 'default' as const
  return 'neutral' as const
}

function JobStateBadge({ state }: { state: JobState }) {
  const { t } = useTranslation()
  return (
    <Badge variant={stateBadgeVariant(state)} data-testid="task-job-state">
      {t(`taskMsg.state.${state}`)}
    </Badge>
  )
}

function CopyChip({ value }: { value: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const copy = () => {
    // clipboard 可能不可用（非安全上下文/jsdom）：静默跳过，不崩卡
    navigator.clipboard
      ?.writeText(value)
      .then(() => {
        setCopied(true)
        window.setTimeout(() => setCopied(false), 1500)
      })
      .catch(() => {})
  }
  return (
    <button
      type="button"
      onClick={copy}
      aria-label={t('taskMsg.copy')}
      className="inline-flex shrink-0 items-center gap-1 rounded px-1 text-xs text-zinc-500 transition-colors hover:bg-zinc-200/60 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-700/60 dark:hover:text-zinc-100"
    >
      {copied ? (
        <Check className="size-3" aria-hidden="true" />
      ) : (
        <Copy className="size-3" aria-hidden="true" />
      )}
      <span>{copied ? t('taskMsg.copied') : t('taskMsg.copy')}</span>
    </button>
  )
}

// 默认渲染器（注册表的 default）：现状灰框逐字保留——兜底一切未匹配形状，
// 样式零回归基线。
export function DefaultToolResult({ message }: { message: TaskMessage }) {
  const { t } = useTranslation()
  return (
    <>
      <pre className="max-h-64 overflow-auto font-mono text-xs break-all whitespace-pre-wrap">
        {message.content}
      </pre>
      {message.truncated && <p className="text-xs opacity-60">{t('taskMsg.truncated')}</p>}
    </>
  )
}

// 轻量违规态（非三卡之一）：A1 输出契约校验失败时模型看到的标准错误文本
export function ContractViolationCard({ violation }: { violation: ContractViolation }) {
  const { t } = useTranslation()
  return (
    <div
      data-testid="task-violation"
      className="rounded-md border border-red-200 bg-red-50 px-2 py-1.5 dark:border-red-900/50 dark:bg-red-950/40"
    >
      <p className="text-xs font-medium text-red-700 dark:text-red-300">
        {t('taskMsg.violationTitle')}
        {violation.tool ? ` · ${violation.tool}` : ''}
      </p>
      <p className="mt-0.5 font-mono text-xs break-all text-red-600 dark:text-red-400">
        {violation.reason}
      </p>
      {violation.excerpt && (
        <>
          <p className="mt-1 text-xs text-red-600/70 dark:text-red-400/70">
            {t('taskMsg.violationExcerpt')}
          </p>
          <pre className="mt-0.5 max-h-32 overflow-auto font-mono text-xs break-all whitespace-pre-wrap text-red-600/80 dark:text-red-400/80">
            {violation.excerpt}
          </pre>
        </>
      )}
    </div>
  )
}

function formatMetricValue(v: unknown): string {
  if (v === null) return 'null'
  if (typeof v === 'string') return v
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  return JSON.stringify(v)
}

function MetricsGrid({ metrics }: { metrics: Record<string, unknown> }) {
  return (
    <dl
      data-testid="task-metrics-grid"
      className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs"
    >
      {Object.entries(metrics).map(([k, v]) => (
        <Fragment key={k}>
          <dt className="text-zinc-500 dark:text-zinc-400">{k}</dt>
          <dd className="font-mono break-all">{formatMetricValue(v)}</dd>
        </Fragment>
      ))}
    </dl>
  )
}

// MetricsCard：result 含 metrics 对象 → 键值对网格（质检指标一类）
export function MetricsCard({ doc }: { doc: unknown }) {
  const { t } = useTranslation()
  const metrics = parseMetrics(doc)
  if (!metrics) return null
  return (
    <div data-testid="task-metrics" className="space-y-1">
      <p className="text-xs font-medium opacity-70">{t('taskMsg.metrics')}</p>
      <MetricsGrid metrics={metrics} />
    </div>
  )
}

// JobStatusCard：匹配 JobStatus 固定 schema → stage 进度 + state 徽章 +
// 失败详情（error.code + stage_failed 标签：post_publish 红色警示 / pre_publish 中性）+
// result.url 可点（内嵌 metrics 也顺手画网格）
export function JobStatusCard({ doc }: { doc: unknown }) {
  const { t } = useTranslation()
  const status = parseJobStatus(doc)
  if (!status) return null
  const failed = status.state === 'failed'
  const progressIdx = PROGRESS_STATES.indexOf(status.state)
  const postPublish = status.error?.stage_failed === 'post_publish'
  return (
    <div data-testid="task-job-status" className="space-y-1.5">
      <div className="flex flex-wrap items-center gap-2">
        <JobStateBadge state={status.state} />
        <span className="text-xs text-zinc-600 dark:text-zinc-300">
          {t('taskMsg.stage')}: {status.stage}
        </span>
        <code className="ml-auto font-mono text-[11px] text-zinc-400">{status.job_id}</code>
      </div>
      <div className="flex gap-1" aria-hidden="true">
        {PROGRESS_STATES.map((s, i) => (
          <span
            key={s}
            className={
              'h-1 flex-1 rounded-full ' +
              (!failed && i <= progressIdx
                ? 'bg-blue-500 dark:bg-blue-400'
                : 'bg-zinc-200 dark:bg-zinc-800')
            }
          />
        ))}
      </div>
      {status.error && (
        <div
          data-testid="task-job-error"
          className={
            'rounded-md border px-2 py-1.5 ' +
            (postPublish
              ? 'border-red-200 bg-red-50 dark:border-red-900/50 dark:bg-red-950/40'
              : 'border-zinc-200 bg-zinc-50 dark:border-zinc-800 dark:bg-zinc-900/50')
          }
        >
          <p className="font-mono text-xs break-all">{status.error.code}</p>
          <div className="mt-1 flex flex-wrap items-center gap-2">
            {status.error.stage_failed && (
              <Badge variant={postPublish ? 'danger' : 'neutral'}>
                {t('taskMsg.stageFailed')}: {status.error.stage_failed}
              </Badge>
            )}
            <span className="font-mono text-xs opacity-70">
              retryable: {String(status.error.retryable)}
            </span>
          </div>
          {postPublish && (
            <p className="mt-1 text-xs font-medium text-red-700 dark:text-red-300">
              {t('taskMsg.postPublishWarning')}
            </p>
          )}
          {status.error.stage_failed === 'pre_publish' && (
            <p className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">
              {t('taskMsg.prePublishNote')}
            </p>
          )}
        </div>
      )}
      {status.result?.summary && (
        <p className="text-xs text-zinc-600 dark:text-zinc-300">{status.result.summary}</p>
      )}
      {status.result?.url && (
        <a
          href={status.result.url}
          target="_blank"
          rel="noreferrer"
          data-testid="task-job-result-url"
          className="block truncate font-mono text-xs text-blue-600 underline-offset-2 hover:underline dark:text-blue-400"
        >
          {status.result.url}
        </a>
      )}
      {status.metrics && (
        <div className="space-y-1">
          <p className="text-xs font-medium opacity-70">{t('taskMsg.metrics')}</p>
          <MetricsGrid metrics={status.metrics} />
        </div>
      )}
    </div>
  )
}

// JobHandleCard：result 匹配 {job_id} 形状 → job_id（可复制）+ state 徽章（若有）+
// "在 pack 控制台打开"深链（{jobId} 模板变量展开）。查不到 pack entry / 无工具名 /
// 无 workspace 时按钮隐藏，job_id 仍显示。ui-token 60s 过期：每次点击现铸，不缓存。
export function JobHandleCard({ doc, toolName }: { doc: unknown; toolName?: string }) {
  const { t } = useTranslation()
  const handle = parseJobHandle(doc)
  const workspaceId = useWorkspaceStore((s) => s.currentId)
  const entries = useQuery({
    // 与侧栏 PackEntries 共用缓存键（相同端点与形状）
    queryKey: ['pack-ui-entries', workspaceId],
    queryFn: listPackEntries,
    enabled: !!workspaceId && !!toolName,
    staleTime: 60000,
  })
  const jump = useMutation({
    mutationFn: async () => {
      if (!handle || !workspaceId) throw new Error('job handle deep link preconditions missing')
      const entry = findPackEntry(toolName, entries.data ?? [])
      if (!entry) throw new Error('no matching pack entry')
      const res = await api.post<{ token: string }>('/api/ui-tokens', {
        plugin: entry.pack,
        workspace_id: workspaceId,
      })
      return renderConsoleUrl(entry.url_template, res.token, workspaceId, handle.job_id)
    },
    onSuccess: (url) => window.open(url, '_blank', 'noopener'),
  })
  if (!handle) return null
  const entry = findPackEntry(toolName, entries.data ?? [])
  return (
    <div data-testid="task-job-handle" className="space-y-1.5">
      <div className="flex flex-wrap items-center gap-2">
        <code className="font-mono text-xs break-all">{handle.job_id}</code>
        <CopyChip value={handle.job_id} />
        {handle.state && <JobStateBadge state={handle.state} />}
      </div>
      {entry && (
        <Button
          size="xs"
          variant="outline"
          disabled={jump.isPending}
          onClick={() => jump.mutate()}
          data-testid="task-job-console-link"
        >
          <ExternalLink className="size-3" aria-hidden="true" />
          {t('taskMsg.openConsole')}
        </Button>
      )}
      {jump.isError && (
        <p role="alert" className="text-xs text-red-600 dark:text-red-400">
          {t('taskMsg.openConsoleFailed')}
        </p>
      )}
    </div>
  )
}

// 渲染器注册表（纯组件映射表，非进程内插件机制）：default 兜底一切未匹配；
// 形状判定按序短路。pack 自定义渲染器（按工具名 keyed）是 v1+ 的扩展位，
// v0 平台只内置这三张形状卡。
function renderShapeCard(doc: unknown, toolName: string | undefined): ReactNode {
  if (parseJobStatus(doc)) return <JobStatusCard doc={doc} />
  if (parseJobHandle(doc)) return <JobHandleCard doc={doc} toolName={toolName} />
  if (parseMetrics(doc)) return <MetricsCard doc={doc} />
  return null
}

// 渲染器入口：TaskMessageRow 对所有非 text 消息统一走这里。
// 硬规则：只有 tool_result 且未截断才尝试形状卡；tool_use / approval / error
// 维持现状灰框。任何一步失败都落 DefaultToolResult（零回归基线）。
export function ToolResultRenderer({
  message,
  toolName,
}: {
  message: TaskMessage
  toolName?: string
}) {
  if (message.kind !== 'tool_result' || message.truncated) {
    return <DefaultToolResult message={message} />
  }
  const violation = parseContractViolation(message.content)
  if (violation) return <ContractViolationCard violation={violation} />
  let parsed: unknown
  try {
    parsed = JSON.parse(message.content)
  } catch {
    return <DefaultToolResult message={message} />
  }
  const doc = unwrapToolResult(parsed)
  return renderShapeCard(doc, toolName) ?? <DefaultToolResult message={message} />
}

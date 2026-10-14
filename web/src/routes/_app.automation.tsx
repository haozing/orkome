import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { Dialog } from '@base-ui/react/dialog'
import { isApiError } from '@ploykit/client'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  createAutopilot,
  createWakeup,
  cronPreview,
  deleteAutopilot,
  deleteWakeup,
  listAutopilots,
  listWorkspaceWakeups,
  triggerAutopilot,
  setAutopilotEnabled,
  type Autopilot,
  type Wakeup,
} from '@/api/automation'
import { listAgents } from '@/api/orchestration'
import { listWorkItems, type WorkItem } from '@/api/work'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
export const Route = createFileRoute('/_app/automation')({
  component: AutomationPage,
})
const EXEC_MODE_KEYS: Record<string, string> = {
  create_work_item: 'automation.modeCreate',
  task_only: 'automation.modeTaskOnly',
}
const WAKEUP_KIND_KEYS: Record<string, string> = {
  time: 'automation.wakeKindTime',
  event: 'automation.wakeKindEvent',
  condition: 'automation.wakeKindCondition',
  system: 'automation.wakeKindSystem',
}
const CARD_CLASS = 'rounded-lg border border-zinc-200 p-4 dark:border-zinc-800'
const CARD_TITLE_CLASS = 'mb-1 text-sm font-semibold'
const CARD_DESC_CLASS = 'mb-3 text-xs text-zinc-500 dark:text-zinc-400'
const LABEL_CLASS = 'mb-1 block text-xs text-zinc-500'
const SELECT_CLASS =
  'h-9 w-full rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900'
const TEXTAREA_CLASS =
  'w-full resize-none rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm outline-none focus:border-zinc-400 dark:border-zinc-700 dark:bg-zinc-900'
/** 常用 IANA 时区；空值=UTC（后端语义：空串按 UTC 解释，"Local" 会被拒绝）。 */
const TIMEZONES = [
  '',
  'Asia/Shanghai',
  'Asia/Tokyo',
  'Asia/Singapore',
  'Europe/London',
  'Europe/Berlin',
  'America/New_York',
  'America/Los_Angeles',
]
function autopilotStatusKey(a: Autopilot): { key: string; tone: 'success' | 'neutral' | 'danger' } {
  if (a.paused_reason) return { key: 'automation.stFused', tone: 'danger' }
  if (!a.enabled) return { key: 'automation.stDisabled', tone: 'neutral' }
  return { key: 'automation.stEnabled', tone: 'success' }
}
function fmtTime(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString()
}
function shortId(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id
}
/** datetime-local 值就是浏览器本地时间；转成 5 段 cron（分 时 日 月 *）做一次性唤醒。 */
export function datetimeLocalToCron(value: string): { cron: string; at: Date } | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(value)
  if (!m) return null
  const at = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]))
  if (Number.isNaN(at.getTime())) return null
  const cron = `${Number(m[5])} ${Number(m[4])} ${Number(m[3])} ${Number(m[2])} *`
  return { cron, at }
}
function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
}
/** 409 分流：routes.go 的 Delete 对"启用中的触发器"和"pending/running runs"都回 409，靠 message 区分。 */
function deleteBlockedKey(err: unknown): 'automation.delBlockedTriggers' | 'automation.delBlockedRuns' | null {
  if (!isApiError(err) || err.status !== 409) return null
  return /trigger/i.test(err.message)
    ? 'automation.delBlockedTriggers'
    : 'automation.delBlockedRuns'
}
interface TriggerFeedback {
  name: string
  status: string
  reasonCode?: string
  taskId?: string
}
export function AutomationPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const autopilots = useQuery({ queryKey: ['autopilots'], queryFn: listAutopilots })
  const agents = useQuery({ queryKey: ['agents'], queryFn: () => listAgents(), staleTime: 60000 })
  const [feedback, setFeedback] = useState<TriggerFeedback | null>(null)
  const [confirmingId, setConfirmingId] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['autopilots'] })
  const agentName = (id: string) =>
    agents.data?.find((a) => a.id === id)?.name ?? shortId(id)
  const toggle = useMutation({
    mutationFn: (a: Autopilot) => setAutopilotEnabled(a.id, !a.enabled),
    onSuccess: () => void invalidate(),
  })
  const trigger = useMutation({
    mutationFn: triggerAutopilot,
    onSuccess: (run) => {
      const ap = autopilots.data?.find((a) => a.id === run.autopilot_id)
      setFeedback({
        name: ap?.name ?? run.autopilot_id,
        status: run.status,
        reasonCode: run.reason_code,
        taskId: run.task_id,
      })
      void invalidate()
    },
  })
  const remove = useMutation({
    mutationFn: deleteAutopilot,
    onSuccess: () => {
      setConfirmingId(null)
      void invalidate()
    },
  })
  const actionError = toggle.error ?? trigger.error ?? remove.error
  const blockedKey = remove.error ? deleteBlockedKey(remove.error) : null
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-lg font-semibold">{t('nav.automation')}</h1>
        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{t('automation.intro')}</p>
      </div>

      <section className={CARD_CLASS}>
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 className={CARD_TITLE_CLASS}>{t('automation.mineTitle')}</h2>
            <p className={CARD_DESC_CLASS}>{t('automation.mineDesc')}</p>
          </div>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            {t('automation.newBtn')}
          </Button>
        </div>

        {feedback && (
          <div role="status" className="mb-3 rounded-md bg-zinc-100 p-2.5 text-xs dark:bg-zinc-900">
            {t('automation.triggered', { name: feedback.name, status: feedback.status })}
            {feedback.reasonCode && t('automation.triggeredReason', { code: feedback.reasonCode })}
            {feedback.taskId && (
              <span className="ml-1">{t('automation.triggeredTask', { taskId: shortId(feedback.taskId) })}</span>
            )}
          </div>
        )}

        {autopilots.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
        {autopilots.error && (
          <p className="text-sm text-red-600 dark:text-red-400">{autopilots.error.message}</p>
        )}
        {autopilots.data && autopilots.data.length === 0 && (
          <p className="text-sm text-zinc-500">{t('automation.empty')}</p>
        )}

        {actionError && (
          <p className="mt-2 text-xs text-red-600 dark:text-red-400">
            {blockedKey ? t(blockedKey) : actionError.message}
          </p>
        )}

        {autopilots.data && autopilots.data.length > 0 && (
          <div className="divide-y divide-zinc-200 dark:divide-zinc-800">
            {autopilots.data.map((a) => {
              const status = autopilotStatusKey(a)
              const confirming = confirmingId === a.id
              return (
                <div key={a.id} className="py-2.5" data-testid="autopilot-row">
                  <div className="flex items-center gap-3">
                    <div className="min-w-0 flex-1">
                      <span className="text-sm font-medium">{a.name}</span>
                      <span className="ml-2 text-xs text-zinc-500 dark:text-zinc-400">
                        {EXEC_MODE_KEYS[a.exec_mode] ? t(EXEC_MODE_KEYS[a.exec_mode]) : a.exec_mode}
                      </span>
                      {a.target_agent_id && (
                        <span className="ml-2 text-xs text-zinc-500 dark:text-zinc-400">
                          → {agentName(a.target_agent_id)}
                        </span>
                      )}
                    </div>
                    <Badge variant={status.tone}>{t(status.key)}</Badge>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={!a.enabled || trigger.isPending}
                      onClick={() => trigger.mutate(a.id)}
                    >
                      {t('automation.triggerNow')}
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={toggle.isPending}
                      onClick={() => toggle.mutate(a)}
                    >
                      {a.enabled ? t('automation.disable') : t('automation.enable')}
                    </Button>
                    {confirming ? (
                      <div className="flex items-center gap-1">
                        <Button
                          variant="danger"
                          size="sm"
                          disabled={remove.isPending}
                          onClick={() => remove.mutate(a.id)}
                        >
                          {t('automation.confirmDelete')}
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => setConfirmingId(null)}>
                          {t('common.cancel')}
                        </Button>
                      </div>
                    ) : (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-red-600 dark:text-red-400"
                        onClick={() => setConfirmingId(a.id)}
                      >
                        {t('common.delete')}
                      </Button>
                    )}
                  </div>
                  {a.paused_reason && (
                    <p className={cn('mt-1 text-xs text-red-600 dark:text-red-400')}>
                      {t('automation.fusedNote', { reason: a.paused_reason })}
                    </p>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </section>

      <WakeupsSection />
      <CreateAutopilotDialog open={createOpen} onOpenChange={setCreateOpen} />
    </div>
  )
}
export function CreateAutopilotDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [execMode, setExecMode] = useState('create_work_item')
  const [prompt, setPrompt] = useState('')
  const [agentId, setAgentId] = useState('')
  const [cron, setCron] = useState('')
  const [timezone, setTimezone] = useState('')
  const [workItemId, setWorkItemId] = useState('')
  const agents = useQuery({ queryKey: ['agents'], queryFn: () => listAgents(), staleTime: 60000 })
  const workItems = useQuery({
    queryKey: ['work-items', 'automation-options'],
    queryFn: () => listWorkItems({ limit: 100 }),
    staleTime: 60000,
    enabled: execMode === 'task_only',
  })
  const cronOk = cron.trim() === '' || /^\S+\s+\S+\s+\S+\s+\S+\s+\S+$/.test(cron.trim())
  const canSubmit =
    name.trim() !== '' && agentId !== '' && cronOk && (execMode !== 'task_only' || workItemId !== '')
  const reset = () => {
    setName('')
    setExecMode('create_work_item')
    setPrompt('')
    setAgentId('')
    setCron('')
    setTimezone('')
    setWorkItemId('')
    create.reset()
  }
  // 坏 cron 会让后端 createAutopilot 500（非 400），所以先打 cron-preview 用真源拦下。
  const create = useMutation({
    mutationFn: async () => {
      const expr = cron.trim()
      if (expr !== '') await cronPreview(expr, timezone)
      const template: Record<string, unknown> =
        execMode === 'task_only'
          ? { work_item_id: workItemId }
          : prompt.trim() === ''
            ? {}
            : { title: prompt.trim(), description: prompt.trim() }
      return createAutopilot({
        name: name.trim(),
        exec_mode: execMode,
        template,
        target_agent_id: agentId,
        cron_expression: expr,
        timezone,
      })
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['autopilots'] })
      onOpenChange(false)
      reset()
    },
  })
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Popup className="fixed top-1/2 left-1/2 z-50 max-h-[85vh] w-full max-w-md -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-lg border border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
          <Dialog.Title className="mb-4 text-sm font-semibold">{t('automation.newTitle')}</Dialog.Title>
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault()
              if (canSubmit && !create.isPending) create.mutate()
            }}
          >
            <div>
              <label htmlFor="ap-name" className={LABEL_CLASS}>
                {t('automation.nameLabel')}
              </label>
              <Input
                id="ap-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={100}
                autoFocus
                placeholder={t('automation.namePlaceholder')}
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <label htmlFor="ap-mode" className={LABEL_CLASS}>
                  {t('automation.execModeLabel')}
                </label>
                <select
                  id="ap-mode"
                  value={execMode}
                  onChange={(e) => setExecMode(e.target.value)}
                  className={SELECT_CLASS}
                >
                  <option value="create_work_item">{t('automation.modeCreate')}</option>
                  <option value="task_only">{t('automation.modeTaskOnly')}</option>
                </select>
              </div>
              <div>
                <label htmlFor="ap-agent" className={LABEL_CLASS}>
                  {t('automation.targetAgentLabel')}
                </label>
                <select
                  id="ap-agent"
                  value={agentId}
                  onChange={(e) => setAgentId(e.target.value)}
                  className={SELECT_CLASS}
                  required
                >
                  <option value="" disabled>
                    {agents.isPending ? t('automation.agentsLoading') : t('automation.targetAgentPick')}
                  </option>
                  {(agents.data ?? []).map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name}
                    </option>
                  ))}
                </select>
              </div>
            </div>
            {execMode === 'task_only' ? (
              <div>
                <label htmlFor="ap-workitem" className={LABEL_CLASS}>
                  {t('automation.workItemLabel')}
                </label>
                <select
                  id="ap-workitem"
                  value={workItemId}
                  onChange={(e) => setWorkItemId(e.target.value)}
                  className={SELECT_CLASS}
                  required
                >
                  <option value="" disabled>
                    {workItems.isPending
                      ? t('common.loading')
                      : t('automation.workItemPick')}
                  </option>
                  {(workItems.data?.items ?? []).map((w: WorkItem) => (
                    <option key={w.id} value={w.id}>
                      #{w.number} {w.title}
                    </option>
                  ))}
                </select>
              </div>
            ) : (
              <div>
                <label htmlFor="ap-prompt" className={LABEL_CLASS}>
                  {t('automation.promptLabel')}
                </label>
                <textarea
                  id="ap-prompt"
                  value={prompt}
                  onChange={(e) => setPrompt(e.target.value)}
                  rows={3}
                  placeholder={t('automation.promptPlaceholder')}
                  className={TEXTAREA_CLASS}
                />
              </div>
            )}
            <div>
              <label htmlFor="ap-cron" className={LABEL_CLASS}>
                {t('automation.cronLabel')}
              </label>
              <Input
                id="ap-cron"
                value={cron}
                onChange={(e) => setCron(e.target.value)}
                placeholder="0 9 * * *"
                aria-invalid={!cronOk}
              />
              <p className={cn('mt-1 text-xs', cronOk ? 'text-zinc-500 dark:text-zinc-400' : 'text-red-600 dark:text-red-400')}>
                {cronOk ? t('automation.cronHint') : t('automation.cronInvalid')}
              </p>
            </div>
            <div>
              <label htmlFor="ap-tz" className={LABEL_CLASS}>
                {t('automation.timezoneLabel')}
              </label>
              <select
                id="ap-tz"
                value={timezone}
                onChange={(e) => setTimezone(e.target.value)}
                className={SELECT_CLASS}
              >
                {TIMEZONES.map((tz) => (
                  <option key={tz || 'utc'} value={tz}>
                    {tz === '' ? t('automation.tzUtc') : tz}
                  </option>
                ))}
              </select>
            </div>
            {create.error && (
              <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                {create.error.message}
              </p>
            )}
            <div className="flex justify-end gap-2 pt-1">
              <Dialog.Close className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-800">
                {t('common.cancel')}
              </Dialog.Close>
              <Button type="submit" disabled={!canSubmit || create.isPending}>
                {create.isPending ? t('automation.creating') : t('common.create')}
              </Button>
            </div>
          </form>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
export function WakeupsSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const wakeups = useQuery({ queryKey: ['wakeups'], queryFn: listWorkspaceWakeups })
  const workItems = useQuery({
    queryKey: ['work-items', 'automation-options'],
    queryFn: () => listWorkItems({ limit: 100 }),
    staleTime: 60000,
  })
  const [confirmingId, setConfirmingId] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['wakeups'] })
  const remove = useMutation({
    mutationFn: (w: Wakeup) => deleteWakeup(w.work_item_id, w.id),
    onSuccess: () => {
      setConfirmingId(null)
      void invalidate()
    },
  })
  const wiTitle = (id: string) => {
    const w = workItems.data?.items.find((x) => x.id === id)
    return w ? `#${w.number} ${w.title}` : t('automation.wakeWorkItemShort', { id: shortId(id) })
  }
  return (
    <section className={CARD_CLASS}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className={CARD_TITLE_CLASS}>{t('automation.wakeTitle')}</h2>
          <p className={CARD_DESC_CLASS}>{t('automation.wakeDesc')}</p>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          {t('automation.wakeNewBtn')}
        </Button>
      </div>

      {wakeups.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
      {wakeups.error && (
        <p className="text-sm text-red-600 dark:text-red-400">{wakeups.error.message}</p>
      )}
      {wakeups.data && wakeups.data.length === 0 && (
        <p className="text-sm text-zinc-500">{t('automation.wakeEmpty')}</p>
      )}
      {remove.error && (
        <p className="mt-2 text-xs text-red-600 dark:text-red-400">{remove.error.message}</p>
      )}

      {wakeups.data && wakeups.data.length > 0 && (
        <div className="divide-y divide-zinc-200 dark:divide-zinc-800">
          {wakeups.data.map((w) => {
            const kindKey = WAKEUP_KIND_KEYS[w.kind]
            const system = w.system_rule !== '' && w.system_rule !== undefined
            const confirming = confirmingId === w.id
            return (
              <div key={w.id} className="py-2.5" data-testid="wakeup-row">
                <div className="flex items-center gap-3">
                  <Badge variant={system ? 'neutral' : 'secondary'}>
                    {kindKey ? t(kindKey) : w.kind}
                  </Badge>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm" title={w.instruction}>
                      {w.instruction}
                    </p>
                    <p className="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">
                      {wiTitle(w.work_item_id)}
                      {w.cron_expression ? ` · cron ${w.cron_expression}` : ''}
                      {w.interval_seconds ? ` · ${t('automation.wakeEvery', { seconds: w.interval_seconds })}` : ''}
                      {w.next_fire_at ? ` · ${t('automation.wakeNext', { time: fmtTime(w.next_fire_at) })}` : ''}
                      {!w.enabled && w.kind !== 'system' ? ` · ${t('automation.stDisabled')}` : ''}
                    </p>
                  </div>
                  {system ? (
                    <span className="text-xs text-zinc-500 dark:text-zinc-400">
                      {t('automation.wakeSystemNote')}
                    </span>
                  ) : confirming ? (
                    <div className="flex items-center gap-1">
                      <Button
                        variant="danger"
                        size="sm"
                        disabled={remove.isPending}
                        onClick={() => remove.mutate(w)}
                      >
                        {t('automation.confirmDelete')}
                      </Button>
                      <Button variant="ghost" size="sm" onClick={() => setConfirmingId(null)}>
                        {t('common.cancel')}
                      </Button>
                    </div>
                  ) : (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-red-600 dark:text-red-400"
                      onClick={() => setConfirmingId(w.id)}
                    >
                      {t('common.delete')}
                    </Button>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}
      <CreateWakeupDialog open={createOpen} onOpenChange={setCreateOpen} />
    </section>
  )
}
function CreateWakeupDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [workItemId, setWorkItemId] = useState('')
  const [instruction, setInstruction] = useState('')
  const [fireAt, setFireAt] = useState('')
  const [pastTime, setPastTime] = useState(false)
  const workItems = useQuery({
    queryKey: ['work-items', 'automation-options'],
    queryFn: () => listWorkItems({ limit: 100 }),
    staleTime: 60000,
  })
  const parsed = fireAt === '' ? null : datetimeLocalToCron(fireAt)
  const canSubmit = workItemId !== '' && instruction.trim() !== '' && parsed !== null && !pastTime
  // 过去时刻检查放在事件处理器里（Date.now 不允许在 render 期间调用）
  const changeFireAt = (value: string) => {
    setFireAt(value)
    const p = value === '' ? null : datetimeLocalToCron(value)
    setPastTime(p !== null && p.at.getTime() <= Date.now())
  }
  const reset = () => {
    setWorkItemId('')
    setInstruction('')
    setFireAt('')
    create.reset()
  }
  const create = useMutation({
    mutationFn: () =>
      createWakeup(workItemId, {
        kind: 'time',
        instruction: instruction.trim(),
        cron_expression: parsed?.cron ?? '',
        // datetime-local 是浏览器本地时间；cron 字段按这个 IANA 时区解释。
        timezone: browserTimezone(),
        max_fires: 1,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['wakeups'] })
      onOpenChange(false)
      reset()
    },
  })
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Popup className="fixed top-1/2 left-1/2 z-50 w-full max-w-md -translate-x-1/2 -translate-y-1/2 rounded-lg border border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
          <Dialog.Title className="mb-4 text-sm font-semibold">{t('automation.wakeNewTitle')}</Dialog.Title>
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault()
              if (canSubmit && !create.isPending) create.mutate()
            }}
          >
            <div>
              <label htmlFor="wu-workitem" className={LABEL_CLASS}>
                {t('automation.wakeWorkItemLabel')}
              </label>
              <select
                id="wu-workitem"
                value={workItemId}
                onChange={(e) => setWorkItemId(e.target.value)}
                className={SELECT_CLASS}
                required
              >
                <option value="" disabled>
                  {workItems.isPending ? t('common.loading') : t('automation.workItemPick')}
                </option>
                {(workItems.data?.items ?? []).map((w: WorkItem) => (
                  <option key={w.id} value={w.id}>
                    #{w.number} {w.title}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label htmlFor="wu-instruction" className={LABEL_CLASS}>
                {t('automation.wakeInstructionLabel')}
              </label>
              <textarea
                id="wu-instruction"
                value={instruction}
                onChange={(e) => setInstruction(e.target.value)}
                rows={3}
                maxLength={4000}
                placeholder={t('automation.wakeInstructionPlaceholder')}
                className={TEXTAREA_CLASS}
              />
            </div>
            <div>
              <label htmlFor="wu-fireat" className={LABEL_CLASS}>
                {t('automation.wakeTimeLabel')}
              </label>
              <Input
                id="wu-fireat"
                type="datetime-local"
                value={fireAt}
                onChange={(e) => changeFireAt(e.target.value)}
                aria-invalid={pastTime}
              />
              {pastTime && (
                <p className="mt-1 text-xs text-red-600 dark:text-red-400">
                  {t('automation.wakePastTime')}
                </p>
              )}
            </div>
            {create.error && (
              <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                {create.error.message}
              </p>
            )}
            <div className="flex justify-end gap-2 pt-1">
              <Dialog.Close className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-800">
                {t('common.cancel')}
              </Dialog.Close>
              <Button type="submit" disabled={!canSubmit || create.isPending}>
                {create.isPending ? t('automation.creating') : t('common.create')}
              </Button>
            </div>
          </form>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

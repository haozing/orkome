import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { isApiError } from '@ploykit/client'
import {
  FormField,
  PageHeader,
  toast,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
  useConfirm,
} from '@ploykit/ui'
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
import { TaskStatusBadge } from '@/components/task-status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { Textarea } from '@/components/ui/textarea'
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
// 卡片内三态压缩纵向留白（states 组合件默认 pt-24 是整页语境）。
const SECTION_STATES_CLASS = 'pt-2'
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
export function AutomationPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const autopilots = useQuery({ queryKey: ['autopilots'], queryFn: listAutopilots })
  const agents = useQuery({ queryKey: ['agents'], queryFn: () => listAgents(), staleTime: 60000 })
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
      // ui-sweep：成功反馈走全站 toast；run 状态以 Badge 呈现（不再灰条直出
      // 原始枚举/原因码拼接），原因码只在失败终态的行详情里有意义。
      toast.success(t('automation.triggered', { name: ap?.name ?? run.autopilot_id }), {
        description: <TaskStatusBadge status={run.status} />,
      })
      void invalidate()
    },
  })
  const remove = useMutation({
    mutationFn: deleteAutopilot,
    onSuccess: () => void invalidate(),
  })
  // ui-sweep R6：删除是不可逆操作，统一走全站确认弹窗（kit useConfirm），
  // description 必须交代不可逆后果。
  const askRemove = async (a: Autopilot) => {
    const ok = await confirm({
      title: t('automation.deleteApTitle', { name: a.name }),
      description: t('automation.deleteConfirmDesc'),
      confirmText: t('automation.confirmDelete'),
      cancelText: t('common.cancel'),
      danger: true,
    })
    if (ok) remove.mutate(a.id)
  }
  const actionError = toggle.error ?? trigger.error ?? remove.error
  const blockedKey = remove.error ? deleteBlockedKey(remove.error) : null
  return (
    <div className="space-y-6">
      <PageHeader title={t('automation.pageTitle')} description={t('automation.intro')} />

      <section className={CARD_CLASS}>
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <h2 className={CARD_TITLE_CLASS}>{t('automation.mineTitle')}</h2>
          </div>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            {t('automation.newBtn')}
          </Button>
        </div>

        {autopilots.isPending && <PageLoading className={SECTION_STATES_CLASS} />}
        {autopilots.error && (
          <PageError
            message={autopilots.error.message}
            onRetry={() => void autopilots.refetch()}
            className={SECTION_STATES_CLASS}
          />
        )}
        {autopilots.data && autopilots.data.length === 0 && (
          <PageEmpty title={t('automation.empty')} className={SECTION_STATES_CLASS} />
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
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-red-600 dark:text-red-400"
                      onClick={() => void askRemove(a)}
                    >
                      {t('common.delete')}
                    </Button>
                  </div>
                  {a.paused_reason && (
                    <p className="mt-1 text-xs text-red-600 dark:text-red-400">
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
  // 时区默认浏览器 IANA 名（对齐 wakeup 弹窗先例）；不在常用清单里就置顶插入。
  const [timezone, setTimezone] = useState(() => browserTimezone())
  const [workItemId, setWorkItemId] = useState('')
  const agents = useQuery({ queryKey: ['agents'], queryFn: () => listAgents(), staleTime: 60000 })
  const workItems = useQuery({
    queryKey: ['work-items', 'automation-options'],
    queryFn: () => listWorkItems({ limit: 100 }),
    staleTime: 60000,
    enabled: execMode === 'task_only',
  })
  const cronExpr = cron.trim()
  const cronOk = cronExpr === '' || /^\S+\s+\S+\s+\S+\s+\S+\s+\S+$/.test(cronExpr)
  // 「下次触发时间」实时预览：与提交前校验同源（cron-preview 真源），坏 cron
  // 时不展示也不拦提交——拦截仍由 create.mutate 里的前置 preview 负责。
  const nextRunAt = useQuery({
    queryKey: ['cron-preview', cronExpr, timezone],
    queryFn: () => cronPreview(cronExpr, timezone, 1),
    enabled: cronExpr !== '' && cronOk,
    retry: false,
  })
  const canSubmit =
    name.trim() !== '' && agentId !== '' && cronOk && (execMode !== 'task_only' || workItemId !== '')
  const reset = () => {
    setName('')
    setExecMode('create_work_item')
    setPrompt('')
    setAgentId('')
    setCron('')
    setTimezone(browserTimezone())
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
  const tzOptions = TIMEZONES.includes(timezone) ? TIMEZONES : [timezone, ...TIMEZONES]
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('automation.newTitle')}</DialogTitle>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (canSubmit && !create.isPending) create.mutate()
          }}
        >
          <FormField label={t('automation.nameLabel')} htmlFor="ap-name">
            <Input
              id="ap-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={100}
              autoFocus
              placeholder={t('automation.namePlaceholder')}
            />
          </FormField>
          <div className="grid grid-cols-2 gap-3">
            <FormField label={t('automation.execModeLabel')} htmlFor="ap-mode">
              <Select
                id="ap-mode"
                value={execMode}
                onChange={(e) => setExecMode(e.target.value)}
              >
                <option value="create_work_item">{t('automation.modeCreate')}</option>
                <option value="task_only">{t('automation.modeTaskOnly')}</option>
              </Select>
            </FormField>
            <FormField label={t('automation.targetAgentLabel')} htmlFor="ap-agent">
              <Select
                id="ap-agent"
                value={agentId}
                onChange={(e) => setAgentId(e.target.value)}
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
              </Select>
            </FormField>
          </div>
          {execMode === 'task_only' ? (
            <FormField label={t('automation.workItemLabel')} htmlFor="ap-workitem">
              <Select
                id="ap-workitem"
                value={workItemId}
                onChange={(e) => setWorkItemId(e.target.value)}
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
              </Select>
            </FormField>
          ) : (
            <FormField label={t('automation.promptLabel')} htmlFor="ap-prompt">
              <Textarea
                id="ap-prompt"
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                rows={3}
                placeholder={t('automation.promptPlaceholder')}
              />
            </FormField>
          )}
          <FormField
            label={t('automation.cronLabel')}
            htmlFor="ap-cron"
            description={cronOk ? t('automation.cronHint') : undefined}
            error={cronOk ? undefined : t('automation.cronInvalid')}
          >
            <Input
              id="ap-cron"
              value={cron}
              onChange={(e) => setCron(e.target.value)}
              placeholder="0 9 * * *"
            />
          </FormField>
          {nextRunAt.data?.length ? (
            <p className="text-xs text-zinc-500 dark:text-zinc-400">
              {t('automation.cronNext', { time: fmtTime(nextRunAt.data[0]) })}
            </p>
          ) : null}
          <FormField label={t('automation.timezoneLabel')} htmlFor="ap-tz">
            <Select
              id="ap-tz"
              value={timezone}
              onChange={(e) => setTimezone(e.target.value)}
            >
              {tzOptions.map((tz) => (
                <option key={tz || 'utc'} value={tz}>
                  {tz === '' ? t('automation.tzUtc') : tz}
                </option>
              ))}
            </Select>
          </FormField>
          {create.error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {create.error.message}
            </p>
          )}
          <DialogFooter>
            <DialogClose render={<Button variant="outline" />}>{t('common.cancel')}</DialogClose>
            <Button type="submit" disabled={!canSubmit || create.isPending}>
              {create.isPending ? t('automation.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
export function WakeupsSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const wakeups = useQuery({ queryKey: ['wakeups'], queryFn: listWorkspaceWakeups })
  const workItems = useQuery({
    queryKey: ['work-items', 'automation-options'],
    queryFn: () => listWorkItems({ limit: 100 }),
    staleTime: 60000,
  })
  const [createOpen, setCreateOpen] = useState(false)
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['wakeups'] })
  const remove = useMutation({
    mutationFn: (w: Wakeup) => deleteWakeup(w.work_item_id, w.id),
    onSuccess: () => void invalidate(),
  })
  const askRemove = async (w: Wakeup) => {
    const ok = await confirm({
      title: t('automation.wakeDeleteTitle'),
      description: t('automation.wakeDeleteDesc'),
      confirmText: t('automation.confirmDelete'),
      cancelText: t('common.cancel'),
      danger: true,
    })
    if (ok) remove.mutate(w)
  }
  const wiTitle = (id: string) => {
    const w = workItems.data?.items.find((x) => x.id === id)
    return w ? `#${w.number} ${w.title}` : t('automation.wakeWorkItemShort', { id: shortId(id) })
  }
  return (
    <section className={CARD_CLASS}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className={CARD_TITLE_CLASS}>{t('automation.wakeTitle')}</h2>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          {t('automation.wakeNewBtn')}
        </Button>
      </div>

      {wakeups.isPending && <PageLoading className={SECTION_STATES_CLASS} />}
      {wakeups.error && (
        <PageError
          message={wakeups.error.message}
          onRetry={() => void wakeups.refetch()}
          className={SECTION_STATES_CLASS}
        />
      )}
      {wakeups.data && wakeups.data.length === 0 && (
        <PageEmpty title={t('automation.wakeEmpty')} className={SECTION_STATES_CLASS} />
      )}
      {remove.error && (
        <p className="mt-2 text-xs text-red-600 dark:text-red-400">{remove.error.message}</p>
      )}

      {wakeups.data && wakeups.data.length > 0 && (
        <div className="divide-y divide-zinc-200 dark:divide-zinc-800">
          {wakeups.data.map((w) => {
            const kindKey = WAKEUP_KIND_KEYS[w.kind]
            const system = w.system_rule !== '' && w.system_rule !== undefined
            return (
              <div key={w.id} className="py-2.5" data-testid="wakeup-row">
                <div className="flex items-center gap-3">
                  <Badge variant={system ? 'neutral' : 'secondary'}>
                    {kindKey ? t(kindKey) : w.kind}
                  </Badge>
                  <div className="min-w-0 flex-1">
                    {/* 截断提示：原生 title → kit Tooltip（ui-sweep 裁决） */}
                    <Tooltip>
                      <TooltipTrigger render={<p className="truncate text-sm" />}>
                        {w.instruction}
                      </TooltipTrigger>
                      <TooltipContent>{w.instruction}</TooltipContent>
                    </Tooltip>
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
                  ) : (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-red-600 dark:text-red-400"
                      onClick={() => void askRemove(w)}
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
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      {/* 与 CreateAutopilotDialog 对齐：限高+滚动，长表单不溢出视口 */}
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('automation.wakeNewTitle')}</DialogTitle>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (canSubmit && !create.isPending) create.mutate()
          }}
        >
          <FormField label={t('automation.wakeWorkItemLabel')} htmlFor="wu-workitem">
            <Select
              id="wu-workitem"
              value={workItemId}
              onChange={(e) => setWorkItemId(e.target.value)}
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
            </Select>
          </FormField>
          <FormField label={t('automation.wakeInstructionLabel')} htmlFor="wu-instruction">
            <Textarea
              id="wu-instruction"
              value={instruction}
              onChange={(e) => setInstruction(e.target.value)}
              rows={3}
              maxLength={4000}
              placeholder={t('automation.wakeInstructionPlaceholder')}
            />
          </FormField>
          <FormField
            label={t('automation.wakeTimeLabel')}
            htmlFor="wu-fireat"
            error={pastTime ? t('automation.wakePastTime') : undefined}
          >
            <Input
              id="wu-fireat"
              type="datetime-local"
              value={fireAt}
              onChange={(e) => changeFireAt(e.target.value)}
            />
          </FormField>
          {create.error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {create.error.message}
            </p>
          )}
          <DialogFooter>
            <DialogClose render={<Button variant="outline" />}>{t('common.cancel')}</DialogClose>
            <Button type="submit" disabled={!canSubmit || create.isPending}>
              {create.isPending ? t('automation.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '@ploykit/client'
import { listAgents } from '@/api/orchestration'
import { listSquads } from '@/api/squads'
import { listStatuses, type WorkItemStatus } from '@/api/work'
import { statusMetaOf } from '@/components/work-item/meta'
import { Button } from '@/components/ui/button'
import { PageLoading } from '@/components/ui/states'
import {
  deleteWorkflow,
  getWorkflow,
  setWorkflowEnabled,
  upsertWorkflow,
  type WorkflowDefinition,
  type WorkflowExecutorType,
  type WorkflowStage,
  type WorkflowTriggerMode,
  type WorkflowUpsertInput,
} from '@/api/workflow'
import { cn } from '@/lib/utils'

// 项目流程编辑器（设计 §6.2 触点 1 / 契约 §6 W1 前端交付）。
// 阶段卡片本地编辑（position 重排只在内存），保存时按契约 §3 形状整体 PUT。
// 后端 W1 并行开发中：api/workflow.ts 置 USE_MOCK=true 即可用本地假数据联 UI。

const CARD_CLASS = 'rounded-lg border border-zinc-200 p-4 dark:border-zinc-800'
const LABEL_CLASS = 'mb-1 block text-xs text-zinc-500'
const INPUT_CLASS =
  'border-input focus-visible:border-ring dark:bg-input/30 h-8 w-full rounded-md border bg-transparent px-2 text-sm outline-none aria-invalid:border-red-500'
const SELECT_CLASS =
  'h-8 w-full rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900'
const TEXTAREA_CLASS =
  'w-full resize-y rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm outline-none focus:border-zinc-400 dark:border-zinc-700 dark:bg-zinc-900'
const CHECK_CLASS = 'size-3.5 shrink-0 accent-zinc-700 dark:accent-zinc-300'

interface EditorStage {
  /** 本地编辑键；提交时重算 position，不随请求发出。 */
  key: string
  name: string
  exitStatusKey: string
  executorType: WorkflowExecutorType
  executorId: string
  instruction: string
}

let stageKeySeq = 0
const nextStageKey = () => `stage-${++stageKeySeq}`

function stageFromDefinition(s: WorkflowStage): EditorStage {
  return {
    key: nextStageKey(),
    name: s.name,
    exitStatusKey: s.exit_status_key,
    executorType: s.executor_type,
    executorId: s.executor_id ?? '',
    instruction: s.instruction,
  }
}

function blankStage(): EditorStage {
  return {
    key: nextStageKey(),
    name: '',
    exitStatusKey: 'todo',
    executorType: 'none',
    executorId: '',
    instruction: '',
  }
}

type StageErrorKind = 'nameRequired' | 'executorRequired'

/** 前端预校验：挡空阶段名与 agent/squad 未选 executor_id（后端错误另见 save.error）。 */
function validateStages(stages: EditorStage[]): Map<string, StageErrorKind> {
  const errs = new Map<string, StageErrorKind>()
  for (const s of stages) {
    if (!s.name.trim()) errs.set(s.key, 'nameRequired')
    else if (s.executorType !== 'none' && !s.executorId.trim()) errs.set(s.key, 'executorRequired')
  }
  return errs
}

/** 编辑器数据体：loading/error 已由外层裁掉，这里拿到的 initial 即查询单次结果。 */
export function WorkflowEditor({ projectId }: { projectId: string }) {
  const { t } = useTranslation()
  const workflow = useQuery({
    queryKey: ['project-workflow', projectId],
    queryFn: () => getWorkflow(projectId),
  })

  if (workflow.isPending) {
    return <PageLoading label={t('workflow.loading')} className="pt-10" />
  }
  if (workflow.isError) {
    return (
      <div role="alert" className="space-y-1 py-8 text-center">
        <p className="text-sm font-medium text-red-600 dark:text-red-400">
          {t('workflow.loadFailed')}
        </p>
        <p className="text-xs break-all text-zinc-500 dark:text-zinc-400">
          {workflow.error instanceof ApiError ? workflow.error.message : t('common.requestFailed')}
        </p>
      </div>
    )
  }
  // key 随查询结果翻新：删除流程回到空态、或外部失效重取后，编辑区从服务器值重置。
  return (
    <EditorBody
      key={workflow.data === null ? 'empty' : workflow.data.id}
      projectId={projectId}
      initial={workflow.data}
    />
  )
}

function EditorBody({
  projectId,
  initial,
}: {
  projectId: string
  initial: WorkflowDefinition | null
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [name, setName] = useState(initial?.name ?? '')
  const [triggerMode, setTriggerMode] = useState<WorkflowTriggerMode>(
    initial?.trigger_mode ?? 'auto',
  )
  const [entryPosition, setEntryPosition] = useState(initial?.entry_stage_position ?? 1)
  const [enabled, setEnabled] = useState(initial?.enabled ?? true)
  const [stages, setStages] = useState<EditorStage[]>(() =>
    initial ? initial.stages.map(stageFromDefinition) : [],
  )
  const [stageErrors, setStageErrors] = useState<Map<string, StageErrorKind>>(new Map())
  const [creating, setCreating] = useState(initial === null && stages.length > 0)
  const [confirmDelete, setConfirmDelete] = useState(false)

  const statuses = useQuery({
    queryKey: ['work-item-statuses'],
    queryFn: listStatuses,
    staleTime: 60000,
  })
  const agents = useQuery({ queryKey: ['agents'], queryFn: listAgents, staleTime: 60000 })
  const squads = useQuery({ queryKey: ['squads'], queryFn: listSquads, staleTime: 60000 })

  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: ['project-workflow', projectId] })

  const statusName = (key: string) => {
    const def = statuses.data?.find((s) => s.key === key)
    return def ? (def.is_builtin ? t(statusMetaOf(def.key).labelKey) : def.name) : key
  }
  // closed 类不能作为阶段出口（契约：应用层校验 category ∈ unstarted|started|done）。
  const exitStatusOptions = useMemo(
    () => (statuses.data ?? []).filter((s) => s.category !== 'closed'),
    [statuses.data],
  )

  const save = useMutation({
    mutationFn: (input: WorkflowUpsertInput) => upsertWorkflow(projectId, input),
    onSuccess: () => {
      setStageErrors(new Map())
      setCreating(false)
      invalidate()
    },
  })
  const remove = useMutation({
    mutationFn: () => deleteWorkflow(projectId),
    onSuccess: () => {
      setConfirmDelete(false)
      // EditorBody 的 key 绑定 workflow.data，失效后回到空态分支。
      invalidate()
    },
  })
  const toggleEnabled = useMutation({
    mutationFn: (next: boolean) => setWorkflowEnabled(projectId, next),
    onSuccess: (def) => {
      setEnabled(def.enabled)
      invalidate()
    },
  })

  const submit = () => {
    const errs = validateStages(stages)
    if (errs.size > 0) {
      setStageErrors(errs)
      return
    }
    setStageErrors(new Map())
    save.mutate({
      trigger_mode: triggerMode,
      entry_stage_position: Math.min(Math.max(entryPosition, 1), Math.max(stages.length, 1)),
      stages: stages.map((s, i) => ({
        position: i + 1,
        name: s.name.trim(),
        exit_status_key: s.exitStatusKey,
        executor_type: s.executorType,
        ...(s.executorType === 'none' ? {} : { executor_id: s.executorId.trim() }),
        instruction: s.instruction,
      })),
    })
  }

  const patchStage = (key: string, patch: Partial<EditorStage>) => {
    setStages((prev) => prev.map((s) => (s.key === key ? { ...s, ...patch } : s)))
    setStageErrors((prev) => {
      if (!prev.has(key)) return prev
      const next = new Map(prev)
      next.delete(key)
      return next
    })
  }
  const addStage = () => setStages((prev) => [...prev, blankStage()])
  const removeStage = (key: string) => {
    setStages((prev) => prev.filter((s) => s.key !== key))
    setStageErrors((prev) => {
      if (!prev.has(key)) return prev
      const next = new Map(prev)
      next.delete(key)
      return next
    })
  }
  const moveStage = (index: number, dir: -1 | 1) => {
    setStages((prev) => {
      const target = index + dir
      if (target < 0 || target >= prev.length) return prev
      const next = [...prev]
      const moved = next[index]
      if (!moved) return prev
      next.splice(index, 1)
      next.splice(target, 0, moved)
      return next
    })
    // 入口标记按 1 起的 position 跟随原阶段换位，避免换序后指向错误的格子。
    setEntryPosition((entry) => {
      const target = index + dir
      if (entry === index + 1) return target + 1
      if (entry === target + 1) return index + 1
      return entry
    })
  }

  const backendError = (() => {
    if (save.error) return save.error instanceof ApiError ? save.error.message : t('common.saveFailed')
    if (remove.error)
      return remove.error instanceof ApiError ? remove.error.message : t('workflow.deleteFailed')
    return null
  })()

  const stageListProps: StageListProps = {
    stages,
    stageErrors,
    exitStatusOptions,
    statusName,
    agents: agents.data ?? [],
    squads: squads.data ?? [],
    entryPosition,
    onPatch: patchStage,
    onRemove: removeStage,
    onMove: moveStage,
    onEntry: setEntryPosition,
  }

  if (initial === null && !creating) {
    // 空态：引导文案 + 创建入口（点后直接落编辑区，保存走 upsert = 创建）。
    return (
      <div className="flex flex-col items-center gap-3 py-8 text-center">
        <p className="max-w-sm text-sm text-zinc-500 dark:text-zinc-400">{t('workflow.emptyDesc')}</p>
        <Button
          size="sm"
          onClick={() => {
            setStages((prev) => (prev.length > 0 ? prev : [blankStage()]))
            setCreating(true)
          }}
        >
          <Plus className="size-4" aria-hidden="true" />
          {t('workflow.createAction')}
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {initial !== null && (
        // 顶部卡：流程名 + 触发模式 + 启停（创建中的空流程不渲染——name 由后端管理）。
        <div className={cn(CARD_CLASS, 'space-y-3')}>
          <div className="flex flex-wrap items-end gap-3">
            <div className="min-w-48 flex-1">
              <label htmlFor="workflow-name" className={LABEL_CLASS}>
                {t('workflow.nameLabel')}
              </label>
              <input
                id="workflow-name"
                value={name}
                maxLength={100}
                onChange={(e) => setName(e.target.value)}
                className={INPUT_CLASS}
              />
            </div>
            <div className="w-40">
              <label htmlFor="workflow-trigger" className={LABEL_CLASS}>
                {t('workflow.triggerMode')}
              </label>
              <select
                id="workflow-trigger"
                value={triggerMode}
                onChange={(e) => setTriggerMode(e.target.value as WorkflowTriggerMode)}
                className={SELECT_CLASS}
              >
                <option value="auto">{t('workflow.triggerAuto')}</option>
                <option value="manual">{t('workflow.triggerManual')}</option>
              </select>
            </div>
            <div className="flex items-center gap-2 pb-0.5">
              <input
                type="checkbox"
                id="workflow-enabled"
                checked={enabled}
                disabled={toggleEnabled.isPending}
                onChange={(e) => toggleEnabled.mutate(e.target.checked)}
                className={CHECK_CLASS}
              />
              <label htmlFor="workflow-enabled" className="text-sm">
                {t('workflow.enabled')}
              </label>
            </div>
          </div>
          {toggleEnabled.error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {toggleEnabled.error instanceof ApiError
                ? toggleEnabled.error.message
                : t('workflow.enableFailed')}
            </p>
          )}
        </div>
      )}

      <StageList {...stageListProps} />

      {stageErrors.size > 0 && (
        <p role="alert" className="text-xs text-red-600 dark:text-red-400">
          {t('workflow.stage.fixErrors')}
        </p>
      )}

      <div className="flex flex-wrap items-center gap-2">
        <Button variant="outline" size="sm" onClick={addStage}>
          <Plus className="size-3.5" aria-hidden="true" />
          {t('workflow.stage.add')}
        </Button>
        <span className="flex-1" />
        {initial !== null && (
          <Button
            variant="outline"
            size="sm"
            disabled={remove.isPending}
            onClick={() => setConfirmDelete(true)}
          >
            <Trash2 className="size-3.5" aria-hidden="true" />
            {t('workflow.deleteAction')}
          </Button>
        )}
        <Button size="sm" disabled={save.isPending || stages.length === 0} onClick={submit}>
          {save.isPending ? t('common.saving') : t('common.save')}
        </Button>
      </div>

      {backendError && (
        <p role="alert" className="text-xs text-red-600 dark:text-red-400">
          {backendError}
        </p>
      )}
      {save.isSuccess && (
        <p className="text-xs text-emerald-600 dark:text-emerald-400">{t('workflow.saved')}</p>
      )}

      {confirmDelete && (
        <div className="rounded-lg border border-red-200 bg-red-50 p-3 dark:border-red-900 dark:bg-red-950/40">
          <p className="text-sm">{t('workflow.deleteConfirm')}</p>
          <div className="mt-2 flex gap-2">
            <Button
              size="sm"
              className="bg-red-600 text-white hover:bg-red-500"
              disabled={remove.isPending}
              onClick={() => remove.mutate()}
            >
              {remove.isPending ? t('workflow.deleting') : t('common.confirm')}
            </Button>
            <Button variant="outline" size="sm" onClick={() => setConfirmDelete(false)}>
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

// —— 阶段卡片列表（编辑区主体） ——

type StatusOption = WorkItemStatus

interface ExecutorOption {
  id: string
  name: string
  archived?: boolean
}

interface StageListProps {
  stages: EditorStage[]
  stageErrors: Map<string, StageErrorKind>
  exitStatusOptions: StatusOption[]
  statusName: (key: string) => string
  agents: ExecutorOption[]
  squads: ExecutorOption[]
  entryPosition: number
  onPatch: (key: string, patch: Partial<EditorStage>) => void
  onRemove: (key: string) => void
  onMove: (index: number, dir: -1 | 1) => void
  onEntry: (position: number) => void
}

function StageList(props: StageListProps) {
  const { stages } = props
  const { t } = useTranslation()
  if (stages.length === 0) {
    return <p className="py-3 text-center text-xs text-zinc-400">{t('workflow.stage.empty')}</p>
  }
  return (
    <ol className="space-y-3">
      {stages.map((stage, i) => (
        <StageCard
          key={stage.key}
          stage={stage}
          index={i}
          total={stages.length}
          {...props}
        />
      ))}
    </ol>
  )
}

function StageCard({
  stage,
  index,
  total,
  stageErrors,
  exitStatusOptions,
  statusName,
  agents,
  squads,
  entryPosition,
  onPatch,
  onRemove,
  onMove,
  onEntry,
}: StageListProps & { stage: EditorStage; index: number; total: number }) {
  const { t } = useTranslation()
  const stageError = stageErrors.get(stage.key)
  const isEntry = entryPosition === index + 1
  return (
    <li className={CARD_CLASS}>
      <div className="mb-2 flex items-center gap-2">
        <span className="flex size-5 shrink-0 items-center justify-center rounded bg-zinc-100 text-xs font-medium text-zinc-600 dark:bg-zinc-800 dark:text-zinc-300">
          {index + 1}
        </span>
        <label className="flex min-w-0 items-center gap-1.5 text-xs text-zinc-500">
          <input
            type="radio"
            name="workflow-entry-stage"
            checked={isEntry}
            onChange={() => onEntry(index + 1)}
            className={CHECK_CLASS}
            aria-label={t('workflow.stage.entryAria', { n: index + 1 })}
          />
          {t('workflow.stage.entryMark')}
        </label>
        <div className="ml-auto flex items-center gap-0.5">
          <Button
            variant="ghost"
            size="icon-xs"
            disabled={index === 0}
            aria-label={t('workflow.stage.moveUp')}
            onClick={() => onMove(index, -1)}
          >
            <ArrowUp className="size-3.5" aria-hidden="true" />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            disabled={index === total - 1}
            aria-label={t('workflow.stage.moveDown')}
            onClick={() => onMove(index, 1)}
          >
            <ArrowDown className="size-3.5" aria-hidden="true" />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={t('workflow.stage.remove')}
            onClick={() => onRemove(stage.key)}
          >
            <Trash2 className="size-3.5" aria-hidden="true" />
          </Button>
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-3">
        <div>
          <label htmlFor={`wf-stage-name-${stage.key}`} className={LABEL_CLASS}>
            {t('workflow.stage.name')}
          </label>
          <input
            id={`wf-stage-name-${stage.key}`}
            value={stage.name}
            maxLength={100}
            aria-invalid={stageError === 'nameRequired'}
            onChange={(e) => onPatch(stage.key, { name: e.target.value })}
            className={INPUT_CLASS}
          />
          {stageError === 'nameRequired' && (
            <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">
              {t('workflow.stage.nameRequired')}
            </p>
          )}
        </div>
        <div>
          <label htmlFor={`wf-stage-exit-${stage.key}`} className={LABEL_CLASS}>
            {t('workflow.stage.exitStatus')}
          </label>
          <select
            id={`wf-stage-exit-${stage.key}`}
            value={stage.exitStatusKey}
            onChange={(e) => onPatch(stage.key, { exitStatusKey: e.target.value })}
            className={SELECT_CLASS}
          >
            {exitStatusOptions.map((s) => (
              <option key={s.key} value={s.key}>
                {statusName(s.key)}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor={`wf-stage-executor-${stage.key}`} className={LABEL_CLASS}>
            {t('workflow.stage.executor')}
          </label>
          <select
            id={`wf-stage-executor-${stage.key}`}
            value={stage.executorType}
            onChange={(e) => {
              const type = e.target.value as WorkflowExecutorType
              // 换执行者类型时清 executor_id，避免把 agent id 残留给 squad。
              onPatch(stage.key, { executorType: type, executorId: '' })
            }}
            className={SELECT_CLASS}
          >
            <option value="agent">{t('workflow.stage.executorAgent')}</option>
            <option value="squad">{t('workflow.stage.executorSquad')}</option>
            <option value="none">{t('workflow.stage.executorNone')}</option>
          </select>
        </div>
      </div>

      {stage.executorType === 'agent' && (
        <div className="mt-3">
          <label htmlFor={`wf-stage-agent-${stage.key}`} className={LABEL_CLASS}>
            {t('workflow.stage.pickAgent')}
          </label>
          <select
            id={`wf-stage-agent-${stage.key}`}
            value={stage.executorId}
            aria-invalid={stageError === 'executorRequired'}
            onChange={(e) => onPatch(stage.key, { executorId: e.target.value })}
            className={SELECT_CLASS}
          >
            <option value="">{t('workflow.stage.pickAgentPlaceholder')}</option>
            {agents
              .filter((a) => !a.archived)
              .map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
          </select>
          {stageError === 'executorRequired' && (
            <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">
              {t('workflow.stage.executorRequired')}
            </p>
          )}
        </div>
      )}
      {stage.executorType === 'squad' && (
        <div className="mt-3">
          <label htmlFor={`wf-stage-squad-${stage.key}`} className={LABEL_CLASS}>
            {t('workflow.stage.pickSquad')}
          </label>
          <select
            id={`wf-stage-squad-${stage.key}`}
            value={stage.executorId}
            aria-invalid={stageError === 'executorRequired'}
            onChange={(e) => onPatch(stage.key, { executorId: e.target.value })}
            className={SELECT_CLASS}
          >
            <option value="">{t('workflow.stage.pickSquadPlaceholder')}</option>
            {squads.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
          {stageError === 'executorRequired' && (
            <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">
              {t('workflow.stage.executorRequired')}
            </p>
          )}
        </div>
      )}
      {stage.executorType === 'none' && (
        <p className="mt-3 text-xs text-zinc-400">{t('workflow.stage.noneHint')}</p>
      )}

      <div className="mt-3">
        <label htmlFor={`wf-stage-instr-${stage.key}`} className={LABEL_CLASS}>
          {t('workflow.stage.instruction')}
        </label>
        <textarea
          id={`wf-stage-instr-${stage.key}`}
          value={stage.instruction}
          rows={2}
          maxLength={4000}
          placeholder={t('workflow.stage.instrPlaceholder')}
          onChange={(e) => onPatch(stage.key, { instruction: e.target.value })}
          className={TEXTAREA_CLASS}
        />
        <p className="mt-1 text-xs text-zinc-400">{t('workflow.stage.instrHint')}</p>
      </div>
    </li>
  )
}

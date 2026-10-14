import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Bot, RotateCcw, Unlink, User, Users, Workflow } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '@ploykit/client'
import {
  detachWorkItemWorkflow,
  getWorkItemWorkflow,
  restartWorkItemWorkflow,
  startWorkItemWorkflow,
  type WorkflowExecutorType,
  type WorkflowState,
} from '@/api/workflow'
import { Button } from '@/components/ui/button'
import { PageLoading } from '@/components/ui/states'
import { cn } from '@/lib/utils'

// 工作项详情侧栏「流程」区块（设计 §6.2 触点 2 / 契约 §6 W3 前端交付）。
// 数据：GET state 后端未挂（W2 notes「给 W3」）→ api 层 404 归一 null，区块
// 事实源 = start/restart 响应 + 空态；自动刷新复用 main.tsx 的 ws-query-bridge
//（work_item:/task: 帧 invalidate ['work-item'] 前缀，本查询 key 挂其下即免接线）。
// v0 不做 executor 名反查（图标 + 类型标签）与 transition 时间线（state 无 log 列表）。

const EXECUTOR_META: Record<WorkflowExecutorType, { icon: typeof Bot; labelKey: string }> = {
  agent: { icon: Bot, labelKey: 'workflow.stage.executorAgent' },
  squad: { icon: Users, labelKey: 'workflow.stage.executorSquad' },
  none: { icon: User, labelKey: 'workflow.stage.executorNone' },
}

function formatDateTime(iso: string) {
  return new Date(iso).toLocaleString(document.documentElement.lang === 'zh' ? 'zh-CN' : 'en-US', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function WorkflowPanel({ workItemId, projectId }: { workItemId: string; projectId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // key 前缀 ['work-item'] 命中 ws-query-bridge 的失效规则（见文件头注）。
  const stateKey = ['work-item', workItemId, 'workflow-state'] as const
  const state = useQuery({
    queryKey: stateKey,
    queryFn: () => getWorkItemWorkflow(workItemId),
    // GET 未挂恒 404 → null：结果恒定，别让重试/重取打后端。
    retry: false,
    staleTime: Infinity,
  })

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['work-item', workItemId] })
  }
  const settle = (next: WorkflowState | null) => {
    queryClient.setQueryData(stateKey, next)
    invalidate()
  }
  const start = useMutation({
    mutationFn: () => startWorkItemWorkflow(workItemId),
    onSuccess: (s) => settle(s),
  })

  if (state.isPending) {
    return (
      <section aria-label={t('workflow.panel.title')} className="pb-4">
        <PageLoading label={t('workflow.loading')} className="py-4" />
      </section>
    )
  }
  if (state.isError) {
    return (
      <section aria-label={t('workflow.panel.title')} className="pb-4">
        <PanelTitle />
        <p role="alert" className="px-2 text-xs text-red-600 dark:text-red-400">
          {state.error instanceof ApiError ? state.error.message : t('workflow.panel.loadFailed')}
        </p>
      </section>
    )
  }
  return (
    <section aria-label={t('workflow.panel.title')} className="pb-4">
      <PanelTitle />
      {state.data ? (
        <StateBody state={state.data} workItemId={workItemId} onSettle={settle} />
      ) : (
        <EmptyBody
          projectId={projectId}
          startPending={start.isPending}
          startUnavailable={
            start.error instanceof ApiError && (start.error.status === 400 || start.error.status === 404)
          }
          onStart={() => start.mutate()}
        />
      )}
    </section>
  )
}

function PanelTitle() {
  const { t } = useTranslation()
  return (
    <h2 className="flex items-center gap-1 px-2 pb-1 text-xs font-medium text-zinc-400">
      <Workflow className="size-3" aria-hidden="true" />
      {t('workflow.panel.title')}
    </h2>
  )
}

/** 空态：未进入流程（或 GET 降级为 null）→ 文案 + 开始流程；start 400/404（项目无流程/未挂项目）→ 隐藏按钮。 */
function EmptyBody({
  projectId,
  startPending,
  startUnavailable,
  onStart,
}: {
  projectId: string
  startPending: boolean
  startUnavailable: boolean
  onStart: () => void
}) {
  const { t } = useTranslation()
  return (
    <div className="space-y-2 px-2 py-2">
      <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('workflow.panel.empty')}</p>
      {!startUnavailable && projectId && (
        <Button size="xs" disabled={startPending} onClick={onStart}>
          {startPending ? t('workflow.panel.starting') : t('workflow.panel.start')}
        </Button>
      )}
      {startUnavailable && (
        <p className="text-xs text-zinc-400">{t('workflow.panel.noWorkflow')}</p>
      )}
    </div>
  )
}

function StateBody({
  state,
  workItemId,
  onSettle,
}: {
  state: WorkflowState
  workItemId: string
  onSettle: (next: WorkflowState | null) => void
}) {
  const { t } = useTranslation()
  const restart = useMutation({
    mutationFn: () => restartWorkItemWorkflow(workItemId),
    onSuccess: (s) => onSettle(s),
  })
  const detach = useMutation({
    mutationFn: () => detachWorkItemWorkflow(workItemId),
    onSuccess: () => onSettle(null),
  })
  const [confirming, setConfirming] = useState<'restart' | 'detach' | null>(null)
  const executor = EXECUTOR_META[state.executor_type] ?? EXECUTOR_META.none
  const ExecutorIcon = executor.icon
  const finished = Boolean(state.finished_at)
  return (
    <div className="space-y-2 px-2 py-1">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-xs text-zinc-500 dark:text-zinc-400">{t('workflow.panel.currentStage')}</span>
        {finished ? (
          <span className="rounded bg-emerald-50 px-1.5 py-0.5 text-[10px] font-medium text-emerald-700 dark:bg-emerald-950/60 dark:text-emerald-400">
            {t('workflow.panel.finished')}
          </span>
        ) : (
          state.stages.length > 0 && (
            <span className="font-mono text-[10px] text-zinc-400">
              {t('workflow.panel.stageProgress', { n: state.stage_position, total: state.stages.length })}
            </span>
          )
        )}
      </div>
      <div className="flex items-center gap-2 text-sm">
        <span className="truncate font-medium">{state.stage_name || '—'}</span>
        {/* 进度点 ●●○○：stages 升序对照 stage_position（孤儿 state stages 空 → 不渲染） */}
        {state.stages.length > 0 && (
          <span className="flex shrink-0 items-center gap-1" aria-hidden="true">
            {state.stages.map((s) => (
              <span
                key={s.position}
                className={cn(
                  'size-1.5 rounded-full',
                  s.position <= state.stage_position
                    ? 'bg-zinc-700 dark:bg-zinc-300'
                    : 'bg-zinc-300 dark:bg-zinc-600',
                )}
              />
            ))}
          </span>
        )}
      </div>
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="text-zinc-500 dark:text-zinc-400">{t('workflow.panel.executor')}</span>
        <span className="flex min-w-0 items-center gap-1 text-zinc-500 dark:text-zinc-400">
          <ExecutorIcon className="size-3 shrink-0" aria-hidden="true" />
          {t(executor.labelKey)}
        </span>
      </div>
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="text-zinc-500 dark:text-zinc-400">{t('workflow.panel.enteredAt')}</span>
        <span className="text-zinc-500 dark:text-zinc-400">{formatDateTime(state.entered_at)}</span>
      </div>

      {confirming === null ? (
        <div className="flex gap-2 pt-1">
          <Button variant="outline" size="xs" onClick={() => setConfirming('restart')}>
            <RotateCcw className="size-3" aria-hidden="true" />
            {t('workflow.panel.restart')}
          </Button>
          <Button variant="outline" size="xs" onClick={() => setConfirming('detach')}>
            <Unlink className="size-3" aria-hidden="true" />
            {t('workflow.panel.detach')}
          </Button>
        </div>
      ) : (
        <div className="rounded-md border border-red-200 bg-red-50 p-2 dark:border-red-900 dark:bg-red-950/40">
          <p className="text-xs">
            {t(confirming === 'restart' ? 'workflow.panel.restartConfirm' : 'workflow.panel.detachConfirm')}
          </p>
          <div className="mt-1.5 flex gap-2">
            <Button
              size="xs"
              className="bg-red-600 text-white hover:bg-red-500"
              disabled={restart.isPending || detach.isPending}
              onClick={() => {
                if (confirming === 'restart') restart.mutate()
                else detach.mutate()
                setConfirming(null)
              }}
            >
              {t('common.confirm')}
            </Button>
            <Button variant="outline" size="xs" onClick={() => setConfirming(null)}>
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      )}
      {(restart.isError || detach.isError) && (
        <p role="alert" className="text-xs text-red-600 dark:text-red-400">
          {(() => {
            const err = restart.error ?? detach.error
            return err instanceof ApiError ? err.message : t('workflow.panel.actionFailed')
          })()}
        </p>
      )}
    </div>
  )
}

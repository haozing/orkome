import { Dialog } from '@base-ui/react/dialog'
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Check, Loader2, X } from 'lucide-react'
import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import {
  settleTask,
  taskMessagesAscending,
  taskMessagesInfiniteQuery,
  type WorkspaceTaskRow,
} from '@/api/orchestration'
import { TaskMessageList } from '@/components/task-messages'
import { TaskStatusBadge, agentDisplayName, useAgentNameMap } from '@/components/task-status-badge'
import { Button } from '@/components/ui/button'
import { useWsScope } from '@/hooks/ws'
import { cn } from '@/lib/utils'
/**
 * run-logs 页的任务转写抽屉。独立成文件（不与路由同模块）：router 插件的
 * autoCodeSplitting 会把路由文件里的非 Route 导出拆进 chunk，测试无法从
 * 路由模块直接引用组件。
 */
export function TaskTranscriptDrawer({ task, onClose }: { task: WorkspaceTaskRow; onClose: () => void }) {
  const { t, i18n } = useTranslation()
  const formatTime = (iso: string) =>
    new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  const agentNames = useAgentNameMap()
  // FP-12 P2-1：抽屉原来是“死回放”——打开期间无订阅、无轮询、无刷新，
  // 运行中任务（默认 tab 的主场景）的转写永远停在那一次 fetch。挂上 task
  // scope 订阅后，task:completed 等状态帧到达 → main.tsx /^task:/ 失效规则
  // refetch 本抽屉查询（消息帧走 workspace scope，全局失效链本就覆盖）。
  // 用法同 task-panel.tsx 的 TaskDetail。
  useWsScope('task', task.id)
  // FP-05 P1-1：分页拉最新 200 条 + 向上翻更早历史（next_before_seq 游标），
  // 渲染按 seq 全局升序拼接——长任务尾段（含最终 tool_result）不再不可见。
  const messages = useInfiniteQuery(taskMessagesInfiniteQuery(task.id))
  const allMessages = taskMessagesAscending(messages.data)
  // wave3 P1（FP-12 P2-5）：直聊任务（无 work_item）到不了工单面板，抽屉是
  // 全站唯一转写入口——awaiting_input 时在这里给审批动作，否则任务永久卡死。
  // 端点与请求形状复用 task-panel 同款 settleTask（POST /api/tasks/{id}/approve
  // |deny，body {note}）。成功后主动失效列表与本任务查询（消息 paged 键同
  // ['task', id] 前缀）；WS task: 帧（completed/再 park）经 main.tsx 规则
  // 二次兜底，与 task-panel 的 invalidate 链一致。
  const queryClient = useQueryClient()
  const {
    mutate: settle,
    isPending: settling,
    isError: settleFailed,
    isSuccess: settledDone,
    reset: resetSettle,
  } = useMutation({
    mutationFn: (approved: boolean) => settleTask(task.id, approved),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['workspace-tasks'] })
      void queryClient.invalidateQueries({ queryKey: ['task', task.id] })
    },
  })
  // 审批是同步 resume：成功即不再 awaiting_input，操作区随即隐藏（即使任务
  // 已掉出当前筛选、快照仍停在旧状态）。若 agent 审批后再次 park（回到
  // awaiting_input），失效链把列表刷新后状态翻转回来——此时复位 mutation，
  // 操作区重新出现，避免按钮一次性消失。
  useEffect(() => {
    if (task.status === 'awaiting_input') resetSettle()
  }, [task.status, resetSettle])
  const docked = task.status === 'awaiting_input'
  return (
    <Dialog.Root open onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Popup className="fixed top-0 right-0 z-50 h-full w-full max-w-lg overflow-y-auto border-l border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
          <Dialog.Title className="mb-3 flex flex-wrap items-center gap-2 text-sm font-semibold">
            <TaskStatusBadge status={task.status} />
            <span>{agentDisplayName(agentNames, task.agent_id)}</span>
            <span className="text-xs font-normal text-zinc-400">{formatTime(task.created_at)}</span>
            <Dialog.Close className="ml-auto rounded-md px-2 py-1 text-xs text-zinc-400 hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-300">
              {t('common.close')}
            </Dialog.Close>
          </Dialog.Title>

          {docked && !settledDone && (
            <div className="mb-3 rounded-md border border-amber-200 bg-amber-50 p-3 dark:border-amber-900/50 dark:bg-amber-950/40">
              <p className="mb-2 text-xs font-medium text-amber-700 dark:text-amber-300">
                {t('taskMsg.awaitingDecision')}
              </p>
              <div className="flex flex-wrap items-center gap-2">
                <Button size="sm" disabled={settling} onClick={() => settle(true)}>
                  <Check className="size-3.5" aria-hidden="true" />
                  {t('taskMsg.approve')}
                </Button>
                <Button size="sm" variant="danger" disabled={settling} onClick={() => settle(false)}>
                  <X className="size-3.5" aria-hidden="true" />
                  {t('taskMsg.reject')}
                </Button>
                {settling && (
                  <Loader2 className="size-3.5 animate-spin text-amber-500" aria-hidden="true" />
                )}
              </div>
              {settleFailed && (
                <p className="mt-2 text-xs text-red-600 dark:text-red-400">
                  {t('taskMsg.settleFailed')}
                </p>
              )}
            </div>
          )}

          {task.work_item_id && (
            <Link
              to="/work-items/$id"
              params={{ id: task.work_item_id }}
              className="mb-3 block truncate text-xs text-zinc-500 underline-offset-2 outline-none hover:text-zinc-700 hover:underline dark:text-zinc-400"
            >
              {t('runLogs.workItemLink', {
                title: task.work_item_title || task.work_item_id,
              })}
            </Link>
          )}

          {(task.failure_reason || task.error) && (
            <div className="mb-3 rounded-md border border-red-200 bg-red-50 p-3 dark:border-red-900/50 dark:bg-red-950/40">
              {task.failure_reason && (
                <p className="font-mono text-xs font-medium text-red-700 dark:text-red-300">
                  {task.failure_reason}
                </p>
              )}
              {task.error && (
                <pre className="mt-1 font-mono text-xs break-all whitespace-pre-wrap text-red-600 dark:text-red-400">
                  {task.error}
                </pre>
              )}
            </div>
          )}

          {task.result && (
            <div className="mb-3 rounded-md border border-zinc-200 p-3 dark:border-zinc-800">
              <p className="mb-1 text-xs font-medium text-zinc-400">{t('runLogs.result')}</p>
              <pre className={cn('max-h-40 overflow-auto font-mono text-xs whitespace-pre-wrap')}>
                {task.result}
              </pre>
            </div>
          )}

          <p className="mb-2 text-xs font-medium text-zinc-400">{t('runLogs.transcript')}</p>
          {messages.isPending && (
            <p className="text-xs text-zinc-400">{t('runLogs.loadingMessages')}</p>
          )}
          {messages.isError && (
            <p className="text-xs text-red-600 dark:text-red-400">{t('runLogs.messagesFailed')}</p>
          )}
          {messages.data && allMessages.length === 0 && (
            <p className="text-xs text-zinc-400">{t('runLogs.noMessages')}</p>
          )}
          {messages.hasNextPage && (
            <div className="mb-2 flex justify-center">
              <Button
                variant="outline"
                size="sm"
                disabled={messages.isFetchingNextPage}
                onClick={() => void messages.fetchNextPage()}
              >
                {messages.isFetchingNextPage ? t('common.loading') : t('runLogs.loadMore')}
              </Button>
            </div>
          )}
          <TaskMessageList messages={allMessages} />
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

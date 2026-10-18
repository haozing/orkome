import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { isApiError } from '@ploykit/client'
import { Pencil, Plus, Trash2, Zap } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  activeTaskIdFromError,
  createQuickAction,
  getWorkItemActiveTask,
  listQuickActions,
  removeQuickAction,
  runQuickAction,
  updateQuickAction,
  type QuickAction,
  type QuickActionInput,
} from '@/api/quick-actions'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { useWorkspaceStore } from '@/stores/workspace'

// 快捷动作行（W1-2）：工单详情页评论标题之上。
// 仅负责人是 agent 时渲染整行（run 对非 agent 负责人必 400，连管理入口都不出）。
// 上限镜像服务端 MaxQuickActions=3 / label ≤80 runes
// （internal/conversation/app/service_quickaction.go:12-15）。
const MAX_ACTIONS = 3
const MAX_LABEL_RUNES = 80

const LABEL_CLS = 'mb-1 block text-xs text-zinc-500 dark:text-zinc-400'

export type QuickActionSearchPatch = { tab?: string; task?: string }

interface QuickActionsRowProps {
  workItemId: string
  /** 工单负责人类型（wi.assignee_type）；非 'agent' 整行不渲染 */
  assigneeType: string
  /** 详情页 patchSearch：run 成功切执行 tab，409 时带 task 直达在途任务 */
  patchSearch: (patch: QuickActionSearchPatch) => void
}

// 行内三态反馈（pack-detail 三态标注先例，无 toast 库）：保留至下次操作。
type RunFeedback =
  | { kind: 'dispatched' }
  | { kind: 'active'; activeTaskId: string | null }
  | { kind: 'error'; message: string }
  | null

const runeCount = (s: string) => [...s].length

export function QuickActionsRow({ workItemId, assigneeType, patchSearch }: QuickActionsRowProps) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const workspaceId = useWorkspaceStore((s) => s.currentId)
  const [manageOpen, setManageOpen] = useState(false)
  const [feedback, setFeedback] = useState<RunFeedback>(null)

  const actions = useQuery({
    queryKey: ['quick-actions', workspaceId],
    queryFn: listQuickActions,
    enabled: !!workspaceId,
  })

  const run = useMutation({
    mutationFn: (id: string) => runQuickAction(workItemId, id),
    onSuccess: () => {
      setFeedback({ kind: 'dispatched' })
      void qc.invalidateQueries({ queryKey: ['work-item', workItemId, 'tasks'] })
      patchSearch({ tab: 'tasks' })
    },
    onError: async (err) => {
      if (isApiError(err) && err.status === 409) {
        // 409 Details 带 active_task_id（webx 扁平字符串信封，客户端保留
        // details）；Details 缺失时（非标准响应/代理改写）才兜底探
        // active-task 端点——同一 GetActiveTask 数据源。
        const fromDetails = activeTaskIdFromError(err)
        if (fromDetails) {
          setFeedback({ kind: 'active', activeTaskId: fromDetails })
          return
        }
        try {
          const task = await getWorkItemActiveTask(workItemId)
          setFeedback({ kind: 'active', activeTaskId: task?.id ?? null })
        } catch {
          setFeedback({ kind: 'active', activeTaskId: null })
        }
        return
      }
      setFeedback({ kind: 'error', message: err instanceof Error ? err.message : String(err) })
    },
  })

  if (assigneeType !== 'agent') return null

  const list = actions.data ?? []
  return (
    <div
      data-testid="quick-actions-row"
      className="mt-6 border-t border-zinc-200 pt-4 dark:border-zinc-800"
    >
      {list.length === 0 ? (
        <Button
          variant="outline"
          size="sm"
          className="h-7 gap-1 px-2 text-xs"
          onClick={() => setManageOpen(true)}
        >
          <Plus className="size-3.5" aria-hidden="true" />
          {t('work.qa.add')}
        </Button>
      ) : (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="flex shrink-0 items-center gap-1 text-xs text-zinc-400">
            <Zap className="size-3" aria-hidden="true" />
            {t('work.qa.runHint')}
          </span>
          {list.map((qa) => (
            <button
              key={qa.id}
              type="button"
              disabled={run.isPending}
              onClick={() => run.mutate(qa.id)}
              title={qa.prompt}
              className="inline-flex items-center rounded-full border border-zinc-200 px-2 py-0.5 text-xs outline-none hover:border-zinc-400 hover:bg-zinc-50 disabled:opacity-50 dark:border-zinc-700 dark:hover:border-zinc-600 dark:hover:bg-zinc-900"
            >
              {qa.label}
            </button>
          ))}
          <Button
            variant="ghost"
            size="sm"
            className="h-6 px-2 text-xs"
            onClick={() => setManageOpen(true)}
          >
            {t('work.qa.manage')}
          </Button>
        </div>
      )}

      {feedback?.kind === 'dispatched' && (
        <p role="status" className="mt-2 text-xs text-emerald-600 dark:text-emerald-400">
          {t('work.qa.dispatched')}
        </p>
      )}
      {feedback?.kind === 'active' && (
        <p className="mt-2 flex items-center gap-2 text-xs text-amber-600 dark:text-amber-400">
          {t('work.qa.activeConflict')}
          {feedback.activeTaskId && (
            <Button
              variant="outline"
              size="sm"
              className="h-6 px-2 text-xs"
              onClick={() => patchSearch({ tab: 'tasks', task: feedback.activeTaskId! })}
            >
              {t('work.qa.viewActive')}
            </Button>
          )}
        </p>
      )}
      {feedback?.kind === 'error' && (
        <p role="alert" className="mt-2 text-xs text-red-600 dark:text-red-400">
          {feedback.message}
        </p>
      )}

      {manageOpen && (
        <ManageDialog
          workspaceId={workspaceId ?? ''}
          list={list}
          onClose={() => setManageOpen(false)}
        />
      )}
    </div>
  )
}

interface ManageDialogProps {
  workspaceId: string
  list: QuickAction[]
  onClose: () => void
}

// 管理 Dialog：列表（label + prompt 摘要 + 编辑/删除）+ 新建表单（达上限禁用）。
function ManageDialog({ workspaceId, list, onClose }: ManageDialogProps) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [editing, setEditing] = useState<QuickAction | null>(null)
  const [label, setLabel] = useState('')
  const [prompt, setPrompt] = useState('')
  const [formError, setFormError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<QuickAction | null>(null)

  const atMax = list.length >= MAX_ACTIONS
  const invalidate = () => {
    if (workspaceId) void qc.invalidateQueries({ queryKey: ['quick-actions', workspaceId] })
  }
  const resetForm = () => {
    setEditing(null)
    setLabel('')
    setPrompt('')
    setFormError(null)
  }

  const save = useMutation({
    mutationFn: (input: QuickActionInput) =>
      editing ? updateQuickAction(editing.id, input) : createQuickAction(input),
    onSuccess: () => {
      resetForm()
      invalidate()
    },
    onError: (err) => setFormError(err instanceof Error ? err.message : String(err)),
  })

  const remove = useMutation({
    mutationFn: (id: string) => removeQuickAction(id),
    onSuccess: () => {
      setDeleting(null)
      resetForm()
      invalidate()
    },
    onError: (err) => setFormError(err instanceof Error ? err.message : String(err)),
  })

  // 客户端校验镜像服务端 validateQuick：label 非空 ≤80 runes、prompt 非空。
  // 达上限（3 条）时新建禁用（编辑既有条目不受限）。
  const labelValid = label.trim() !== '' && runeCount(label.trim()) <= MAX_LABEL_RUNES
  const createBlocked = !editing && atMax
  const canSave = labelValid && prompt.trim() !== '' && !save.isPending && !createBlocked

  const startEdit = (qa: QuickAction) => {
    setEditing(qa)
    setLabel(qa.label)
    setPrompt(qa.prompt)
    setFormError(null)
  }
  const submit = () => {
    if (!canSave) return
    save.mutate({ label: label.trim(), prompt: prompt.trim() })
  }

  return (
    <Dialog open onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <DialogContent
        className="max-h-[85vh] overflow-y-auto sm:max-w-md"
        data-testid="qa-manage-dialog"
      >
        <DialogHeader>
          <DialogTitle>{t('work.qa.manage')}</DialogTitle>
          <DialogDescription>{t('work.qa.manageDesc')}</DialogDescription>
        </DialogHeader>

        {list.length > 0 && (
          <ul className="space-y-1.5">
            {list.map((qa) => (
              <li
                key={qa.id}
                className="flex items-center gap-2 rounded-md border border-zinc-200 px-2 py-1.5 dark:border-zinc-700"
              >
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{qa.label}</p>
                  <p className="truncate text-xs text-zinc-400">{qa.prompt}</p>
                </div>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t('work.qa.edit')}
                  onClick={() => startEdit(qa)}
                >
                  <Pencil className="size-3.5" aria-hidden="true" />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t('work.qa.delete')}
                  onClick={() => setDeleting(qa)}
                >
                  <Trash2 className="size-3.5" aria-hidden="true" />
                </Button>
              </li>
            ))}
          </ul>
        )}

        <form
          className="space-y-3 border-t border-zinc-200 pt-3 dark:border-zinc-800"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <div>
            <div className="flex items-center justify-between">
              <label htmlFor="qa-label" className={LABEL_CLS}>
                {t('work.qa.labelLabel')}
              </label>
              <span className="text-xs text-zinc-400">
                {runeCount(label)}/{MAX_LABEL_RUNES}
              </span>
            </div>
            <Input
              id="qa-label"
              value={label}
              maxLength={MAX_LABEL_RUNES}
              onChange={(e) => setLabel(e.target.value)}
              placeholder={t('work.qa.labelPlaceholder')}
            />
          </div>
          <div>
            <label htmlFor="qa-prompt" className={LABEL_CLS}>
              {t('work.qa.promptLabel')}
            </label>
            <Textarea
              id="qa-prompt"
              value={prompt}
              rows={3}
              onChange={(e) => setPrompt(e.target.value)}
              placeholder={t('work.qa.promptPlaceholder')}
            />
          </div>
          {formError && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {formError}
            </p>
          )}
          <div className="flex items-center gap-2">
            <Button type="submit" size="sm" disabled={!canSave}>
              {save.isPending ? t('work.qa.saving') : t('work.qa.save')}
            </Button>
            {editing && (
              <Button type="button" variant="ghost" size="sm" onClick={resetForm}>
                {t('common.cancel')}
              </Button>
            )}
            {atMax && !editing && (
              <span className="text-xs text-amber-600 dark:text-amber-400">
                {t('work.qa.maxHint')}
              </span>
            )}
          </div>
        </form>

        <AlertDialog
          open={!!deleting}
          onOpenChange={(open) => {
            if (!open) setDeleting(null)
          }}
        >
          <AlertDialogContent>
            <AlertDialogTitle>{t('work.qa.deleteConfirm')}</AlertDialogTitle>
            <AlertDialogDescription>{deleting?.label}</AlertDialogDescription>
            <AlertDialogFooter>
              <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
              <AlertDialogAction
                variant="destructive"
                disabled={remove.isPending}
                onClick={(e) => {
                  e.preventDefault()
                  if (deleting) remove.mutate(deleting.id)
                }}
              >
                {t('common.confirm')}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </DialogContent>
    </Dialog>
  )
}

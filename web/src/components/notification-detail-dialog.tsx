import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { ApiError } from '@ploykit/client'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { settleTask } from '@/api/orchestration'
import {
  markNotificationRead,
  notificationTypeKey,
  type NotificationItem,
} from '@/api/notification'
import { looksMarkdown, Markdown } from '@/components/markdown'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
// Shared by the notifications page and the bell dropdown: clicking a
// notification opens its full body here. Read-state change happens on open
// (unread → read); navigation to the linked work item stays an explicit action.
// Approval-request notifications that carry a task_id settle inline (W1-1):
// approve/reject call POST /api/tasks/{id}/approve|deny right from the dialog.
export function NotificationDetailDialog({
  item,
  onOpenChange,
}: {
  item: NotificationItem | null
  onOpenChange: (open: boolean) => void
}) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [shown, setShown] = useState<NotificationItem | null>(item)
  // Keep the last non-null item while closing so the content doesn't blank out
  // mid exit-animation.
  const [settled, setSettled] = useState<'approved' | 'rejected' | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const shownIdRef = useRef<string | null>(item?.id ?? null)
  useEffect(() => {
    if (!item) return
    setShown(item)
    // 裁决成功会失效通知列表，同一 id 以新对象引用回流——终态/错误只在新
    // 通知（id 变化）时重置，不能被失效回流冲掉。
    if (item.id !== shownIdRef.current) {
      shownIdRef.current = item.id
      setSettled(null)
      setActionError(null)
    }
  }, [item])
  const open = !!item
  useEffect(() => {
    if (open && item && !item.read_at) void markNotificationRead(item.id, true)
  }, [open, item])
  const settle = useMutation({
    mutationFn: (approved: boolean) => {
      const taskId = shown?.task_id
      if (!taskId) return Promise.reject(new Error('missing task_id'))
      return settleTask(taskId, approved)
    },
    onSuccess: (_data, approved) => {
      setSettled(approved ? 'approved' : 'rejected')
      setActionError(null)
      void queryClient.invalidateQueries({ queryKey: ['notifications'] })
      if (shown?.work_item_id)
        void queryClient.invalidateQueries({
          queryKey: ['work-item', shown.work_item_id, 'tasks'],
        })
    },
    onError: (err) => setActionError(err instanceof ApiError ? err.message : String(err)),
  })
  const canSettle = shown?.type === 'approval_request' && !!shown?.task_id
  const formatTime = (iso: string) =>
    new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[85dvh] flex-col gap-0 overflow-hidden sm:max-w-lg">
        <DialogHeader className="gap-1.5">
          <DialogTitle className="pr-6 leading-snug">{shown?.title}</DialogTitle>
          <DialogDescription className="flex items-center gap-2 text-xs">
            <span className="rounded bg-zinc-100 px-1.5 py-0.5 text-[10px] text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
              {shown ? t(notificationTypeKey(shown.type)) : ''}
            </span>
            <span>{shown ? formatTime(shown.created_at) : ''}</span>
          </DialogDescription>
        </DialogHeader>
        <div className="-mx-1 min-h-0 flex-1 overflow-y-auto px-1 py-2">
          {shown?.body ? (
            looksMarkdown(shown.body) ? (
              <Markdown className="break-words">{shown.body}</Markdown>
            ) : (
              <p className="whitespace-pre-wrap break-words text-sm leading-relaxed text-zinc-700 dark:text-zinc-300">
                {shown.body}
              </p>
            )
          ) : (
            <p className="text-sm text-zinc-400">{t('notif.detailEmptyBody')}</p>
          )}
        </div>
        <DialogFooter className="items-center sm:justify-between">
          {canSettle && !settled && (
            <div className="flex items-center gap-2">
              <Button size="sm" disabled={settle.isPending} onClick={() => settle.mutate(true)}>
                {t('notif.inlineApprove')}
              </Button>
              <Button
                size="sm"
                variant="destructive"
                disabled={settle.isPending}
                onClick={() => settle.mutate(false)}
              >
                {t('notif.inlineReject')}
              </Button>
            </div>
          )}
          {settled && (
            <span className="text-sm text-zinc-500" data-testid="settle-outcome">
              {settled === 'approved' ? t('notif.inlineApproved') : t('notif.inlineRejected')}
            </span>
          )}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              onOpenChange(false)
              if (shown?.work_item_id)
                void navigate({ to: '/work-items/$id', params: { id: shown.work_item_id } })
            }}
          >
            {t('notif.detailOpenWorkItem')}
          </Button>
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
            {t('common.close')}
          </Button>
        </DialogFooter>
        {actionError && (
          <p role="alert" className="px-1 pb-2 text-sm text-destructive">
            {t('notif.inlineActionFailed')}: {actionError}
          </p>
        )}
      </DialogContent>
    </Dialog>
  )
}

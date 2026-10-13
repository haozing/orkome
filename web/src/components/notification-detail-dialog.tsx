import { useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
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
export function NotificationDetailDialog({
  item,
  onOpenChange,
}: {
  item: NotificationItem | null
  onOpenChange: (open: boolean) => void
}) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const [shown, setShown] = useState<NotificationItem | null>(item)
  // Keep the last non-null item while closing so the content doesn't blank out
  // mid exit-animation.
  useEffect(() => {
    if (item) setShown(item)
  }, [item])
  const open = !!item
  useEffect(() => {
    if (open && item && !item.read_at) void markNotificationRead(item.id, true)
  }, [open, item])
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
      </DialogContent>
    </Dialog>
  )
}

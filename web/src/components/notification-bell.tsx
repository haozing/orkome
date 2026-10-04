import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Bell, CheckCheck } from 'lucide-react'
import { useEffect } from 'react'
import {
  getUnreadCount,
  listNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  notificationTypeKey,
  type NotificationItem,
} from '@/api/notification'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { wsClient } from '@/realtime/client'
import { cn } from '@/lib/utils'
import { useTranslation } from 'react-i18next'
const NOTIFICATION_FRAMES = [
  'notification:read',
  'notification:unread',
  'notification:batch_read',
  'notification:archived',
  'notification:unarchived',
  'notification:batch_archived',
] as const
function formatTime(iso: string, locale = 'en-US'): string {
  return new Date(iso).toLocaleString(locale, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
function PreviewRow({ item }: { item: NotificationItem }) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const unread = !item.read_at
  const open = () => {
    if (!unread) {
      if (item.work_item_id)
        void navigate({ to: '/work-items/$id', params: { id: item.work_item_id } })
      return
    }
    void markNotificationRead(item.id, true).then(() => {
      if (item.work_item_id) {
        void navigate({ to: '/work-items/$id', params: { id: item.work_item_id } })
      }
    })
  }
  return (
    <button
      type="button"
      onClick={open}
      className="flex w-full flex-col gap-0.5 rounded-md px-2.5 py-2 text-left outline-none hover:bg-zinc-100 dark:hover:bg-zinc-800"
    >
      <span className="flex items-center gap-1.5">
        {unread && (
          <span className="size-1.5 shrink-0 rounded-full bg-sky-500" aria-hidden="true" />
        )}
        <span
          className={cn(
            'truncate text-xs',
            unread ? 'font-medium' : 'text-zinc-500 dark:text-zinc-400',
          )}
        >
          {item.title}
        </span>
      </span>
      <span className="flex items-center justify-between gap-2 pl-3">
        <span className="text-[10px] text-zinc-400">{t(notificationTypeKey(item.type))}</span>
        <span className="text-[10px] text-zinc-400">
          {formatTime(item.created_at, i18n.language === 'zh' ? 'zh-CN' : 'en-US')}
        </span>
      </span>
    </button>
  )
}
export function NotificationBell() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const unread = useQuery({
    queryKey: ['notifications', 'unread-count'],
    queryFn: getUnreadCount,
    refetchInterval: 60000,
  })
  const recent = useQuery({
    queryKey: ['notifications', 'recent'],
    queryFn: () => listNotifications(false, 8),
  })
  useEffect(() => {
    const unsubs = NOTIFICATION_FRAMES.map((type) =>
      wsClient.on(type, () => {
        void queryClient.invalidateQueries({ queryKey: ['notifications'] })
      }),
    )
    return () => unsubs.forEach((un) => un())
  }, [queryClient])
  const markAll = () => void markAllNotificationsRead()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t('notif.bellAria', { count: unread.data?.count ?? 0 })}
          >
            <Bell className="size-4" />
            {!!unread.data?.count && (
              <span
                aria-hidden="true"
                className="absolute -top-0.5 -right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-red-500 px-1 text-[10px] font-medium text-white"
              >
                {unread.data.count > 99 ? '99+' : unread.data.count}
              </span>
            )}
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="w-80 p-0">
        <div className="flex items-center justify-between border-b border-zinc-200 px-3 py-2 dark:border-zinc-800">
          <span className="text-sm font-medium">{t('nav.notifications')}</span>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 gap-1 text-xs"
            disabled={!unread.data?.count}
            onClick={markAll}
          >
            <CheckCheck className="size-3.5" aria-hidden="true" />
            {t('notif.markAllRead')}
          </Button>
        </div>
        <div className="max-h-80 overflow-y-auto p-1">
          {recent.data?.items.length === 0 && (
            <p className="px-3 py-6 text-center text-xs text-zinc-400">{t('notif.empty')}</p>
          )}
          {recent.data?.items.map((it) => (
            <PreviewRow key={it.id} item={it} />
          ))}
        </div>
        <button
          type="button"
          className="w-full border-t border-zinc-200 py-2 text-center text-xs text-zinc-500 outline-none hover:bg-zinc-100 hover:text-zinc-700 dark:border-zinc-800 dark:text-zinc-400 dark:hover:bg-zinc-800"
          onClick={() => void navigate({ to: '/notifications' })}
        >
          {t('notif.viewAllNotifications')}
        </button>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

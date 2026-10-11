import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { Archive, CheckCheck, Circle, CircleCheck } from 'lucide-react'
import { z } from 'zod'
import {
  archiveAllReadNotifications,
  archiveNotification,
  listNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  notificationTypeKey,
  type NotificationItem,
} from '@/api/notification'
import { Button } from '@/components/ui/button'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { cn } from '@/lib/utils'
import { useTranslation } from 'react-i18next'
const searchSchema = z.object({
  archived: z.boolean().optional(),
  unread: z.boolean().optional(),
})
export const Route = createFileRoute('/_app/notifications')({
  validateSearch: (search) => {
    const parsed = searchSchema.safeParse(search)
    return parsed.success ? parsed.data : {}
  },
  component: NotificationsPage,
})
// Realtime: user-scoped notification:* frames arrive on the hub's user scope;
// the central bridge in main.tsx invalidates the ['notifications', …] prefix
// on them (see wsInvalidationRules), so this page needs no frame listeners.
function useFormatTime() {
  const { i18n } = useTranslation()
  return (iso: string) =>
    new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
}
function Row({ item, view }: { item: NotificationItem; view: 'all' | 'unread' | 'archived' }) {
  const { t } = useTranslation()
  const formatTime = useFormatTime()
  const queryClient = useQueryClient()
  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ['notifications'] })
  const read = useMutation({
    mutationFn: () => markNotificationRead(item.id, !item.read_at),
    onSuccess: invalidate,
  })
  const archive = useMutation({
    mutationFn: () => archiveNotification(item.id, view !== 'archived'),
    onSuccess: invalidate,
  })
  const unread = !item.read_at
  const canOpen = !!item.work_item_id || !!item.link
  const open = () => {
    if (unread) read.mutate()
  }
  return (
    <div
      className={cn(
        'flex items-start gap-3 px-3 py-2.5',
        unread && 'bg-sky-50/60 dark:bg-sky-950/20',
      )}
    >
      <span className="mt-1.5 shrink-0" aria-hidden="true">
        <Circle
          className={cn(
            'size-2',
            unread ? 'fill-sky-500 text-sky-500' : 'text-zinc-300 dark:text-zinc-600',
          )}
        />
      </span>
      <button
        type="button"
        onClick={open}
        disabled={!canOpen}
        className="min-w-0 flex-1 cursor-default text-left outline-none"
        aria-label={canOpen ? t('notif.openAndRead', { title: item.title }) : undefined}
      >
        <p className="flex items-center gap-2">
          <span
            className={cn(
              'truncate text-sm',
              unread ? 'font-medium' : 'text-zinc-500 dark:text-zinc-400',
            )}
          >
            {item.title}
          </span>
          <span className="shrink-0 rounded bg-zinc-100 px-1.5 py-0.5 text-[10px] text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
            {t(notificationTypeKey(item.type))}
          </span>
        </p>
        {item.body && (
          <p className="mt-0.5 line-clamp-1 text-xs text-zinc-500 dark:text-zinc-400">
            {item.body}
          </p>
        )}
        <p className="mt-0.5 text-[10px] text-zinc-400">{formatTime(item.created_at)}</p>
      </button>
      <div className="flex shrink-0 items-center gap-1">
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={unread ? t('notif.markRead') : t('notif.markUnread')}
          disabled={read.isPending}
          onClick={() => read.mutate()}
        >
          {unread ? <CircleCheck className="size-4" /> : <Circle className="size-4" />}
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={view === 'archived' ? t('notif.unarchive') : t('notif.archive')}
          disabled={archive.isPending}
          onClick={() => archive.mutate()}
        >
          <Archive className="size-4" />
        </Button>
      </div>
    </div>
  )
}
function NotificationsPage() {
  const { t } = useTranslation()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const queryClient = useQueryClient()
  const view: 'all' | 'unread' | 'archived' = search.archived
    ? 'archived'
    : search.unread
      ? 'unread'
      : 'all'
  const list = useInfiniteQuery({
    queryKey: ['notifications', 'list', view],
    queryFn: ({ pageParam }: { pageParam: string }) =>
      listNotifications(view === 'archived', 30, pageParam || undefined),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  })
  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ['notifications'] })
  const markAll = useMutation({ mutationFn: markAllNotificationsRead, onSuccess: invalidate })
  const archiveRead = useMutation({
    mutationFn: archiveAllReadNotifications,
    onSuccess: invalidate,
  })
  const items = list.data?.pages.flatMap((p) => p.items) ?? []
  const visible = view === 'unread' ? items.filter((it) => !it.read_at) : items
  const unreadCount = items.filter((it) => !it.read_at).length
  const switchView = (v: 'all' | 'unread' | 'archived') =>
    void navigate({
      search: { archived: v === 'archived' || undefined, unread: v === 'unread' || undefined },
      replace: true,
    })
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        <h1 className="text-lg font-semibold">{t('nav.notifications')}</h1>
        {view !== 'archived' && (
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={!unreadCount || markAll.isPending}
              onClick={() => markAll.mutate()}
            >
              <CheckCheck className="size-4" aria-hidden="true" />
              {t('notif.markAllRead')}
              {unreadCount ? ` (${unreadCount})` : ''}
            </Button>
            {view === 'all' && (
              <Button
                variant="ghost"
                size="sm"
                disabled={archiveRead.isPending}
                onClick={() => archiveRead.mutate()}
              >
                <Archive className="size-4" aria-hidden="true" />
                {t('notif.archiveAllRead')}
              </Button>
            )}
          </div>
        )}
      </div>

      <div className="flex gap-1" role="tablist" aria-label={t('notif.viewAria')}>
        {(
          [
            { key: 'all', label: 'notif.viewAll' },
            { key: 'unread', label: 'notif.viewUnread' },
            { key: 'archived', label: 'notif.viewArchived' },
          ] as const
        ).map((tab) => (
          <button
            key={tab.key}
            type="button"
            role="tab"
            aria-selected={view === tab.key}
            className={
              'cursor-default rounded-md px-3 py-1.5 text-sm outline-none ' +
              (view === tab.key
                ? 'bg-zinc-100 font-medium text-zinc-900 dark:bg-zinc-800 dark:text-zinc-100'
                : 'text-zinc-500 hover:bg-zinc-100 hover:text-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-200')
            }
            onClick={() => switchView(tab.key)}
          >
            {t(tab.label)}
          </button>
        ))}
      </div>

      {list.isPending && <PageLoading label={t('notif.loading')} />}

      {list.isError && (
        <PageError
          title={t('notif.loadFailed')}
          message={list.error instanceof Error ? list.error.message : undefined}
          onRetry={() => void list.refetch()}
        />
      )}

      {list.isSuccess && visible.length === 0 && (
        <PageEmpty
          title={
            view === 'unread'
              ? t('notif.emptyUnread')
              : view === 'archived'
                ? t('notif.emptyArchived')
                : t('notif.empty')
          }
          description={t('notif.emptyDesc')}
        />
      )}

      {visible.length > 0 && (
        <div className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
          {visible.map((it) => (
            <Row key={it.id} item={it} view={view} />
          ))}
        </div>
      )}

      {view !== 'unread' && list.hasNextPage && (
        <div className="text-center">
          <Button
            variant="outline"
            size="sm"
            disabled={list.isFetchingNextPage}
            onClick={() => void list.fetchNextPage()}
          >
            {list.isFetchingNextPage ? t('common.loading') : t('runLogs.loadMore')}
          </Button>
        </div>
      )}
    </div>
  )
}

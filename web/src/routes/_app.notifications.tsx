import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { Archive, CheckCheck, Circle, CircleCheck } from 'lucide-react'
import { z } from 'zod'
import { useState } from 'react'
import {
  archiveAllReadNotifications,
  archiveNotification,
  listNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  notificationTypeKey,
  type NotificationItem,
} from '@/api/notification'
import { PageHeader } from '@ploykit/ui'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { NotificationDetailDialog } from '@/components/notification-detail-dialog'
import { SegmentedControl } from '@/components/shared/segmented-control'
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
function Row({
  item,
  view,
  onOpen,
}: {
  item: NotificationItem
  view: 'all' | 'unread' | 'archived'
  onOpen: (item: NotificationItem) => void
}) {
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
        onClick={() => onOpen(item)}
        className="min-w-0 flex-1 cursor-default text-left outline-none"
        aria-label={t('notif.openAndRead', { title: item.title })}
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
          {/* 类型 chip 归一 kit Badge（ui-sweep #8：与详情弹窗同款 secondary） */}
          <Badge variant="secondary">{t(notificationTypeKey(item.type))}</Badge>
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
  const [detail, setDetail] = useState<NotificationItem | null>(null)
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
      {/* 页头 + 双操作同行已是既有布局，kit PageHeader 化（ui-sweep #8/#13）；
          「全部已读 (n)」改插值键 markAllReadN，不再手写字符串拼接 */}
      <PageHeader
        title={t('nav.notifications')}
        actions={
          view !== 'archived' ? (
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={!unreadCount || markAll.isPending}
                onClick={() => markAll.mutate()}
              >
                <CheckCheck className="size-4" aria-hidden="true" />
                {unreadCount
                  ? t('notif.markAllReadN', { n: unreadCount })
                  : t('notif.markAllRead')}
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
          ) : undefined
        }
      />

      <SegmentedControl
        ariaLabel={t('notif.viewAria')}
        value={view}
        onChange={switchView}
        options={[
          { value: 'all', label: t('notif.viewAll') },
          { value: 'unread', label: t('notif.viewUnread') },
          { value: 'archived', label: t('notif.viewArchived') },
        ]}
      />

      {list.isPending && <PageLoading label={t('notif.loading')} />}

      {list.isError && (
        <PageError
          title={t('notif.loadFailed')}
          message={list.error instanceof Error ? list.error.message : undefined}
          onRetry={() => void list.refetch()}
        />
      )}

      {list.isSuccess && visible.length === 0 && (
        /* 空态不再复述五类通知枚举（ui-sweep #6：notif.emptyDesc 删） */
        <PageEmpty
          title={
            view === 'unread'
              ? t('notif.emptyUnread')
              : view === 'archived'
                ? t('notif.emptyArchived')
                : t('notif.empty')
          }
        />
      )}

      {visible.length > 0 && (
        <div className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
          {visible.map((it) => (
            <Row key={it.id} item={it} view={view} onOpen={setDetail} />
          ))}
        </div>
      )}

      <NotificationDetailDialog item={detail} onOpenChange={(o) => !o && setDetail(null)} />

      {/* unread 视图也渲染加载更多（真 bug 修复：visible 是客户端过滤，
          过滤后列表可能为空但服务端仍有下一页——旧条件 view !== 'unread'
          会把 unread 视图卡死在第一页 30 条，后到的未读永远拉不进来） */}
      {list.hasNextPage && (
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

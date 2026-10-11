import { api } from '@ploykit/client'
export interface NotificationItem {
  id: string
  type: string
  title: string
  body: string
  link?: string
  work_item_id?: string
  read_at?: string
  created_at: string
}
export interface NotificationSummary {
  unread: number
  today_new: number
}
export interface NotificationFacet {
  type: string
  count: number
}
export const NOTIFICATION_TYPE_KEYS: Record<string, string> = {
  'task.completed': 'notif.typeTaskCompleted',
  'task.failed': 'notif.typeTaskFailed',
  'comment.created': 'notif.typeComment',
  approval_request: 'notif.typeApproval',
  eval_report: 'notif.typeEval',
}
export function notificationTypeKey(type: string): string {
  return NOTIFICATION_TYPE_KEYS[type] ?? type
}
export async function listNotifications(
  archived: boolean,
  limit = 30,
  cursor?: string,
): Promise<{
  items: NotificationItem[]
  next_cursor: string | null
}> {
  const qs = new URLSearchParams()
  if (archived) qs.set('archived', 'true')
  if (limit) qs.set('limit', String(limit))
  if (cursor) qs.set('cursor', cursor)
  const page = await api.get<{
    items: NotificationItem[] | null
    next_cursor: string | null
  }>(`/api/notifications${qs.size ? `?${qs}` : ''}`)
  return { items: page.items ?? [], next_cursor: page.next_cursor ?? null }
}
export function getUnreadCount(): Promise<{
  count: number
}> {
  return api.get('/api/notifications/unread-count')
}
export function getNotificationSummary(): Promise<NotificationSummary> {
  return api.get('/api/notifications/summary')
}
export function markNotificationRead(id: string, read: boolean): Promise<void> {
  return api.post<void>(`/api/notifications/${id}/${read ? 'read' : 'unread'}`)
}
export function markAllNotificationsRead(): Promise<void> {
  return api.post<void>('/api/notifications/mark-all-read')
}
export function archiveNotification(id: string, archived: boolean): Promise<void> {
  return api.post<void>(`/api/notifications/${id}/${archived ? 'archive' : 'unarchive'}`)
}
export function archiveAllNotifications(): Promise<void> {
  return api.post<void>('/api/notifications/archive-all')
}
export function archiveAllReadNotifications(): Promise<{
  archived: number
}> {
  return api.post<{
    archived: number
  }>('/api/notifications/archive-all-read')
}

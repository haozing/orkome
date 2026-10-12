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

// ---- mute-group preferences (migration 1034, the 'mute' sentinel row) ----

/** Product notification types a user can mute from the settings page. */
export const MUTABLE_NOTIFICATION_TYPES = [
  'task.completed',
  'task.failed',
  'comment.created',
  'approval_request',
  'eval_report',
] as const

interface RawPreferences {
  muted_types: string[] | null
  muted_work_item_ids: string[] | null
  muted_conversation_ids: string[] | null
}

export interface NotificationPreferences {
  muted_types: string[]
  muted_work_item_ids: string[]
  muted_conversation_ids: string[]
}

// Empty groups arrive as null in the raw envelope (pgrepo splitMutedTypes
// returns nil for empty input) — normalize to [] so the UI renders backend
// defaults ("nothing muted") uniformly.
function normalizePreferences(raw: RawPreferences): NotificationPreferences {
  return {
    muted_types: raw.muted_types ?? [],
    muted_work_item_ids: raw.muted_work_item_ids ?? [],
    muted_conversation_ids: raw.muted_conversation_ids ?? [],
  }
}

export async function getNotificationPreferences(): Promise<NotificationPreferences> {
  return normalizePreferences(await api.get<RawPreferences>('/api/notification-preferences'))
}

/**
 * PATCH /api/notification-preferences — partial update: only the provided
 * keys change, the server merges them over the current row, validates the
 * merged preference and returns it (verified live 2026-10-12). Validation:
 * ≤50 muted_types / ≤200 per ID group, and the `session:` prefix is
 * reserved inside muted_types (E_VALIDATION 400).
 */
export async function patchNotificationPreferences(
  patch: Partial<NotificationPreferences>,
): Promise<NotificationPreferences> {
  return normalizePreferences(await api.patch<RawPreferences>('/api/notification-preferences', patch))
}

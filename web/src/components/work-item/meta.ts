export interface StatusMeta {
  labelKey: string
  tone: 'neutral' | 'warning' | 'success' | 'danger'
}
export const STATUS_META: Record<string, StatusMeta> = {
  backlog: { labelKey: 'status.backlog', tone: 'neutral' },
  todo: { labelKey: 'status.todo', tone: 'neutral' },
  in_progress: { labelKey: 'status.inProgress', tone: 'warning' },
  in_review: { labelKey: 'status.inReview', tone: 'warning' },
  blocked: { labelKey: 'status.blocked', tone: 'danger' },
  done: { labelKey: 'status.done', tone: 'success' },
  cancelled: { labelKey: 'status.cancelled', tone: 'danger' },
}
export function statusMetaOf(key: string): StatusMeta {
  return STATUS_META[key] ?? { labelKey: key, tone: 'neutral' }
}
export const PRIORITY_META: Record<
  number,
  {
    labelKey: string
    className: string
  }
> = {
  0: { labelKey: 'priority.none', className: 'text-zinc-400' },
  1: { labelKey: 'priority.low', className: 'text-sky-600 dark:text-sky-400' },
  2: { labelKey: 'priority.medium', className: 'text-amber-600 dark:text-amber-400' },
  3: { labelKey: 'priority.high', className: 'text-red-600 dark:text-red-400' },
}

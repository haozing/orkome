import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
export function PageLoading({ label, className }: { label?: string; className?: string }) {
  const { t } = useTranslation()
  return (
    <div className={cn('flex flex-col items-center gap-3 pt-24 text-center', className)}>
      <span
        aria-hidden="true"
        className="size-5 animate-spin rounded-full border-2 border-zinc-300 border-t-zinc-700 dark:border-zinc-700 dark:border-t-zinc-300"
      />
      <p className="text-sm text-zinc-500 dark:text-zinc-400">{label ?? t('common.loading')}</p>
    </div>
  )
}
export function PageEmpty({
  title,
  description,
  action,
  className,
}: {
  title: string
  description?: string
  action?: ReactNode
  className?: string
}) {
  return (
    <div className={cn('flex flex-col items-center gap-2 pt-24 text-center', className)}>
      <h2 className="text-sm font-medium">{title}</h2>
      {description && (
        <p className="max-w-sm text-sm text-zinc-500 dark:text-zinc-400">{description}</p>
      )}
      {action && <div className="mt-3">{action}</div>}
    </div>
  )
}
export function PageError({
  title,
  message,
  onRetry,
  retryLabel,
  className,
}: {
  title?: string
  message?: string
  onRetry?: () => void
  retryLabel?: string
  className?: string
}) {
  const { t } = useTranslation()
  return (
    <div
      role="alert"
      className={cn('flex flex-col items-center gap-2 pt-24 text-center', className)}
    >
      <h2 className="text-sm font-medium text-red-600 dark:text-red-400">
        {title ?? t('common.error')}
      </h2>
      {message && (
        <p className="max-w-md text-sm break-all text-zinc-500 dark:text-zinc-400">{message}</p>
      )}
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="mt-3 rounded-md border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-800"
        >
          {retryLabel ?? t('common.retry')}
        </button>
      )}
    </div>
  )
}

import { type ErrorComponentProps, Link, useRouter } from '@tanstack/react-router'
import { Button } from '@/components/ui/button'
import { useTranslation } from 'react-i18next'
export function RouteErrorComponent({ error, reset }: ErrorComponentProps) {
  const { t } = useTranslation()
  const router = useRouter()
  const message = error instanceof Error ? error.message : String(error ?? '')
  return (
    <div
      role="alert"
      className="flex min-h-dvh flex-col items-center justify-center gap-3 p-6 text-center"
    >
      <h1 className="text-lg font-semibold">{t('err.title')}</h1>
      <p className="max-w-md text-sm break-all text-zinc-500 dark:text-zinc-400">
        {message || t('err.unknown')}
      </p>
      <div className="mt-2 flex gap-2">
        <Button size="sm" onClick={() => reset()}>
          {t('common.retry')}
        </Button>
        <Button size="sm" variant="outline" onClick={() => router.history.back()}>
          {t('err.back')}
        </Button>
        <Link to="/work-items">
          <Button size="sm" variant="ghost">
            {t('err.backToWork')}
          </Button>
        </Link>
      </div>
    </div>
  )
}
export function RouteNotFoundComponent() {
  const { t } = useTranslation()
  return (
    <div className="flex min-h-dvh flex-col items-center justify-center gap-3 p-6 text-center">
      <h1 className="text-lg font-semibold">{t('err.notFound')}</h1>
      <Link to="/work-items">
        <Button size="sm" variant="outline">
          {t('err.backToWork')}
        </Button>
      </Link>
    </div>
  )
}

import { useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link } from '@tanstack/react-router'
import { configQueryOptions, meQueryOptions } from '@/api/identity'
import { Button } from '@/components/ui/button'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { ApiError } from '@ploykit/client'
import { switchWorkspace } from '@/services/workspace-switch'
import { useWorkspaceStore } from '@/stores/workspace'
import { useTranslation } from 'react-i18next'
export const Route = createFileRoute('/_app/settings')({
  component: SettingsPage,
})
function SettingsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const config = useQuery(configQueryOptions)
  const me = useQuery(meQueryOptions)
  const currentId = useWorkspaceStore((s) => s.currentId)
  const unauthenticated = me.error instanceof ApiError && me.error.status === 401
  return (
    <div className="space-y-6">
      <section className="rounded-lg border border-zinc-200 p-4 dark:border-zinc-800">
        <h2 className="mb-3 text-sm font-semibold">{t('settings.server')}</h2>
        {config.isPending && <PageLoading label={t('settings.connecting')} className="pt-2" />}
        {config.data && (
          <p className="font-mono text-sm">
            {t('settings.version')}{' '}
            <span className="text-zinc-500">{config.data.version || 'unknown'}</span>
          </p>
        )}
        {config.error && (
          <PageError
            title={t('settings.unreachable')}
            message={t('settings.unreachableDesc')}
            onRetry={() => void config.refetch()}
            className="pt-4"
          />
        )}
      </section>

      <section className="rounded-lg border border-zinc-200 p-4 dark:border-zinc-800">
        <h2 className="mb-3 text-sm font-semibold">{t('settings.session')}</h2>
        {me.isPending && <PageLoading label={t('settings.loadingSession')} className="pt-2" />}
        {me.data && (
          <div className="space-y-2 text-sm">
            <p>
              {me.data.user.display_name || me.data.user.email}
              <span className="ml-2 text-zinc-500">{me.data.user.email}</span>
            </p>
            {me.data.workspaces.length > 0 ? (
              <div className="flex flex-wrap gap-1">
                {me.data.workspaces.map((ws) => (
                  <Button
                    key={ws.id}
                    variant={ws.id === currentId ? 'default' : 'outline'}
                    size="sm"
                    onClick={() => switchWorkspace(queryClient, ws.id)}
                  >
                    {ws.name}
                    <span className="font-mono text-xs opacity-60">/{ws.slug}</span>
                  </Button>
                ))}
              </div>
            ) : (
              <PageEmpty
                title={t('settings.noWorkspace')}
                description={t('settings.noWorkspaceDesc')}
                action={
                  <Link to="/onboarding">
                    <Button size="sm" variant="outline">
                      {t('settings.goCreate')}
                    </Button>
                  </Link>
                }
                className="pt-2"
              />
            )}
          </div>
        )}
        {unauthenticated && (
          <div className="space-y-2 text-sm">
            <p className="text-zinc-500">{t('settings.notLoggedIn')}</p>
            <Link to="/login">
              <Button size="sm">{t('settings.goLogin')}</Button>
            </Link>
          </div>
        )}
      </section>
    </div>
  )
}

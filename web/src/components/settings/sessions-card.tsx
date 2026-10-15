import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '@ploykit/client'
import {
  revokeAllSessions,
  revokeSession,
  sessionsQueryOptions,
  type SessionInfo,
} from '@/api/identity'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { signOutEverywhere, useSettingsTimeFormatter } from '@/components/settings/shared'

// 会话管理卡片（design §3.2c）：裸数组已归一；本设备（current）置顶；
// 逐行「退出」；底部「退出所有设备」红字 + 二次确认（成功后清 cookie → 登出流程）。
export function SessionsCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const fmtTime = useSettingsTimeFormatter()
  const sessions = useQuery(sessionsQueryOptions)
  const [confirmingId, setConfirmingId] = useState<string | null>(null)
  const [confirmRevokeAll, setConfirmRevokeAll] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const invalidate = () => void queryClient.invalidateQueries({ queryKey: sessionsQueryOptions.queryKey })

  const revoke = useMutation({
    mutationFn: revokeSession,
    onSuccess: () => {
      setConfirmingId(null)
      setError(null)
      invalidate()
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : String(err)),
  })

  // DELETE /auth/sessions 清 cookie（含当前会话）——成功后走登出流程。
  const revokeAll = useMutation({
    mutationFn: revokeAllSessions,
    onSuccess: () => {
      setConfirmRevokeAll(false)
      void signOutEverywhere(queryClient)
    },
    onError: (err) => {
      setConfirmRevokeAll(false)
      setError(err instanceof ApiError ? err.message : String(err))
    },
  })

  // current 置顶，其余按 last_seen_at 降序。
  const sorted = sortSessions(sessions.data)

  return (
    <div className="space-y-3" data-testid="sessions-card">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="text-sm font-medium">{t('settings.sessions.title')}</p>
          <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('settings.sessions.desc')}</p>
        </div>
      </div>

      {error && (
        <p className="text-xs text-red-600 dark:text-red-400" role="alert">
          {error}
        </p>
      )}

      <div className="divide-y divide-zinc-100 dark:divide-zinc-800">
        {sessions.isPending && <p className="py-3 text-sm text-zinc-500">{t('common.loading')}</p>}
        {sessions.error && (
          <p className="py-3 text-sm text-red-600 dark:text-red-400">{sessions.error.message}</p>
        )}
        {sorted.map((s) => {
          const confirming = confirmingId === s.id
          return (
            <div key={s.id} className="flex items-center justify-between gap-4 py-3">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  {s.current && (
                    <Badge variant="success" data-testid="session-current">
                      {t('settings.sessions.currentDevice')}
                    </Badge>
                  )}
                  <span className="max-w-md truncate text-sm" title={s.user_agent}>
                    {s.user_agent || t('settings.sessions.unknownUa')}
                  </span>
                </div>
                <p className="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">
                  {t('settings.sessions.lastSeen', { time: fmtTime(s.last_seen_at) })}
                  {s.impersonated_by && ` · ${t('settings.sessions.impersonated', { by: s.impersonated_by })}`}
                </p>
              </div>
              {confirming ? (
                <div className="flex shrink-0 items-center gap-1">
                  <Button
                    variant="danger"
                    size="xs"
                    disabled={revoke.isPending}
                    onClick={() => revoke.mutate(s.id)}
                  >
                    {t('settings.sessions.confirmRevoke')}
                  </Button>
                  <Button variant="ghost" size="xs" onClick={() => setConfirmingId(null)}>
                    {t('common.cancel')}
                  </Button>
                </div>
              ) : (
                <Button
                  variant="ghost"
                  size="xs"
                  className="shrink-0 text-red-600 hover:text-red-600 dark:text-red-400"
                  onClick={() => setConfirmingId(s.id)}
                  data-testid={`session-revoke-${s.id}`}
                >
                  {t('settings.sessions.revoke')}
                </Button>
              )}
            </div>
          )
        })}
      </div>

      <div className="flex justify-end border-t border-zinc-100 pt-2 dark:border-zinc-800">
        <Button
          variant="ghost"
          size="xs"
          className="text-red-600 hover:text-red-600 dark:text-red-400"
          onClick={() => setConfirmRevokeAll(true)}
          disabled={!sorted.length}
          data-testid="sessions-revoke-all"
        >
          {t('settings.sessions.revokeAll')}
        </Button>
      </div>

      <AlertDialog open={confirmRevokeAll} onOpenChange={setConfirmRevokeAll}>
        <AlertDialogContent>
          <AlertDialogTitle>{t('settings.sessions.revokeAllConfirmTitle')}</AlertDialogTitle>
          <AlertDialogDescription>{t('settings.sessions.revokeAllConfirmDesc')}</AlertDialogDescription>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              variant="danger"
              disabled={revokeAll.isPending}
              onClick={() => revokeAll.mutate()}
              data-testid="sessions-revoke-all-confirm"
            >
              {t('settings.sessions.revokeAllConfirmAction')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function sortSessions(list: SessionInfo[] | undefined): SessionInfo[] {
  if (!list) return []
  return [...list].sort((a, b) => {
    if (a.current !== b.current) return a.current ? -1 : 1
    return new Date(b.last_seen_at).getTime() - new Date(a.last_seen_at).getTime()
  })
}

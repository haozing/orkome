import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '@ploykit/client'
import { MailPlus } from 'lucide-react'
import {
  acceptInvitation,
  declineInvitation,
  meQueryOptions,
  myInvitationsQueryOptions,
} from '@/api/identity'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { switchWorkspace } from '@/services/workspace-switch'

// 我的邀请条（design §3.5 收件方视角）：账户区顶部，有待处理邀请才显示。
// accept → 200 Workspace：刷新 me 缓存并切到该工作区（switchWorkspace 语义）；
// decline → {"status":"ok"}，原地从列表移除。
// 视觉：amber「通知条」容器 + 邀请意图图标，workspace 名套 Badge。
export function InvitationsBanner() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const invitations = useQuery(myInvitationsQueryOptions)
  const [error, setError] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<string | null>(null)

  const accept = useMutation({
    mutationFn: acceptInvitation,
    onSuccess: (ws) => {
      setError(null)
      void queryClient.invalidateQueries({ queryKey: myInvitationsQueryOptions.queryKey })
      void queryClient.invalidateQueries({ queryKey: meQueryOptions.queryKey })
      switchWorkspace(queryClient, ws.id)
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : String(err)),
  })

  const decline = useMutation({
    mutationFn: declineInvitation,
    onSuccess: () => {
      setError(null)
      void queryClient.invalidateQueries({ queryKey: myInvitationsQueryOptions.queryKey })
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : String(err)),
  })

  const act = (id: string, fn: (id: string) => void) => {
    setBusyId(id)
    fn(id)
  }
  const clearBusy = () => setBusyId(null)

  if (invitations.isPending || invitations.isError || !invitations.data?.length) return null

  return (
    <div
      className="rounded-lg border border-amber-200 bg-amber-50 p-3 dark:border-amber-900 dark:bg-amber-950/40"
      data-testid="invitations-banner"
    >
      <p className="flex items-center gap-1.5 text-sm font-medium">
        <span className="flex items-center text-amber-600 dark:text-amber-400" aria-hidden="true">
          <MailPlus className="size-3.5" />
        </span>
        {t('settings.invitations.title')}
      </p>
      <ul className="mt-2 space-y-2">
        {invitations.data.map((inv) => (
          <li key={inv.id} className="flex flex-wrap items-center gap-2 text-sm">
            <Badge variant="secondary">{inv.workspace_name}</Badge>
            <span className="font-mono text-xs text-zinc-500 dark:text-zinc-400">
              /{inv.workspace_slug}
            </span>
            <Badge variant="neutral">{t('settings.invitations.role', { role: inv.role })}</Badge>
            <span className="min-w-0 flex-1" />
            <Button
              size="sm"
              disabled={busyId === inv.id}
              onClick={() => {
                act(inv.id, (id) => accept.mutate(id, { onSettled: clearBusy }))
              }}
              data-testid={`invitation-accept-${inv.id}`}
            >
              {t('settings.invitations.accept')}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={busyId === inv.id}
              onClick={() => {
                act(inv.id, (id) => decline.mutate(id, { onSettled: clearBusy }))
              }}
              data-testid={`invitation-decline-${inv.id}`}
            >
              {t('settings.invitations.decline')}
            </Button>
          </li>
        ))}
      </ul>
      {error && (
        <p className="mt-2 text-xs text-destructive" role="alert">
          {error}
        </p>
      )}
    </div>
  )
}

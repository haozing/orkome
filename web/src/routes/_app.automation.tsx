import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  deleteAutopilot,
  listAutopilots,
  setAutopilotEnabled,
  triggerAutopilot,
  type Autopilot,
} from '@/api/automation'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
export const Route = createFileRoute('/_app/automation')({
  component: AutomationPage,
})
const EXEC_MODE_KEYS: Record<string, string> = {
  create_work_item: 'automation.modeCreate',
  task_only: 'automation.modeTaskOnly',
}
const CARD_CLASS = 'rounded-lg border border-zinc-200 p-4 dark:border-zinc-800'
const CARD_TITLE_CLASS = 'mb-1 text-sm font-semibold'
const CARD_DESC_CLASS = 'mb-3 text-xs text-zinc-500 dark:text-zinc-400'
function autopilotStatusKey(a: Autopilot): { key: string; tone: 'success' | 'neutral' | 'danger' } {
  if (a.paused_reason) return { key: 'automation.stFused', tone: 'danger' }
  if (!a.enabled) return { key: 'automation.stDisabled', tone: 'neutral' }
  return { key: 'automation.stEnabled', tone: 'success' }
}
interface TriggerFeedback {
  name: string
  status: string
  reasonCode?: string
}
function AutomationPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const autopilots = useQuery({ queryKey: ['autopilots'], queryFn: listAutopilots })
  const [feedback, setFeedback] = useState<TriggerFeedback | null>(null)
  const [confirmingId, setConfirmingId] = useState<string | null>(null)
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['autopilots'] })
  const toggle = useMutation({
    mutationFn: (a: Autopilot) => setAutopilotEnabled(a.id, !a.enabled),
    onSuccess: () => void invalidate(),
  })
  const trigger = useMutation({
    mutationFn: triggerAutopilot,
    onSuccess: (run) => {
      const ap = autopilots.data?.find((a) => a.id === run.autopilot_id)
      setFeedback({
        name: ap?.name ?? run.autopilot_id,
        status: run.status,
        reasonCode: run.reason_code,
      })
    },
  })
  const remove = useMutation({
    mutationFn: deleteAutopilot,
    onSuccess: () => {
      setConfirmingId(null)
      void invalidate()
    },
  })
  const actionError = toggle.error ?? trigger.error ?? remove.error
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-lg font-semibold">{t('nav.automation')}</h1>
        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{t('automation.intro')}</p>
      </div>

      <section className={CARD_CLASS}>
        <h2 className={CARD_TITLE_CLASS}>{t('automation.mineTitle')}</h2>
        <p className={CARD_DESC_CLASS}>{t('automation.mineDesc')}</p>

        {feedback && (
          <div role="status" className="mb-3 rounded-md bg-zinc-100 p-2.5 text-xs dark:bg-zinc-900">
            {t('automation.triggered', { name: feedback.name, status: feedback.status })}
            {feedback.reasonCode && t('automation.triggeredReason', { code: feedback.reasonCode })}
          </div>
        )}

        {autopilots.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
        {autopilots.error && (
          <p className="text-sm text-red-600 dark:text-red-400">{autopilots.error.message}</p>
        )}
        {autopilots.data && autopilots.data.length === 0 && (
          <p className="text-sm text-zinc-500">{t('automation.empty')}</p>
        )}

        {actionError && (
          <p className="mt-2 text-xs text-red-600 dark:text-red-400">{actionError.message}</p>
        )}

        {autopilots.data && autopilots.data.length > 0 && (
          <div className="divide-y divide-zinc-200 dark:divide-zinc-800">
            {autopilots.data.map((a) => {
              const status = autopilotStatusKey(a)
              const confirming = confirmingId === a.id
              return (
                <div key={a.id} className="py-2.5">
                  <div className="flex items-center gap-3">
                    <div className="min-w-0 flex-1">
                      <span className="text-sm font-medium">{a.name}</span>
                      <span className="ml-2 text-xs text-zinc-500 dark:text-zinc-400">
                        {EXEC_MODE_KEYS[a.exec_mode] ? t(EXEC_MODE_KEYS[a.exec_mode]) : a.exec_mode}
                      </span>
                    </div>
                    <Badge variant={status.tone}>{t(status.key)}</Badge>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={!a.enabled || trigger.isPending}
                      onClick={() => trigger.mutate(a.id)}
                    >
                      {t('automation.triggerNow')}
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={toggle.isPending}
                      onClick={() => toggle.mutate(a)}
                    >
                      {a.enabled ? t('automation.disable') : t('automation.enable')}
                    </Button>
                    {confirming ? (
                      <div className="flex items-center gap-1">
                        <Button
                          variant="danger"
                          size="sm"
                          disabled={remove.isPending}
                          onClick={() => remove.mutate(a.id)}
                        >
                          {t('automation.confirmDelete')}
                        </Button>
                        <Button variant="ghost" size="sm" onClick={() => setConfirmingId(null)}>
                          {t('common.cancel')}
                        </Button>
                      </div>
                    ) : (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-red-600 dark:text-red-400"
                        onClick={() => setConfirmingId(a.id)}
                      >
                        {t('common.delete')}
                      </Button>
                    )}
                  </div>
                  {a.paused_reason && (
                    <p className={cn('mt-1 text-xs text-red-600 dark:text-red-400')}>
                      {t('automation.fusedNote', { reason: a.paused_reason })}
                    </p>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </section>
    </div>
  )
}

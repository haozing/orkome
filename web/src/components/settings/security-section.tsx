import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api, ApiError } from '@ploykit/client'
import { PasswordField } from '@ploykit/ui'
import { Button } from '@/components/ui/button'
import { MfaCard } from '@/components/settings/mfa-card'
import { SessionsCard } from '@/components/settings/sessions-card'

// 修改密码卡片（design §3.2a）：提交前警示「退出所有其他设备」；
// 响应 200 + Set-Cookie 新 session（当前设备会话已轮换，无需登出）；
// 弱密码 400 错误消息原样展示；无密码账户引导文案记入缓办（§3.2d）。
function PasswordCard() {
  const { t } = useTranslation()
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [status, setStatus] = useState<{ kind: 'saved' | 'error'; message?: string } | null>(null)

  const change = useMutation({
    mutationFn: async () => {
      await api.post<{ expires_at: string }>('/auth/change-password', {
        old_password: oldPassword,
        new_password: newPassword,
      })
    },
    onSuccess: () => {
      setOldPassword('')
      setNewPassword('')
      setStatus({ kind: 'saved' })
    },
    onError: (err) => {
      setStatus({ kind: 'error', message: err instanceof ApiError ? err.message : String(err) })
    },
  })

  const canSubmit = oldPassword.length > 0 && newPassword.length > 0 && !change.isPending

  return (
    <div className="space-y-3" data-testid="password-card">
      <div>
        <p className="text-sm font-medium">{t('settings.password.title')}</p>
        <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('settings.password.desc')}</p>
      </div>
      <form
        className="max-w-md space-y-3"
        onSubmit={(e) => {
          e.preventDefault()
          if (canSubmit) change.mutate()
        }}
      >
        {/* 密码框采纳 kit PasswordField（Input 官方配方加可见性切换，视觉同
            orkome Input：h-8 / rounded-lg / border-input）；testid 落在内部 input。 */}
        <PasswordField
          value={oldPassword}
          onChange={(e) => setOldPassword(e.target.value)}
          autoComplete="current-password"
          placeholder={t('settings.password.oldPlaceholder')}
          aria-label={t('settings.password.oldPlaceholder')}
          data-testid="password-old-input"
        />
        <PasswordField
          value={newPassword}
          onChange={(e) => setNewPassword(e.target.value)}
          autoComplete="new-password"
          placeholder={t('settings.password.newPlaceholder')}
          aria-label={t('settings.password.newPlaceholder')}
          data-testid="password-new-input"
        />
        <p className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-900 dark:bg-amber-950/40 dark:text-amber-300">
          {t('settings.password.warnOtherDevices')}
        </p>
        <div className="flex items-center justify-end gap-3">
          <p
            aria-live="polite"
            data-testid="password-status"
            className={
              status?.kind === 'error'
                ? 'text-xs text-red-600 dark:text-red-400'
                : 'mr-auto text-xs text-zinc-500 dark:text-zinc-400'
            }
          >
            {status?.kind === 'saved' && t('settings.password.saved')}
            {status?.kind === 'error' && (
              <>
                {t('common.saveFailed')}
                {status.message && <span className="ml-1 break-all">{status.message}</span>}
              </>
            )}
          </p>
          <Button type="submit" disabled={!canSubmit} data-testid="password-save">
            {change.isPending ? t('common.saving') : t('settings.password.submit')}
          </Button>
        </div>
      </form>
    </div>
  )
}

// 密码与安全区块（design §二-2）：改密 / MFA / 会话三卡片合一。
export function SecuritySection() {
  const { t } = useTranslation()
  return (
    <div className="divide-y divide-zinc-100 dark:divide-zinc-800">
      <div className="pb-4">
        <PasswordCard />
      </div>
      <div className="py-4">
        <MfaCard />
      </div>
      <div className="py-4">
        <SessionsCard />
      </div>
      <p className="pt-3 text-xs text-muted-foreground">{t('settings.noPasswordHint')}</p>
    </div>
  )
}

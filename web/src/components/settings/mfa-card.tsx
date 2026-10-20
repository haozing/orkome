import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { SecretModal, useConfirm } from '@ploykit/ui'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '@ploykit/client'
import { mfaDisable, mfaEnable, mfaEnroll, mfaStatusQueryOptions } from '@/api/identity'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { CopyButton, signOutEverywhere } from '@/components/settings/shared'

// MFA（TOTP）卡片（design §3.2b）：
// 未启用 → enroll（展示 otpauth_uri 文本 + secret 手输回退，不引 qrcode 依赖）
//        → enable（6 位码）→ 备份码一次性 SecretModal（多值形态：逐码展示 + 复制全部，
//          「已保存」门控内置；关闭不可再取警示由组件 description 说一次）。
// 已启用 → disable 二次确认（useConfirm，警示吊销全部会话含当前 → 登出流程）。
export function MfaCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const status = useQuery(mfaStatusQueryOptions)

  // enroll 产物只存组件 state（secret 一次性，不入缓存不入 localStorage）。
  const [enrollment, setEnrollment] = useState<{ otpauth_uri: string; secret: string } | null>(null)
  const [code, setCode] = useState('')
  const [backupCodes, setBackupCodes] = useState<string[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const invalidate = () => void queryClient.invalidateQueries({ queryKey: mfaStatusQueryOptions.queryKey })

  const enroll = useMutation({
    mutationFn: mfaEnroll,
    onSuccess: (res) => {
      setError(null)
      setEnrollment({ otpauth_uri: res.otpauth_uri, secret: res.secret })
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : String(err)),
  })

  const enable = useMutation({
    mutationFn: () => mfaEnable(code.trim()),
    onSuccess: (res) => {
      setError(null)
      setEnrollment(null)
      setCode('')
      setBackupCodes(res.backup_codes)
      invalidate()
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : String(err)),
  })

  // disable 吊销全部 session 含当前——成功后走登出流程。
  const disable = useMutation({
    mutationFn: mfaDisable,
    onSuccess: () => {
      invalidate()
      void signOutEverywhere(queryClient)
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : String(err)),
  })

  const codeReady = /^\d{6}$/.test(code.trim())

  // disable 吊销全部会话含当前——确认后执行，成功走登出流程。
  const confirmDisable = async () => {
    const ok = await confirm({
      title: t('settings.mfa.disableConfirmTitle'),
      description: t('settings.mfa.disableConfirmDesc'),
      confirmText: t('settings.mfa.disableConfirmAction'),
      cancelText: t('common.cancel'),
      danger: true,
    })
    if (ok) disable.mutate()
  }

  return (
    <div className="space-y-3" data-testid="mfa-card">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="text-sm font-medium">{t('settings.mfa.title')}</p>
        </div>
        {status.isPending ? (
          <span className="text-xs text-zinc-400">{t('common.loading')}</span>
        ) : status.data?.enabled ? (
          <Button
            variant="outline"
            size="sm"
            className="text-red-600 dark:text-red-400"
            onClick={() => void confirmDisable()}
            data-testid="mfa-disable"
          >
            {t('settings.mfa.disable')}
          </Button>
        ) : (
          <Button
            size="sm"
            variant="outline"
            onClick={() => enroll.mutate()}
            disabled={enroll.isPending}
            data-testid="mfa-enroll"
          >
            {enroll.isPending ? t('common.loading') : t('settings.mfa.enable')}
          </Button>
        )}
      </div>

      {error && (
        <p className="text-xs text-red-600 dark:text-red-400" role="alert">
          {error}
        </p>
      )}

      {enrollment && (
        <div className="max-w-md space-y-3 rounded-md border border-zinc-200 p-3 dark:border-zinc-700">
          <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('settings.mfa.enrollHint')}</p>
          <div className="space-y-2">
            <CodeField value={enrollment.otpauth_uri} />
            <div className="space-y-1">
              <p className="text-xs text-zinc-500 dark:text-zinc-400">
                {t('settings.mfa.secretLabel')}
              </p>
              <CodeField value={enrollment.secret} />
            </div>
          </div>
          <div className="flex max-w-xs gap-2">
            <Input
              value={code}
              onChange={(e) => setCode(e.target.value)}
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              placeholder={t('settings.mfa.codePlaceholder')}
              aria-label={t('settings.mfa.codeLabel')}
              data-testid="mfa-code-input"
            />
            <Button
              onClick={() => enable.mutate()}
              disabled={!codeReady || enable.isPending}
              data-testid="mfa-enable"
            >
              {enable.isPending ? t('common.loading') : t('settings.mfa.confirm')}
            </Button>
            <Button variant="ghost" onClick={() => setEnrollment(null)}>
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      )}

      {/* 备份码一次性展示（ui-sweep 裁决③ 收口兑现）：kit SecretModal 多值形态
          （secretValue: string[]）——逐码只读框 + 单个「复制全部」（'\n' 连接纯文本），
          「我已保存」勾选前关不掉（ESC/遮罩/关闭钮均被组件拦截）；
          关闭即不可再取的警示由组件内置 description 说一次（design §八-2）。 */}
      {backupCodes && (
        <SecretModal
          open
          onOpenChange={(o) => {
            if (!o) setBackupCodes(null)
          }}
          secretName={t('settings.mfa.backupTitle')}
          secretValue={backupCodes}
          title={t('settings.mfa.backupTitle')}
        />
      )}
    </div>
  )
}

// 只读代码块（otpauth 链接 / secret 手输回退）：等宽 + 全断行 + 复制按钮
// （共享 CopyButton，ghost xs 次级形态），视觉参照 access 页 Snippet 的代码块
// （浅 zinc 底，紧凑行高）。
function CodeField({ value }: { value: string }) {
  return (
    <div className="flex items-start gap-2 rounded-md bg-zinc-50 p-2.5 dark:bg-zinc-900">
      <code className="min-w-0 flex-1 break-all font-mono text-xs leading-relaxed">{value}</code>
      <CopyButton
        text={value}
        variant="ghost"
        size="xs"
        className="shrink-0 text-zinc-500 dark:text-zinc-400"
      />
    </div>
  )
}

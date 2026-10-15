import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '@ploykit/client'
import { mfaDisable, mfaEnable, mfaEnroll, mfaStatusQueryOptions } from '@/api/identity'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { signOutEverywhere } from '@/components/settings/shared'

// MFA（TOTP）卡片（design §3.2b）：
// 未启用 → enroll（展示 otpauth_uri 文本 + secret 手输回退，不引 qrcode 依赖）
//        → enable（6 位码）→ 备份码一次性 Dialog（复制 + 离开不可再取警示）。
// 已启用 → disable 二次确认（AlertDialog，警示吊销全部会话含当前 → 登出流程）。
export function MfaCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const status = useQuery(mfaStatusQueryOptions)

  // enroll 产物只存组件 state（secret 一次性，不入缓存不入 localStorage）。
  const [enrollment, setEnrollment] = useState<{ otpauth_uri: string; secret: string } | null>(null)
  const [code, setCode] = useState('')
  const [backupCodes, setBackupCodes] = useState<string[] | null>(null)
  const [confirmDisable, setConfirmDisable] = useState(false)
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
      setConfirmDisable(false)
      invalidate()
      void signOutEverywhere(queryClient)
    },
    onError: (err) => {
      setConfirmDisable(false)
      setError(err instanceof ApiError ? err.message : String(err))
    },
  })

  const codeReady = /^\d{6}$/.test(code.trim())

  return (
    <div className="space-y-3" data-testid="mfa-card">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="text-sm font-medium">{t('settings.mfa.title')}</p>
          <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('settings.mfa.desc')}</p>
        </div>
        {status.isPending ? (
          <span className="text-xs text-zinc-400">{t('common.loading')}</span>
        ) : status.data?.enabled ? (
          <Button
            variant="outline"
            size="sm"
            className="text-red-600 dark:text-red-400"
            onClick={() => setConfirmDisable(true)}
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

      {/* 备份码一次性展示：关闭即不可再取（design §八-2）。 */}
      <Dialog
        open={backupCodes !== null}
        onOpenChange={(open) => {
          if (!open) setBackupCodes(null)
        }}
      >
        <DialogContent data-testid="mfa-backup-codes-dialog">
          <DialogHeader>
            <DialogTitle>{t('settings.mfa.backupTitle')}</DialogTitle>
            <DialogDescription>{t('settings.mfa.backupWarn')}</DialogDescription>
          </DialogHeader>
          <ul
            className="grid grid-cols-2 gap-1.5 font-mono text-sm sm:grid-cols-3"
            data-testid="mfa-backup-codes"
          >
            {backupCodes?.map((c) => (
              <li
                key={c}
                className="rounded-md bg-zinc-50 px-2 py-1.5 text-center tracking-wider dark:bg-zinc-900"
              >
                {c}
              </li>
            ))}
          </ul>
          <div className="flex justify-end gap-2">
            <Button
              variant="outline"
              onClick={() => void navigator.clipboard.writeText((backupCodes ?? []).join('\n'))}
            >
              {t('settings.mfa.copyCodes')}
            </Button>
            <Button onClick={() => setBackupCodes(null)}>{t('common.ok')}</Button>
          </div>
        </DialogContent>
      </Dialog>

      <AlertDialog open={confirmDisable} onOpenChange={setConfirmDisable}>
        <AlertDialogContent>
          <AlertDialogTitle>{t('settings.mfa.disableConfirmTitle')}</AlertDialogTitle>
          <AlertDialogDescription>{t('settings.mfa.disableConfirmDesc')}</AlertDialogDescription>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              variant="danger"
              disabled={disable.isPending}
              onClick={() => disable.mutate()}
              data-testid="mfa-disable-confirm"
            >
              {t('settings.mfa.disableConfirmAction')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

// 只读代码块（otpauth 链接 / secret 手输回退）：等宽 + 全断行 + 复制按钮，
// 视觉参照 access 页 Snippet 的代码块（浅 zinc 底，紧凑行高）。
function CodeField({ value }: { value: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex items-start gap-2 rounded-md bg-zinc-50 p-2.5 dark:bg-zinc-900">
      <code className="min-w-0 flex-1 break-all font-mono text-xs leading-relaxed">{value}</code>
      <Button
        variant="ghost"
        size="xs"
        className="shrink-0 text-zinc-500 dark:text-zinc-400"
        onClick={() => {
          void navigator.clipboard.writeText(value).then(
            () => {
              setCopied(true)
              setTimeout(() => setCopied(false), 1500)
            },
            () => {},
          )
        }}
        aria-label={t('common.copy')}
      >
        {copied ? (
          <Check className="size-3.5" aria-hidden="true" />
        ) : (
          <Copy className="size-3.5" aria-hidden="true" />
        )}
        {copied ? t('common.copied') : t('common.copy')}
      </Button>
    </div>
  )
}

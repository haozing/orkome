import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useForm, useWatch, type Resolver } from 'react-hook-form'
import { z } from 'zod'
import { meQueryOptions, sendLoginCode, verifyLoginCode } from '@/api/identity'
import { AuthShell, FormField } from '@ploykit/ui'
import { ApiError } from '@ploykit/client'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
export const Route = createFileRoute('/login')({
  beforeLoad: async ({ context }) => {
    try {
      await context.queryClient.ensureQueryData(meQueryOptions)
    } catch {
      return
    }
    throw redirect({ to: '/work-items' })
  },
  component: LoginPage,
})
// 发送验证码 60s 冷却（照 kit LoginPage 同名模式）：验证码接口按邮箱计费/限频，
// 双击与「换个邮箱」返回后连发都靠它拦（ui-sweep Wave2 修复④）。
const SEND_COOLDOWN_S = 60
const emailFormSchema = (t: (k: string) => string) =>
  z.object({
    email: z.email(t('login.emailInvalid')),
    code: z.string().optional(),
  })
const codeFormSchema = (t: (k: string) => string) =>
  z.object({
    email: z.email(t('login.emailInvalid')),
    code: z.string().regex(/^\d{6}$/, t('login.codeInvalid')),
  })
interface LoginFormValues {
  email: string
  code?: string
}
function LoginPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [step, setStep] = useState<'email' | 'code'>('email')
  const [cooldown, setCooldown] = useState(0)
  useEffect(() => {
    if (cooldown <= 0) return
    const timer = setInterval(() => setCooldown((c) => c - 1), 1_000)
    return () => clearInterval(timer)
  }, [cooldown])
  const form = useForm<LoginFormValues>({
    resolver: zodResolver(
      step === 'email' ? emailFormSchema(t) : codeFormSchema(t),
    ) as Resolver<LoginFormValues>,
    defaultValues: { email: '', code: '' },
  })
  const emailValue = useWatch({ control: form.control, name: 'email' })
  const sendCode = useMutation({
    mutationFn: (email: string) => sendLoginCode(email),
    onSuccess: () => {
      setStep('code')
      setCooldown(SEND_COOLDOWN_S)
    },
  })
  const verifyCode = useMutation({
    mutationFn: (v: LoginFormValues) => verifyLoginCode(v.email, v.code ?? ''),
    onSuccess: () => {
      void navigate({ to: '/work-items' })
    },
  })
  const submitting = sendCode.isPending || verifyCode.isPending
  const rawError = sendCode.error ?? verifyCode.error
  const error =
    rawError instanceof ApiError ? rawError.message : rawError ? t('common.requestFailed') : null
  const onSubmit = form.handleSubmit((values) => {
    if (step === 'email') {
      if (cooldown > 0) return
      sendCode.mutate(values.email)
    } else {
      verifyCode.mutate(values)
    }
  })
  return (
    <AuthShell
      brand={<span className="text-lg font-bold text-foreground">Orkome</span>}
    >
      <div className="space-y-4">
        <div className="text-center">
          <h1 className="text-2xl font-bold">{t('login.title')}</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {step === 'email'
              ? t('login.emailStepDesc')
              : t('login.codeSentTo', { email: emailValue })}
          </p>
        </div>

        <form onSubmit={onSubmit} className="space-y-4" noValidate>
          {step === 'code' && (
            <FormField
              label={t('login.codeLabel')}
              htmlFor="login-code"
              error={form.formState.errors.code?.message}
            >
              <Input
                id="login-code"
                inputMode="numeric"
                autoComplete="one-time-code"
                autoFocus
                {...form.register('code')}
              />
            </FormField>
          )}

          <FormField
            label={t('login.emailLabel')}
            htmlFor="login-email"
            error={form.formState.errors.email?.message}
          >
            <Input
              id="login-email"
              type="email"
              autoComplete="email"
              disabled={step === 'code'}
              {...form.register('email')}
            />
          </FormField>

          {error && (
            <p role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}

          <Button
            type="submit"
            className="w-full"
            disabled={submitting || (step === 'email' && cooldown > 0)}
          >
            {step === 'email'
              ? sendCode.isPending
                ? t('login.sending')
                : cooldown > 0
                  ? t('login.resendCooldown', { seconds: cooldown })
                  : t('login.sendCode')
              : verifyCode.isPending
                ? t('login.signingIn')
                : t('common.login')}
          </Button>

          {step === 'code' && (
            <Button
              type="button"
              variant="ghost"
              className="w-full"
              onClick={() => {
                sendCode.reset()
                verifyCode.reset()
                setStep('email')
              }}
            >
              {t('login.changeEmail')}
            </Button>
          )}
        </form>
      </div>
    </AuthShell>
  )
}

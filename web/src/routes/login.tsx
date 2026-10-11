import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useForm, useWatch, type Resolver } from 'react-hook-form'
import { z } from 'zod'
import { meQueryOptions, sendLoginCode, verifyLoginCode } from '@/api/identity'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ApiError } from '@ploykit/client'
import { useTranslation } from 'react-i18next'
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
  const form = useForm<LoginFormValues>({
    resolver: zodResolver(
      step === 'email' ? emailFormSchema(t) : codeFormSchema(t),
    ) as Resolver<LoginFormValues>,
    defaultValues: { email: '', code: '' },
  })
  const emailValue = useWatch({ control: form.control, name: 'email' })
  const sendCode = useMutation({
    mutationFn: (email: string) => sendLoginCode(email),
    onSuccess: () => setStep('code'),
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
    if (step === 'email') sendCode.mutate(values.email)
    else verifyCode.mutate(values)
  })
  return (
    <div className="flex min-h-dvh items-center justify-center p-6">
      <div className="w-full max-w-sm">
        <h1 className="mb-1 text-xl font-semibold">{t('login.title')}</h1>
        <p className="mb-6 text-sm text-zinc-500 dark:text-zinc-400">
          {step === 'email'
            ? t('login.emailStepDesc')
            : t('login.codeSentTo', { email: emailValue })}
        </p>

        <form onSubmit={onSubmit} className="space-y-4" noValidate>
          {step === 'code' && (
            <div>
              <label htmlFor="login-code" className="mb-1 block text-sm font-medium">
                {t('login.codeLabel')}
              </label>
              <Input
                id="login-code"
                inputMode="numeric"
                autoComplete="one-time-code"
                autoFocus
                {...form.register('code')}
              />
              {form.formState.errors.code && (
                <p className="mt-1 text-xs text-red-600 dark:text-red-400">
                  {form.formState.errors.code.message}
                </p>
              )}
            </div>
          )}

          <div>
            <label htmlFor="login-email" className="mb-1 block text-sm font-medium">
              {t('login.emailLabel')}
            </label>
            <Input
              id="login-email"
              type="email"
              autoComplete="email"
              disabled={step === 'code'}
              {...form.register('email')}
            />
            {form.formState.errors.email && (
              <p className="mt-1 text-xs text-red-600 dark:text-red-400">
                {form.formState.errors.email.message}
              </p>
            )}
          </div>

          {error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {error}
            </p>
          )}

          <Button type="submit" className="w-full" disabled={submitting}>
            {submitting
              ? t('common.loading')
              : step === 'email'
                ? t('login.sendCode')
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
    </div>
  )
}

import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { useForm, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import { createWorkspace, joinByShareCode, meQueryOptions } from '@/api/identity'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ApiError } from '@ploykit/client'
import { useWorkspaceStore } from '@/stores/workspace'
export const Route = createFileRoute('/onboarding')({
  component: OnboardingPage,
})
const slugRegex = /^[a-z0-9-]{4,40}$/
const createSchema = (t: (k: string) => string) =>
  z.object({
    name: z.string().trim().min(1, t('onboarding.required')).max(100, t('onboarding.max100')),
    slug: z.string().trim().regex(slugRegex, t('onboarding.slugRule')),
  })
const joinSchema = (t: (k: string) => string) =>
  z.object({
    code: z.string().trim().min(4, t('onboarding.codeRequired')),
  })
interface CreateValues {
  name: string
  slug: string
}
function suggestSlug(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 40)
}
function OnboardingPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const setCurrent = useWorkspaceStore((s) => s.setCurrent)
  const createForm = useForm<CreateValues>({
    resolver: zodResolver(createSchema(t)) as Resolver<CreateValues>,
    defaultValues: { name: '', slug: '' },
  })
  const create = useMutation({
    mutationFn: (v: CreateValues) => createWorkspace(v.name, v.slug),
    onSuccess: async (ws) => {
      setCurrent(ws.id)
      await queryClient.refetchQueries({ queryKey: meQueryOptions.queryKey, type: 'all' })
      void navigate({ to: '/work-items' })
    },
  })
  const joinForm = useForm<{
    code: string
  }>({
    resolver: zodResolver(joinSchema(t)) as Resolver<{
      code: string
    }>,
    defaultValues: { code: '' },
  })
  const join = useMutation({
    mutationFn: (v: { code: string }) => joinByShareCode(v.code),
    onSuccess: async (member) => {
      setCurrent(member.workspace_id)
      await queryClient.refetchQueries({ queryKey: meQueryOptions.queryKey, type: 'all' })
      void navigate({ to: '/work-items' })
    },
  })
  const fieldError = (msg?: string) =>
    msg && <p className="mt-1 text-xs text-red-600 dark:text-red-400">{msg}</p>
  const mutationError = (err: unknown) =>
    err ? (
      <p role="alert" className="text-xs text-red-600 dark:text-red-400">
        {err instanceof ApiError ? err.message : t('common.requestFailed')}
      </p>
    ) : null
  return (
    <div className="flex min-h-dvh items-center justify-center p-6">
      <div className="grid w-full max-w-2xl gap-6 md:grid-cols-2">
        <section className="rounded-lg border border-zinc-200 p-5 dark:border-zinc-800">
          <h1 className="mb-1 text-base font-semibold">{t('onboarding.createTitle')}</h1>
          <p className="mb-4 text-xs text-zinc-500 dark:text-zinc-400">
            {t('onboarding.createDesc')}
          </p>
          <form
            className="space-y-3"
            noValidate
            onSubmit={createForm.handleSubmit((v) => create.mutate(v))}
          >
            <div>
              <label htmlFor="ws-name" className="mb-1 block text-sm font-medium">
                {t('agents.name')}
              </label>
              <Input
                id="ws-name"
                autoFocus
                {...createForm.register('name', {
                  onBlur: (e) => {
                    if (!createForm.getValues('slug')) {
                      createForm.setValue('slug', suggestSlug(e.target.value))
                    }
                  },
                })}
              />
              {fieldError(createForm.formState.errors.name?.message)}
            </div>
            <div>
              <label htmlFor="ws-slug" className="mb-1 block text-sm font-medium">
                {t('onboarding.slugLabel')}
              </label>
              <Input id="ws-slug" {...createForm.register('slug')} />
              {fieldError(createForm.formState.errors.slug?.message)}
            </div>
            {mutationError(create.error)}
            <Button type="submit" className="w-full" disabled={create.isPending}>
              {create.isPending ? t('skills.creating') : t('common.create')}
            </Button>
          </form>
        </section>

        <section className="rounded-lg border border-zinc-200 p-5 dark:border-zinc-800">
          <h1 className="mb-1 text-base font-semibold">{t('onboarding.joinTitle')}</h1>
          <p className="mb-4 text-xs text-zinc-500 dark:text-zinc-400">
            {t('onboarding.joinDesc')}
          </p>
          <form
            className="space-y-3"
            noValidate
            onSubmit={joinForm.handleSubmit((v) => join.mutate(v))}
          >
            <div>
              <label htmlFor="join-code" className="mb-1 block text-sm font-medium">
                {t('onboarding.codeLabel')}
              </label>
              <Input id="join-code" {...joinForm.register('code')} />
              {fieldError(joinForm.formState.errors.code?.message)}
            </div>
            {mutationError(join.error)}
            <Button type="submit" variant="outline" className="w-full" disabled={join.isPending}>
              {join.isPending ? t('onboarding.joining') : t('onboarding.join')}
            </Button>
          </form>
        </section>
      </div>
    </div>
  )
}

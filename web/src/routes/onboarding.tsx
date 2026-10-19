import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, redirect, useNavigate } from '@tanstack/react-router'
import { useForm, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import { logout, createWorkspace, joinByShareCode, meQueryOptions } from '@/api/identity'
import { AuthShell, Card, CardContent, CardDescription, CardHeader, CardTitle, FormField } from '@ploykit/ui'
import { ApiError } from '@ploykit/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { wsClient } from '@/realtime/client'
import { useWorkspaceStore } from '@/stores/workspace'
export const Route = createFileRoute('/onboarding')({
  // Escape hatch for the login/onboarding redirect loop: without a session,
  // /login bounces authed users here and this page had no way back out
  // (audit FP-17 P1-1). Unauthenticated visitors go to /login instead of
  // hitting a bare 401 on submit.
  beforeLoad: async ({ context }) => {
    try {
      await context.queryClient.ensureQueryData(meQueryOptions)
    } catch {
      throw redirect({ to: '/login' })
    }
  },
  component: OnboardingPage,
})
// Aligned with the backend rule (ploykit domain/rules.go):
// ^[a-z0-9][a-z0-9-]{2,38}[a-z0-9]$ — 4-40 chars, no leading/trailing hyphen
// (audit FP-02 P1-3: the old /^[a-z0-9-]{4,40}$/ let "-abcd"/"abcd-" through
// only for the backend to reject them with an English 400).
const slugRegex = /^[a-z0-9][a-z0-9-]{2,38}[a-z0-9]$/
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
    // The redeem endpoint answers a Workspace; switching to the joined
    // workspace means using its `id` (there is no workspace_id field —
    // audit FP-02 P0-3: undefined id made multi-workspace users land in
    // the wrong workspace).
    onSuccess: async (ws) => {
      setCurrent(ws.id)
      await queryClient.refetchQueries({ queryKey: meQueryOptions.queryKey, type: 'all' })
      void navigate({ to: '/work-items' })
    },
  })
  // Second way out of the login<->onboarding ping-pong (typo'd email => JIT
  // account with zero workspaces): log out and return to /login
  // (audit FP-17 P1-1).
  const signOut = async () => {
    await logout()
    useWorkspaceStore.getState().setCurrent(null)
    queryClient.clear()
    wsClient.close()
    void navigate({ to: '/login' })
  }
  const mutationError = (err: unknown) =>
    err ? (
      <p role="alert" className="text-sm text-destructive">
        {err instanceof ApiError ? err.message : t('common.requestFailed')}
      </p>
    ) : null
  return (
    // AuthShell 内容槽固定 max-w-sm；双卡布局需要 2xl——外层用任意变体选择器
    // 覆写内层宽度（.wrapper>div>div 特异性高于 .max-w-sm，稳定胜出）。
    <div className="w-full [&>div>div]:max-w-2xl">
      <AuthShell
        brand={<span className="text-lg font-bold text-foreground">Orkome</span>}
        footer={
          // 第二条逃生通道（audit FP-17 P1-1）：退出登录回 /login。
          // 旧实现按钮挂在外层 flex 行容器上，mt-6 在 row 方向失效、
          // 按钮被挤到卡片右侧（ui-sweep 真 bug①）；footer 槽天然纵向归位。
          <Button variant="ghost" size="sm" onClick={() => void signOut()}>
            {t('onboarding.useAnotherAccount')}
          </Button>
        }
      >
        <div className="space-y-6">
          <h1 className="text-center text-xl font-semibold">{t('onboarding.title')}</h1>
          <div className="grid gap-6 md:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle>{t('onboarding.createTitle')}</CardTitle>
                <CardDescription>{t('onboarding.createDesc')}</CardDescription>
              </CardHeader>
              <CardContent>
                <form
                  className="space-y-3"
                  noValidate
                  onSubmit={createForm.handleSubmit((v) => create.mutate(v))}
                >
                  <FormField
                    label={t('onboarding.nameLabel')}
                    htmlFor="ws-name"
                    error={createForm.formState.errors.name?.message}
                  >
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
                  </FormField>
                  <FormField
                    label={t('onboarding.slugLabel')}
                    htmlFor="ws-slug"
                    error={createForm.formState.errors.slug?.message}
                  >
                    <Input id="ws-slug" {...createForm.register('slug')} />
                  </FormField>
                  {mutationError(create.error)}
                  <Button type="submit" className="w-full" disabled={create.isPending}>
                    {create.isPending ? t('onboarding.creating') : t('common.create')}
                  </Button>
                </form>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>{t('onboarding.joinTitle')}</CardTitle>
                <CardDescription>{t('onboarding.joinDesc')}</CardDescription>
              </CardHeader>
              <CardContent>
                <form
                  className="space-y-3"
                  noValidate
                  onSubmit={joinForm.handleSubmit((v) => join.mutate(v))}
                >
                  <FormField
                    label={t('onboarding.codeLabel')}
                    htmlFor="join-code"
                    error={joinForm.formState.errors.code?.message}
                  >
                    <Input id="join-code" {...joinForm.register('code')} />
                  </FormField>
                  {mutationError(join.error)}
                  <Button type="submit" variant="outline" className="w-full" disabled={join.isPending}>
                    {join.isPending ? t('onboarding.joining') : t('onboarding.join')}
                  </Button>
                </form>
              </CardContent>
            </Card>
          </div>
        </div>
      </AuthShell>
    </div>
  )
}

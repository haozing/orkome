import { Dialog } from '@base-ui/react/dialog'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link } from '@tanstack/react-router'
import { ArrowLeft, Loader2, Plus, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import {
  archiveAgent,
  cancelAgentTasks,
  deleteAgentMcp,
  getAgent,
  getAgentEnv,
  listAgentMcp,
  listAgentTasks,
  putAgentEnv,
  resolvedAgentMcp,
  updateAgent,
  updateAgentMcp,
  upsertAgentMcp,
  type Agent,
  type AgentMCPServer,
  type InvocationTarget,
} from '@/api/orchestration'
import { membersQueryOptions } from '@/api/identity'
import {
  addAgentSkill,
  listAgentSkills,
  listSkills,
  removeAgentSkill,
  setAgentSkillEnabled,
} from '@/api/skills'
import { CredentialField } from '@/components/agent/credential-field'
import { visibilityMeta } from '@/components/agent/visibility'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { TaskStatusBadge } from '@/components/task-status-badge'
import { ApiError } from '@/lib/api'
import { useWorkspaceStore } from '@/stores/workspace'
const searchSchema = z.object({
  tab: z.enum(['base', 'model', 'skills', 'mcp', 'perm', 'tasks']).optional(),
})
type AgentTab = z.infer<typeof searchSchema>['tab']
export const Route = createFileRoute('/_app/agents/$agentId')({
  validateSearch: (search) => {
    const parsed = searchSchema.safeParse(search)
    return parsed.success ? parsed.data : {}
  },
  component: AgentDetailPage,
})
function AgentDetailPage() {
  const { t } = useTranslation()
  const { agentId } = Route.useParams()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const queryClient = useQueryClient()
  const agent = useQuery({ queryKey: ['agent', agentId], queryFn: () => getAgent(agentId) })
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['agents'] })
    void queryClient.invalidateQueries({ queryKey: ['agent', agentId] })
  }
  const archive = useMutation({
    mutationFn: (archived: boolean) => archiveAgent(agentId, archived),
    onSuccess: invalidate,
  })
  const cancelTasks = useMutation({
    mutationFn: () => cancelAgentTasks(agentId),
    onSuccess: invalidate,
  })
  const [confirming, setConfirming] = useState<'archive' | 'restore' | 'cancel' | null>(null)
  if (agent.isPending) return <PageLoading label={t('agents.loadingAgent')} />
  if (agent.isError) {
    return (
      <PageError
        title={t('agents.notFound')}
        message={agent.error instanceof Error ? agent.error.message : undefined}
      />
    )
  }
  const a = agent.data
  const model = [a.runtime_config?.provider, a.runtime_config?.model].filter(Boolean).join('·')
  const vis = visibilityMeta(a.visibility)
  const patchSearch = (tab: AgentTab) =>
    void navigate({ search: (prev) => ({ ...prev, tab }), replace: true })
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Link
          to="/agents"
          className="flex size-7 items-center justify-center rounded-md text-zinc-400 outline-none hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
          aria-label={t('agents.backToList')}
        >
          <ArrowLeft className="size-4" aria-hidden="true" />
        </Link>
        <h1
          className={'min-w-0 truncate text-lg font-semibold ' + (a.archived ? 'opacity-55' : '')}
        >
          {a.name}
        </h1>
        {model && <span className="font-mono text-xs text-zinc-400">{model}</span>}
        <Badge variant={vis.tone}>{t(vis.labelKey)}</Badge>
        {a.archived && <Badge variant="neutral">{t('agents.archived')}</Badge>}

        <div className="ml-auto flex items-center gap-2">
          {a.archived ? (
            confirming === 'restore' ? (
              <>
                <Button
                  size="sm"
                  disabled={archive.isPending}
                  onClick={() => archive.mutate(false)}
                >
                  {t('agents.confirmRestore')}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setConfirming(null)}>
                  {t('common.cancel')}
                </Button>
              </>
            ) : (
              <Button size="sm" variant="outline" onClick={() => setConfirming('restore')}>
                {t('agents.restore')}
              </Button>
            )
          ) : confirming === 'archive' ? (
            <>
              <Button
                size="sm"
                variant="danger"
                disabled={archive.isPending}
                onClick={() => archive.mutate(true)}
              >
                {t('agents.confirmArchive')}
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setConfirming(null)}>
                {t('common.cancel')}
              </Button>
            </>
          ) : (
            <Button size="sm" variant="outline" onClick={() => setConfirming('archive')}>
              {t('agents.archive')}
            </Button>
          )}

          {!a.archived &&
            (confirming === 'cancel' ? (
              <>
                <Button
                  size="sm"
                  variant="danger"
                  disabled={cancelTasks.isPending}
                  onClick={() => cancelTasks.mutate()}
                >
                  {t('agents.confirmCancelAll')}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setConfirming(null)}>
                  {t('common.cancel')}
                </Button>
              </>
            ) : (
              <Button size="sm" variant="ghost" onClick={() => setConfirming('cancel')}>
                {t('agents.cancelAll')}
              </Button>
            ))}
          {(archive.isPending || cancelTasks.isPending) && (
            <Loader2 className="size-3.5 animate-spin text-zinc-400" aria-hidden="true" />
          )}
        </div>
      </div>

      {confirming === 'archive' && (
        <p className="text-xs text-zinc-500">{t('agents.archiveHint')}</p>
      )}

      <Tabs
        value={search.tab ?? 'base'}
        onValueChange={(v) => patchSearch(String(v) as AgentTab)}
        className="shrink-0"
      >
        <TabsList
          variant="line"
          className="max-w-full [scrollbar-width:none] overflow-x-auto pb-px [&::-webkit-scrollbar]:hidden"
        >
          {(
            [
              ['base', 'agents.tabBase'],
              ['model', 'agents.tabModel'],
              ['skills', 'agents.tabSkills'],
              ['mcp', 'agents.tabMcp'],
              ['perm', 'agents.tabPerm'],
              ['tasks', 'agents.tabTasks'],
            ] as const
          ).map(([key, label]) => (
            <TabsTrigger key={key} value={key} className="px-3">
              {t(label)}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>

      <div className="pt-2">
        {(!search.tab || search.tab === 'base') && (
          <BaseInfoForm key={'base:' + a.id + ':' + agent.dataUpdatedAt} agent={a} />
        )}
        {search.tab === 'model' && (
          <ModelForm key={'model:' + a.id + ':' + agent.dataUpdatedAt} agent={a} />
        )}
        {search.tab === 'skills' && <AgentSkillsTab agentId={a.id} />}
        {search.tab === 'mcp' && <AgentMcpTab agentId={a.id} />}
        {search.tab === 'perm' && (
          <PermForm key={'perm:' + a.id + ':' + agent.dataUpdatedAt} agent={a} />
        )}
        {search.tab === 'tasks' && <AgentTasksTab agentId={a.id} />}
      </div>
    </div>
  )
}
const LABEL_CLASS = 'mb-1 block text-xs text-zinc-500'
const TEXTAREA_CLASS =
  'w-full resize-none rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm outline-none focus:border-zinc-400 dark:border-zinc-700 dark:bg-zinc-900'
function BaseInfoForm({ agent }: { agent: Agent }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [name, setName] = useState(agent.name)
  const [description, setDescription] = useState(agent.description)
  const [starters, setStarters] = useState<string[]>(
    (agent.conversation_starters ?? []).map((s) => s.label),
  )
  const save = useMutation({
    mutationFn: () =>
      updateAgent(agent.id, {
        name: name.trim(),
        description,
        runtime_config: agent.runtime_config,
        max_concurrent_tasks: agent.max_concurrent_tasks,
        visibility: agent.visibility,
        conversation_starters: starters
          .filter((s) => s.trim() !== '')
          .map((s) => ({ label: s.trim() })),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agent', agent.id] })
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
    },
  })
  const dirty =
    name !== agent.name ||
    description !== agent.description ||
    starters.filter((s) => s.trim()).join('\n') !==
      (agent.conversation_starters ?? []).map((s) => s.label).join('\n')
  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault()
        if (name.trim() && dirty) save.mutate()
      }}
    >
      <div>
        <label htmlFor="agent-name" className={LABEL_CLASS}>
          {t('agents.name')}
        </label>
        <Input
          id="agent-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          maxLength={100}
        />
      </div>

      <div>
        <label htmlFor="agent-desc" className={LABEL_CLASS}>
          {t('agents.descLabel')}
        </label>
        <textarea
          id="agent-desc"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={6}
          className={TEXTAREA_CLASS}
        />
      </div>

      <div>
        <span className={LABEL_CLASS}>{t('agents.startersLabel')}</span>
        <div className="space-y-2">
          {starters.map((s, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input
                aria-label={t('agents.starterN', { n: i + 1 })}
                value={s}
                maxLength={80}
                onChange={(e) =>
                  setStarters((prev) => prev.map((v, j) => (j === i ? e.target.value : v)))
                }
                placeholder={t('agents.starterPlaceholder')}
              />
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="shrink-0 text-red-600 dark:text-red-400"
                onClick={() => setStarters((prev) => prev.filter((_, j) => j !== i))}
              >
                {t('common.remove')}
              </Button>
            </div>
          ))}
          {starters.length < 3 && (
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setStarters((p) => [...p, ''])}
            >
              {t('agents.addStarter')}
            </Button>
          )}
        </div>
      </div>

      {save.error && (
        <p role="alert" className="text-xs text-red-600 dark:text-red-400">
          {save.error instanceof ApiError ? save.error.message : t('common.saveFailed')}
        </p>
      )}
      <div className="flex justify-end">
        <Button type="submit" disabled={!name.trim() || !dirty || save.isPending}>
          {save.isPending ? t('common.saving') : t('agents.saveBase')}
        </Button>
      </div>
    </form>
  )
}
function ModelForm({ agent }: { agent: Agent }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const rc = agent.runtime_config ?? {}
  const [provider, setProvider] = useState(String(rc.provider ?? ''))
  const [model, setModel] = useState(String(rc.model ?? ''))
  const [baseUrl, setBaseUrl] = useState(String(rc.base_url ?? ''))
  const [credentialId, setCredentialId] = useState(String(rc.credential_id ?? ''))
  const [maxConcurrent, setMaxConcurrent] = useState(agent.max_concurrent_tasks)
  const save = useMutation({
    mutationFn: () =>
      updateAgent(agent.id, {
        name: agent.name,
        description: agent.description,
        runtime_config: {
          ...rc,
          provider: provider.trim(),
          model: model.trim(),
          ...(baseUrl.trim() ? { base_url: baseUrl.trim() } : { base_url: '' }),
          ...(credentialId ? { credential_id: credentialId } : { credential_id: '' }),
        },
        max_concurrent_tasks: maxConcurrent,
        visibility: agent.visibility,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agent', agent.id] })
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
    },
  })
  const dirty =
    provider !== String(rc.provider ?? '') ||
    model !== String(rc.model ?? '') ||
    baseUrl !== String(rc.base_url ?? '') ||
    credentialId !== String(rc.credential_id ?? '') ||
    maxConcurrent !== agent.max_concurrent_tasks
  return (
    <div className="space-y-5">
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          if (provider.trim() && model.trim() && dirty) save.mutate()
        }}
      >
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label htmlFor="model-provider" className={LABEL_CLASS}>
              {t('agents.provider')}
            </label>
            <Input
              id="model-provider"
              value={provider}
              onChange={(e) => setProvider(e.target.value)}
              placeholder="openai"
            />
          </div>
          <div>
            <label htmlFor="model-model" className={LABEL_CLASS}>
              {t('agents.model')}
            </label>
            <Input
              id="model-model"
              value={model}
              onChange={(e) => setModel(e.target.value)}
              placeholder="gpt-4o-mini"
            />
          </div>
        </div>
        <div>
          <label htmlFor="model-baseurl" className={LABEL_CLASS}>
            {t('agents.baseUrlLabel')}
          </label>
          <Input
            id="model-baseurl"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="https://your-gateway.example/v1"
          />
        </div>
        <div>
          <span className={LABEL_CLASS}>{t('agents.credentialLabel')}</span>
          <CredentialField value={credentialId} onChange={setCredentialId} />
        </div>
        <div className="max-w-40">
          <label htmlFor="model-concurrent" className={LABEL_CLASS}>
            {t('agents.concurrentLabel')}
          </label>
          <Input
            id="model-concurrent"
            type="number"
            min={1}
            max={50}
            value={maxConcurrent}
            onChange={(e) => setMaxConcurrent(Number(e.target.value))}
          />
        </div>

        {rc.params && Object.keys(rc.params).length > 0 && (
          <div>
            <span className={LABEL_CLASS}>{t('agents.paramsLabel')}</span>
            <pre className="overflow-x-auto rounded-md bg-zinc-100 p-3 font-mono text-xs dark:bg-zinc-900">
              {JSON.stringify(rc.params, null, 2)}
            </pre>
          </div>
        )}

        {save.error && (
          <p role="alert" className="text-xs text-red-600 dark:text-red-400">
            {save.error instanceof ApiError ? save.error.message : t('common.saveFailed')}
          </p>
        )}
        <div className="flex justify-end">
          <Button
            type="submit"
            disabled={!provider.trim() || !model.trim() || !dirty || save.isPending}
          >
            {save.isPending ? t('common.saving') : t('agents.saveModel')}
          </Button>
        </div>
      </form>

      <EnvEditor agentId={agent.id} />
    </div>
  )
}
function EnvEditor({ agentId }: { agentId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const env = useQuery({ queryKey: ['agent-env', agentId], queryFn: () => getAgentEnv(agentId) })
  const save = useMutation({
    mutationFn: (next: Record<string, string>) => putAgentEnv(agentId, next),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agent-env', agentId] })
      void queryClient.invalidateQueries({ queryKey: ['agent', agentId] })
    },
  })
  if (env.isPending) return <p className="text-xs text-zinc-400">{t('agents.envLoading')}</p>
  if (env.isError) {
    return <p className="text-xs text-red-600 dark:text-red-400">{t('agents.envLoadFailed')}</p>
  }
  return (
    <EnvEditorForm
      key={'env:' + agentId + ':' + env.dataUpdatedAt}
      initial={env.data}
      save={save}
    />
  )
}
function EnvEditorForm({
  initial,
  save,
}: {
  initial: Record<string, string>
  save: ReturnType<typeof useMutation<unknown, Error, Record<string, string>>>
}) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<[string, string][]>(Object.entries(initial))
  const current = { ...initial }
  const next = Object.fromEntries(rows.filter(([k]) => k.trim() !== ''))
  const dirty = JSON.stringify(next) !== JSON.stringify(current)
  const submit = () => {
    if (dirty && !save.isPending) save.mutate(next)
  }
  return (
    <form
      className="space-y-2 rounded-lg border border-zinc-200 p-4 dark:border-zinc-800"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <p className="text-sm font-semibold">{t('agents.envTitle')}</p>
      <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('agents.envHint')}</p>
      {rows.map(([k, v], i) => (
        <div key={i} className="flex items-center gap-2">
          <Input
            aria-label={t('agents.envKeyN', { n: i + 1 })}
            value={k}
            onChange={(e) =>
              setRows((prev) => prev.map((r, j) => (j === i ? [e.target.value, r[1]] : r)))
            }
            placeholder="KEY"
            className="font-mono text-xs"
          />
          <Input
            aria-label={t('agents.envValueN', { n: i + 1 })}
            value={v}
            onChange={(e) =>
              setRows((prev) => prev.map((r, j) => (j === i ? [r[0], e.target.value] : r)))
            }
            placeholder="value"
            className="font-mono text-xs"
          />
          <Button
            type="button"
            variant="ghost"
            size="sm"
            className="shrink-0 text-red-600 dark:text-red-400"
            onClick={() => setRows((prev) => prev.filter((_, j) => j !== i))}
          >
            {t('common.remove')}
          </Button>
        </div>
      ))}
      <div className="flex items-center justify-between">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setRows((p) => [...p, ['', '']])}
        >
          {t('agents.addEnvVar')}
        </Button>
        <Button type="submit" size="sm" disabled={!dirty || save.isPending}>
          {save.isPending ? t('common.saving') : t('agents.saveEnv')}
        </Button>
      </div>
      {save.error && (
        <p role="alert" className="text-xs text-red-600 dark:text-red-400">
          {save.error instanceof ApiError ? save.error.message : t('common.saveFailed')}
        </p>
      )}
    </form>
  )
}
function AgentSkillsTab({ agentId }: { agentId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [pickerOpen, setPickerOpen] = useState(false)
  const [removingId, setRemovingId] = useState<string | null>(null)
  const bound = useQuery({
    queryKey: ['agent-skills', agentId],
    queryFn: () => listAgentSkills(agentId),
  })
  const invalidate = () =>
    void queryClient.invalidateQueries({ queryKey: ['agent-skills', agentId] })
  const toggle = useMutation({
    mutationFn: (input: { skillId: string; enabled: boolean }) =>
      setAgentSkillEnabled(agentId, input.skillId, input.enabled),
    onSuccess: invalidate,
  })
  const remove = useMutation({
    mutationFn: (skillId: string) => removeAgentSkill(agentId, skillId),
    onSuccess: () => {
      setRemovingId(null)
      invalidate()
    },
  })
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('agents.skillsHint')}</p>
        <Button size="sm" onClick={() => setPickerOpen(true)}>
          {t('agents.addSkill')}
        </Button>
      </div>

      {bound.isPending && <PageLoading label={t('agents.loadingBound')} />}

      {bound.isError && (
        <PageError
          title={t('agents.boundFailed')}
          message={bound.error instanceof Error ? bound.error.message : undefined}
          onRetry={() => void bound.refetch()}
        />
      )}

      {bound.isSuccess && bound.data.length === 0 && (
        <PageEmpty title={t('agents.noBoundTitle')} description={t('agents.noBoundDesc')} />
      )}

      {bound.data && bound.data.length > 0 && (
        <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
          {bound.data.map((sk) => (
            <li key={sk.skill_id} className="flex items-center gap-3 px-3 py-2.5">
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{sk.name}</p>
                {sk.description && (
                  <p className="truncate text-xs text-zinc-500 dark:text-zinc-400">
                    {sk.description}
                  </p>
                )}
              </div>
              <label className="flex shrink-0 cursor-pointer items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400">
                <input
                  type="checkbox"
                  checked={sk.enabled}
                  disabled={toggle.isPending}
                  onChange={(e) =>
                    toggle.mutate({ skillId: sk.skill_id, enabled: e.target.checked })
                  }
                  className="size-3.5 accent-zinc-700"
                />
                {t('agents.enable')}
              </label>
              {removingId === sk.skill_id ? (
                <span className="flex shrink-0 items-center gap-1">
                  <button
                    type="button"
                    className="text-xs text-red-600 underline-offset-2 hover:underline dark:text-red-400"
                    onClick={() => remove.mutate(sk.skill_id)}
                  >
                    {t('agents.confirmUnbind')}
                  </button>
                  <button
                    type="button"
                    className="text-xs text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-300"
                    onClick={() => setRemovingId(null)}
                  >
                    {t('common.cancel')}
                  </button>
                </span>
              ) : (
                <Button
                  size="sm"
                  variant="ghost"
                  className="shrink-0 text-red-600 dark:text-red-400"
                  onClick={() => setRemovingId(sk.skill_id)}
                >
                  {t('agents.unbind')}
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}

      {pickerOpen && (
        <SkillPickerDialog
          agentId={agentId}
          boundIds={new Set((bound.data ?? []).map((s) => s.skill_id))}
          onClose={() => setPickerOpen(false)}
          onAdded={invalidate}
        />
      )}
    </div>
  )
}
function SkillPickerDialog({
  agentId,
  boundIds,
  onClose,
  onAdded,
}: {
  agentId: string
  boundIds: Set<string>
  onClose: () => void
  onAdded: () => void
}) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const [debounced, setDebounced] = useState('')
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(query), 400)
    return () => clearTimeout(timer)
  }, [query])
  const results = useQuery({
    queryKey: ['skills', debounced],
    queryFn: () => listSkills(debounced),
  })
  const add = useMutation({
    mutationFn: (skillId: string) => addAgentSkill(agentId, skillId),
    onSuccess: () => {
      onAdded()
      onClose()
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : t('agents.bindFailed')),
  })
  return (
    <Dialog.Root open onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Popup className="fixed top-1/2 left-1/2 z-50 w-full max-w-sm -translate-x-1/2 -translate-y-1/2 rounded-lg border border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
          <Dialog.Title className="mb-3 text-sm font-semibold">
            {t('agents.bindSkill')}
          </Dialog.Title>
          <Input
            autoFocus
            aria-label={t('agents.searchSkill')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t('agents.searchSkillPlaceholder')}
          />
          <ul className="mt-3 max-h-64 divide-y divide-zinc-200 overflow-y-auto rounded-md border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
            {(results.data ?? []).map((s) => {
              const isBound = boundIds.has(s.id)
              return (
                <li key={s.id}>
                  <button
                    type="button"
                    disabled={isBound || add.isPending}
                    className="flex w-full cursor-default items-center gap-2 px-3 py-2 text-left text-sm outline-none hover:bg-zinc-50 disabled:opacity-40 dark:hover:bg-zinc-900/50"
                    onClick={() => add.mutate(s.id)}
                  >
                    <span className="min-w-0 flex-1 truncate">
                      {s.name}
                      {s.description && (
                        <span className="ml-2 truncate text-xs text-zinc-400">{s.description}</span>
                      )}
                    </span>
                    <span className="shrink-0 text-xs text-zinc-400">
                      {isBound ? t('agents.bound') : t('agents.bind')}
                    </span>
                  </button>
                </li>
              )
            })}
            {results.data && results.data.length === 0 && (
              <li className="px-3 py-3 text-center text-xs text-zinc-400">
                {t('agents.noSkillMatch')}
              </li>
            )}
          </ul>
          {error && (
            <p role="alert" className="mt-2 text-xs text-red-600 dark:text-red-400">
              {error}
            </p>
          )}
          <div className="mt-3 flex justify-end">
            <Dialog.Close className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-800">
              {t('common.close')}
            </Dialog.Close>
          </div>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
function AgentMcpTab({ agentId }: { agentId: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<AgentMCPServer | 'new' | null>(null)
  const [removingId, setRemovingId] = useState<string | null>(null)
  const own = useQuery({ queryKey: ['agent-mcp', agentId], queryFn: () => listAgentMcp(agentId) })
  const resolved = useQuery({
    queryKey: ['agent-mcp-resolved', agentId],
    queryFn: () => resolvedAgentMcp(agentId),
  })
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['agent-mcp', agentId] })
    void queryClient.invalidateQueries({ queryKey: ['agent-mcp-resolved', agentId] })
  }
  const toggle = useMutation({
    mutationFn: (s: AgentMCPServer) =>
      updateAgentMcp(agentId, s.id, { name: s.name, url: s.url, enabled: !s.enabled }),
    onSuccess: invalidate,
  })
  const remove = useMutation({
    mutationFn: (serverId: string) => deleteAgentMcp(agentId, serverId),
    onSuccess: () => {
      setRemovingId(null)
      invalidate()
    },
  })
  return (
    <div className="space-y-5">
      <section className="space-y-3">
        <div className="flex items-center justify-between">
          <div>
            <p className="text-sm font-semibold">{t('agents.ownServers')}</p>
            <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('agents.ownServersHint')}</p>
          </div>
          <Button size="sm" onClick={() => setEditing('new')}>
            <Plus className="size-3.5" aria-hidden="true" />
            {t('agents.addServer')}
          </Button>
        </div>

        {own.isPending && <p className="text-xs text-zinc-400">{t('agents.loadingServers')}</p>}
        {own.isError && (
          <PageError
            title={t('agents.serversFailed')}
            message={own.error instanceof Error ? own.error.message : undefined}
            onRetry={() => void own.refetch()}
          />
        )}
        {own.data && own.data.length === 0 && (
          <p className="text-xs text-zinc-400">{t('agents.noOwnServers')}</p>
        )}

        {own.data && own.data.length > 0 && (
          <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
            {own.data.map((s) => (
              <li key={s.id} className="flex items-center gap-3 px-3 py-2.5">
                <div className="min-w-0 flex-1">
                  <p className="flex items-center gap-2 text-sm font-medium">
                    {s.name}
                    {s.has_credentials && <Badge variant="neutral">{t('agents.storedCred')}</Badge>}
                  </p>
                  <p className="truncate font-mono text-xs text-zinc-400">{s.url}</p>
                </div>
                <label className="flex shrink-0 cursor-pointer items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400">
                  <input
                    type="checkbox"
                    checked={s.enabled}
                    disabled={toggle.isPending}
                    onChange={() => toggle.mutate(s)}
                    className="size-3.5 accent-zinc-700"
                  />
                  {t('agents.enable')}
                </label>
                <Button size="sm" variant="ghost" onClick={() => setEditing(s)}>
                  {t('common.edit')}
                </Button>
                {removingId === s.id ? (
                  <span className="flex shrink-0 items-center gap-1">
                    <button
                      type="button"
                      className="text-xs text-red-600 underline-offset-2 hover:underline dark:text-red-400"
                      onClick={() => remove.mutate(s.id)}
                    >
                      {t('agents.confirmDelete')}
                    </button>
                    <button
                      type="button"
                      className="text-xs text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-300"
                      onClick={() => setRemovingId(null)}
                    >
                      {t('common.cancel')}
                    </button>
                  </span>
                ) : (
                  <Button
                    size="sm"
                    variant="ghost"
                    className="shrink-0 text-red-600 dark:text-red-400"
                    onClick={() => setRemovingId(s.id)}
                  >
                    <Trash2 className="size-3.5" aria-hidden="true" />
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="space-y-2">
        <p className="text-sm font-semibold">{t('agents.resolvedTitle')}</p>
        <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('agents.resolvedHint')}</p>
        {resolved.isPending && <p className="text-xs text-zinc-400">{t('agents.resolving')}</p>}
        {resolved.data && resolved.data.length === 0 && (
          <p className="text-xs text-zinc-400">{t('agents.noResolved')}</p>
        )}
        {resolved.data && resolved.data.length > 0 && (
          <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
            {resolved.data.map((s) => (
              <li key={s.name} className="flex items-center gap-3 px-3 py-2 text-sm">
                <span className="shrink-0 font-medium">{s.name}</span>
                <span className="min-w-0 flex-1 truncate font-mono text-xs text-zinc-400">
                  {s.url}
                </span>
                <Badge variant={s.enabled ? 'success' : 'neutral'}>
                  {s.enabled ? t('agents.active') : t('agents.inactive')}
                </Badge>
              </li>
            ))}
          </ul>
        )}
      </section>

      {editing && (
        <McpServerDialog
          agentId={agentId}
          server={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={invalidate}
        />
      )}
    </div>
  )
}
function McpServerDialog({
  agentId,
  server,
  onClose,
  onSaved,
}: {
  agentId: string
  server: AgentMCPServer | null
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState(server?.name ?? '')
  const [url, setUrl] = useState(server?.url ?? '')
  const [enabled, setEnabled] = useState(server?.enabled ?? true)
  const [apiKey, setApiKey] = useState('')
  const save = useMutation({
    mutationFn: () => {
      const credentials = apiKey.trim() ? { api_key: apiKey.trim() } : undefined
      return server
        ? updateAgentMcp(agentId, server.id, {
            name: name.trim(),
            url: url.trim(),
            enabled,
            credentials,
          })
        : upsertAgentMcp(agentId, { name: name.trim(), url: url.trim(), enabled, credentials })
    },
    onSuccess: () => {
      onSaved()
      onClose()
    },
  })
  return (
    <Dialog.Root open onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Popup className="fixed top-1/2 left-1/2 z-50 w-full max-w-sm -translate-x-1/2 -translate-y-1/2 rounded-lg border border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
          <Dialog.Title className="mb-4 text-sm font-semibold">
            {server ? t('agents.editServer') : t('agents.addServer')}
          </Dialog.Title>
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault()
              if (name.trim() && url.trim() && !save.isPending) save.mutate()
            }}
          >
            <div>
              <label htmlFor="mcp-name" className={LABEL_CLASS}>
                {t('agents.serverNameLabel')}
              </label>
              <Input
                id="mcp-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoFocus
                placeholder="web-search"
              />
            </div>
            <div>
              <label htmlFor="mcp-url" className={LABEL_CLASS}>
                {t('agents.serverUrlLabel')}
              </label>
              <Input
                id="mcp-url"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://mcp.example.com/sse"
              />
            </div>
            <div>
              <label htmlFor="mcp-key" className={LABEL_CLASS}>
                {server ? t('agents.apiKeyKeepLabel') : t('agents.apiKeyLabel')}
              </label>
              <Input
                id="mcp-key"
                type="password"
                value={apiKey}
                onChange={(e) => setApiKey(e.target.value)}
                placeholder="sk-…"
              />
            </div>
            <label className="flex cursor-pointer items-center gap-1.5 text-sm">
              <input
                type="checkbox"
                checked={enabled}
                onChange={(e) => setEnabled(e.target.checked)}
                className="size-3.5 accent-zinc-700"
              />
              {t('agents.enable')}
            </label>
            {save.error && (
              <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                {save.error instanceof ApiError ? save.error.message : t('common.saveFailed')}
              </p>
            )}
            <div className="flex justify-end gap-2 pt-1">
              <Dialog.Close className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-800">
                {t('common.cancel')}
              </Dialog.Close>
              <button
                type="submit"
                disabled={!name.trim() || !url.trim() || save.isPending}
                className="rounded-md bg-zinc-900 px-3 py-1.5 text-sm text-white disabled:opacity-50 dark:bg-zinc-100 dark:text-zinc-900"
              >
                {save.isPending ? t('common.saving') : t('common.save')}
              </button>
            </div>
          </form>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
const VISIBILITY_OPTIONS = [
  { key: 'public', title: 'agents.visPublic', desc: 'agents.visPublicDesc' },
  { key: 'public_to', title: 'agents.visLimited', desc: 'agents.visLimitedDesc' },
  { key: 'private', title: 'agents.visPrivate', desc: 'agents.visPrivateDesc' },
] as const
function PermForm({ agent }: { agent: Agent }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const workspaceId = useWorkspaceStore((s) => s.currentId) ?? ''
  const members = useQuery(membersQueryOptions(workspaceId))
  const [visibility, setVisibility] = useState(agent.visibility || 'public')
  const [targets, setTargets] = useState<InvocationTarget[]>(agent.invocation_targets ?? [])
  const memberName = (userId: string) => {
    const m = members.data?.find((x) => x.user_id === userId)
    return m ? m.display_name || m.email : userId.slice(0, 8) + '…'
  }
  const dirty =
    visibility !== (agent.visibility || 'public') ||
    JSON.stringify(targets) !== JSON.stringify(agent.invocation_targets ?? [])
  const save = useMutation({
    mutationFn: () =>
      updateAgent(agent.id, {
        name: agent.name,
        description: agent.description,
        runtime_config: agent.runtime_config,
        max_concurrent_tasks: agent.max_concurrent_tasks,
        visibility,
        invocation_targets: targets,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['agent', agent.id] })
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
    },
  })
  const hasWorkspaceRow = targets.some((row) => row.type === 'workspace')
  const memberIds = new Set(
    targets.filter((row) => row.type === 'member').map((row) => row.user_id),
  )
  const addableMembers = (members.data ?? []).filter((m) => !memberIds.has(m.user_id))
  const editorVisible = visibility === 'public_to' || visibility === 'private'
  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <p className="text-sm font-semibold">{t('agents.visibility')}</p>
        <div className="space-y-1.5">
          {VISIBILITY_OPTIONS.map((opt) => (
            <label
              key={opt.key}
              className={
                'flex cursor-pointer items-start gap-2.5 rounded-lg border px-3 py-2.5 outline-none ' +
                (visibility === opt.key
                  ? 'border-zinc-400 bg-zinc-50 dark:border-zinc-600 dark:bg-zinc-800/60'
                  : 'border-zinc-200 hover:bg-zinc-50 dark:border-zinc-800 dark:hover:bg-zinc-900/50')
              }
            >
              <input
                type="radio"
                name="agent-visibility"
                checked={visibility === opt.key}
                onChange={() => setVisibility(opt.key)}
                className="mt-0.5 size-3.5 accent-zinc-700"
              />
              <span>
                <span className="block text-sm font-medium">{t(opt.title)}</span>
                <span className="block text-xs text-zinc-500 dark:text-zinc-400">
                  {t(opt.desc)}
                </span>
              </span>
            </label>
          ))}
        </div>
      </div>

      {editorVisible ? (
        <div className="space-y-2">
          <p className="text-sm font-semibold">{t('agents.allowlist')}</p>
          {visibility === 'public_to' && targets.length === 0 && (
            <p className="text-xs text-amber-600 dark:text-amber-400">
              {t('agents.emptyRestricted')}
            </p>
          )}
          <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
            {targets.map((row, i) => (
              <li key={i} className="flex items-center gap-3 px-3 py-2 text-sm">
                <Badge variant={row.type === 'workspace' ? 'success' : 'neutral'}>
                  {row.type === 'workspace' ? t('agents.allMembers') : t('agents.member')}
                </Badge>
                <span className="min-w-0 flex-1 truncate">
                  {row.type === 'workspace'
                    ? t('agents.workspaceScope')
                    : memberName(row.user_id ?? '')}
                </span>
                <Button
                  size="sm"
                  variant="ghost"
                  className="text-red-600 dark:text-red-400"
                  onClick={() => setTargets((prev) => prev.filter((_, j) => j !== i))}
                >
                  {t('common.remove')}
                </Button>
              </li>
            ))}
            {targets.length === 0 && (
              <li className="px-3 py-2.5 text-xs text-zinc-400">{t('agents.emptyAllowlist')}</li>
            )}
          </ul>
          <div className="flex flex-wrap items-center gap-2">
            {!hasWorkspaceRow && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => setTargets((prev) => [...prev, { type: 'workspace' }])}
              >
                <Plus className="size-3.5" aria-hidden="true" />
                {t('agents.allMembers')}
              </Button>
            )}
            <select
              aria-label={t('agents.addMemberToAllowlist')}
              value=""
              onChange={(e) => {
                const uid = e.target.value
                if (uid) setTargets((prev) => [...prev, { type: 'member', user_id: uid }])
              }}
              className="h-8 rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none dark:border-zinc-700 dark:bg-zinc-900"
            >
              <option value="">{t('agents.addMemberOption')}</option>
              {addableMembers.map((m) => (
                <option key={m.user_id} value={m.user_id}>
                  {m.display_name || m.email}
                </option>
              ))}
            </select>
          </div>
        </div>
      ) : (
        <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('agents.publicHint')}</p>
      )}

      {save.error && (
        <p role="alert" className="text-xs text-red-600 dark:text-red-400">
          {save.error instanceof ApiError ? save.error.message : t('common.saveFailed')}
        </p>
      )}
      <div className="flex justify-end">
        <Button disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? t('common.saving') : t('agents.savePerm')}
        </Button>
      </div>
    </div>
  )
}
const TRIGGER_KEYS: Record<string, string> = {
  assignment: 'agents.triggerAssignment',
  member_comment: 'agents.triggerMemberComment',
  chat: 'agents.triggerChat',
  squad: 'agents.triggerSquad',
  leader_dispatch: 'agents.triggerLeaderDispatch',
  rule: 'agents.triggerRule',
  rerun: 'agents.triggerRerun',
}
function AgentTasksTab({ agentId }: { agentId: string }) {
  const { t, i18n } = useTranslation()
  const tasks = useQuery({
    queryKey: ['agent-tasks', agentId],
    queryFn: () => listAgentTasks(agentId),
  })
  if (tasks.isPending) return <PageLoading label={t('agents.loadingTasks')} />
  if (tasks.isError) {
    return <PageError title={t('agents.tasksFailed')} message={String(tasks.error.message)} />
  }
  if (tasks.data.length === 0) {
    return <PageEmpty title={t('agents.noTasksTitle')} description={t('agents.noTasksDesc')} />
  }
  const formatTime = (iso: string) =>
    new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  return (
    <div className="space-y-2">
      <div className="flex justify-end">
        <Button variant="outline" size="sm" onClick={() => void tasks.refetch()}>
          {t('agents.refresh')}
        </Button>
      </div>
      <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
        {tasks.data.map((task) => (
          <li key={task.id} className="space-y-1 px-3 py-2.5">
            <div className="flex items-center gap-3 text-sm">
              <TaskStatusBadge status={task.status} />
              {task.trigger_evidence_kind && (
                <span className="text-xs text-zinc-500">
                  {TRIGGER_KEYS[task.trigger_evidence_kind]
                    ? t(TRIGGER_KEYS[task.trigger_evidence_kind])
                    : task.trigger_evidence_kind}
                </span>
              )}
              {task.attempt > 1 && (
                <span className="text-xs text-amber-600 dark:text-amber-400">
                  {t('agents.retryAttempt', { attempt: task.attempt, max: task.max_attempts })}
                </span>
              )}
              <span className="ml-auto shrink-0 text-xs text-zinc-400">
                {formatTime(task.created_at)}
              </span>
            </div>
            {task.failure_reason && (
              <p className="font-mono text-xs text-red-600 dark:text-red-400">
                {task.failure_reason}
              </p>
            )}
            {task.work_item_id ? (
              <Link
                to="/work-items/$id"
                params={{ id: task.work_item_id }}
                className="inline-block py-0.5 text-xs text-zinc-500 underline-offset-2 outline-none hover:text-zinc-700 hover:underline dark:text-zinc-400 dark:hover:text-zinc-200"
              >
                {t('agents.viewWorkItem')}
              </Link>
            ) : (
              <p className="text-xs text-zinc-400">{t('agents.directTask')}</p>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}

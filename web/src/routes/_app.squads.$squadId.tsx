import { Dialog } from '@base-ui/react/dialog'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { ArrowLeft, Loader2, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { listAgents } from '@/api/orchestration'
import {
  addSquadMember,
  deleteSquad,
  getSquad,
  listSquadMembers,
  removeSquadMember,
  squadMembersStatus,
  updateSquad,
  updateSquadMemberRole,
  type Squad,
} from '@/api/squads'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { PageError, PageLoading } from '@/components/ui/states'
import { ApiError } from '@/lib/api'
import { cn } from '@/lib/utils'
export const Route = createFileRoute('/_app/squads/$squadId')({
  component: SquadDetailPage,
})
const LABEL_CLASS = 'mb-1 block text-xs text-zinc-500'
const SELECT_CLASS =
  'h-9 w-full rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900'
const STATUS_KEYS: Record<string, { label: string; cls: string }> = {
  running: { label: 'squads.statusRunning', cls: 'bg-emerald-500' },
  queued: { label: 'squads.statusQueued', cls: 'bg-amber-400' },
  idle: { label: 'squads.statusIdle', cls: 'bg-zinc-300 dark:bg-zinc-600' },
}
function SquadDetailPage() {
  const { t } = useTranslation()
  const { squadId } = Route.useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const squad = useQuery({ queryKey: ['squad', squadId], queryFn: () => getSquad(squadId) })
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['squads'] })
    void queryClient.invalidateQueries({ queryKey: ['squad', squadId] })
    void queryClient.invalidateQueries({ queryKey: ['squad-members', squadId] })
  }
  const [confirmingDelete, setConfirmingDelete] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const removeSquad = useMutation({
    mutationFn: () => deleteSquad(squadId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['squads'] })
      void navigate({ to: '/squads' })
    },
    onError: (e) => setDeleteError(e instanceof ApiError ? e.message : t('squads.deleteFailed')),
  })
  if (squad.isPending) return <PageLoading label={t('squads.loading')} />
  if (squad.isError) {
    return (
      <PageError
        title={t('squads.notFound')}
        message={squad.error instanceof Error ? squad.error.message : undefined}
      />
    )
  }
  const sq = squad.data
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        <Link
          to="/squads"
          className="flex size-7 items-center justify-center rounded-md text-zinc-400 outline-none hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
          aria-label={t('squads.backToList')}
        >
          <ArrowLeft className="size-4" aria-hidden="true" />
        </Link>
        <h1 className="min-w-0 truncate text-lg font-semibold">{sq.name}</h1>
        <Badge variant={sq.choreography ? 'warning' : 'neutral'}>
          {sq.choreography ? t('squads.modeB') : t('squads.modeA')}
        </Badge>
        <div className="ml-auto flex items-center gap-2">
          {confirmingDelete ? (
            <>
              <Button
                size="sm"
                variant="danger"
                disabled={removeSquad.isPending}
                onClick={() => removeSquad.mutate()}
              >
                {t('squads.confirmDelete')}
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  setConfirmingDelete(false)
                  setDeleteError(null)
                }}
              >
                {t('common.cancel')}
              </Button>
            </>
          ) : (
            <Button
              size="sm"
              variant="ghost"
              className="text-red-600 dark:text-red-400"
              onClick={() => setConfirmingDelete(true)}
            >
              <Trash2 className="size-3.5" aria-hidden="true" />
              {t('squads.deleteSquad')}
            </Button>
          )}
          {removeSquad.isPending && (
            <Loader2 className="size-3.5 animate-spin text-zinc-400" aria-hidden="true" />
          )}
        </div>
      </div>
      {confirmingDelete && <p className="text-xs text-zinc-500">{t('squads.deleteHint')}</p>}
      {deleteError && <p className="text-xs text-red-600 dark:text-red-400">{deleteError}</p>}

      <SquadBasicForm
        key={'base:' + sq.id + ':' + squad.dataUpdatedAt}
        squad={sq}
        onSaved={invalidate}
      />
      <SquadMembersSection squadId={sq.id} onChanged={invalidate} />
    </div>
  )
}
function SquadBasicForm({ squad, onSaved }: { squad: Squad; onSaved: () => void }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [name, setName] = useState(squad.name)
  const [instructions, setInstructions] = useState(squad.instructions)
  const [choreography, setChoreography] = useState(squad.choreography)
  const [leaderId, setLeaderId] = useState(squad.leader_agent_id || '')
  const agents = useQuery({ queryKey: ['agents'], queryFn: listAgents, staleTime: 60000 })
  const activeAgents = (agents.data ?? []).filter((a) => !a.archived)
  const save = useMutation({
    mutationFn: () =>
      updateSquad(squad.id, {
        name: name.trim(),
        instructions,
        ...(leaderId ? { leader_agent_id: leaderId } : {}),
        choreography,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['squad', squad.id] })
      onSaved()
    },
  })
  const dirty =
    name !== squad.name ||
    instructions !== squad.instructions ||
    choreography !== squad.choreography ||
    leaderId !== (squad.leader_agent_id || '')
  return (
    <form
      className="space-y-4 rounded-lg border border-zinc-200 p-4 dark:border-zinc-800"
      onSubmit={(e) => {
        e.preventDefault()
        if (name.trim() && dirty) save.mutate()
      }}
    >
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-48 flex-1">
          <label htmlFor="squad-name" className={LABEL_CLASS}>
            {t('agents.name')}
          </label>
          <Input
            id="squad-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={100}
          />
        </div>
        <div className="min-w-48 flex-1">
          <label htmlFor="squad-leader" className={LABEL_CLASS}>
            {t('squads.leaderLabel')}
          </label>
          <select
            id="squad-leader"
            value={leaderId}
            onChange={(e) => setLeaderId(e.target.value)}
            className={SELECT_CLASS}
          >
            <option value="">{t('squads.leaderUnset')}</option>
            {activeAgents.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div>
        <label htmlFor="squad-instructions" className={LABEL_CLASS}>
          {t('squads.instructionsLabel')}
        </label>
        <textarea
          id="squad-instructions"
          value={instructions}
          onChange={(e) => setInstructions(e.target.value)}
          rows={3}
          className="w-full resize-none rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm outline-none focus:border-zinc-400 dark:border-zinc-700 dark:bg-zinc-900"
        />
      </div>

      <div>
        <span className={LABEL_CLASS}>{t('squads.modeLabel')}</span>
        <div className="grid gap-2 sm:grid-cols-2">
          <label
            className={cn(
              'flex cursor-pointer items-start gap-2.5 rounded-lg border px-3 py-2.5 outline-none',
              !choreography
                ? 'border-zinc-400 bg-zinc-50 dark:border-zinc-600 dark:bg-zinc-800/60'
                : 'border-zinc-200 hover:bg-zinc-50 dark:border-zinc-800 dark:hover:bg-zinc-900/50',
            )}
          >
            <input
              type="radio"
              name="squad-mode"
              checked={!choreography}
              onChange={() => setChoreography(false)}
              className="mt-0.5 size-3.5 accent-zinc-700"
            />
            <span>
              <span className="block text-sm font-medium">{t('squads.modeATitle')}</span>
              <span className="block text-xs text-zinc-500 dark:text-zinc-400">
                {t('squads.modeADesc')}
              </span>
            </span>
          </label>
          <label
            className={cn(
              'flex cursor-pointer items-start gap-2.5 rounded-lg border px-3 py-2.5 outline-none',
              choreography
                ? 'border-zinc-400 bg-zinc-50 dark:border-zinc-600 dark:bg-zinc-800/60'
                : 'border-zinc-200 hover:bg-zinc-50 dark:border-zinc-800 dark:hover:bg-zinc-900/50',
            )}
          >
            <input
              type="radio"
              name="squad-mode"
              checked={choreography}
              onChange={() => setChoreography(true)}
              className="mt-0.5 size-3.5 accent-zinc-700"
            />
            <span>
              <span className="block text-sm font-medium">{t('squads.modeBTitle')}</span>
              <span className="block text-xs text-zinc-500 dark:text-zinc-400">
                {t('squads.modeBDesc')}
              </span>
            </span>
          </label>
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
function SquadMembersSection({ squadId, onChanged }: { squadId: string; onChanged: () => void }) {
  const { t } = useTranslation()
  const [addOpen, setAddOpen] = useState(false)
  const [removingId, setRemovingId] = useState<string | null>(null)
  const [memberError, setMemberError] = useState<string | null>(null)
  const members = useQuery({
    queryKey: ['squad-members', squadId],
    queryFn: () => listSquadMembers(squadId),
  })
  const status = useQuery({
    queryKey: ['squad-members-status', squadId],
    queryFn: () => squadMembersStatus(squadId),
    refetchInterval: 10000,
  })
  const statusByAgent = new Map((status.data ?? []).map((s) => [s.agent_id, s]))
  const remove = useMutation({
    mutationFn: (agentId: string) => removeSquadMember(squadId, agentId),
    onSuccess: () => {
      setRemovingId(null)
      setMemberError(null)
      onChanged()
    },
    onError: (e) => setMemberError(e instanceof ApiError ? e.message : t('squads.removeFailed')),
  })
  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-sm font-semibold">
          {t('squads.membersTitle', { n: members.data?.length ?? 0 })}
        </p>
        <Button size="sm" onClick={() => setAddOpen(true)}>
          <Plus className="size-3.5" aria-hidden="true" />
          {t('squads.addMember')}
        </Button>
      </div>

      {members.isPending && <p className="text-xs text-zinc-400">{t('squads.loadingMembers')}</p>}
      {members.isError && (
        <PageError
          title={t('squads.membersFailed')}
          message={members.error instanceof Error ? members.error.message : undefined}
          onRetry={() => void members.refetch()}
        />
      )}

      {members.data && members.data.length === 0 && (
        <p className="text-xs text-zinc-400">{t('squads.noMembers')}</p>
      )}

      {members.data && members.data.length > 0 && (
        <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
          {members.data.map((m) => {
            const st = statusByAgent.get(m.agent_id)
            const dot = STATUS_KEYS[st?.status ?? 'idle'] ?? STATUS_KEYS.idle!
            return (
              <li key={m.agent_id} className="flex items-center gap-3 px-3 py-2.5">
                <span
                  className={'size-2 shrink-0 rounded-full ' + dot.cls}
                  title={t(dot.label)}
                  aria-label={t(dot.label)}
                />
                <div className="min-w-0 flex-1">
                  <p className={'truncate text-sm font-medium ' + (m.archived ? 'opacity-55' : '')}>
                    {m.name}
                    {m.archived && (
                      <span className="ml-1.5 text-xs text-zinc-400">
                        {t('squads.memberArchived')}
                      </span>
                    )}
                  </p>
                  {st && (st.active_tasks?.length ?? 0) > 0 && (
                    <p className="text-xs text-zinc-400">
                      {t('squads.activeTasksN', { n: st.active_tasks?.length ?? 0 })}
                    </p>
                  )}
                </div>
                <RoleEditor
                  squadId={squadId}
                  agentId={m.agent_id}
                  role={m.role}
                  onChanged={onChanged}
                />
                {removingId === m.agent_id ? (
                  <span className="flex shrink-0 items-center gap-1">
                    <button
                      type="button"
                      className="text-xs text-red-600 underline-offset-2 hover:underline dark:text-red-400"
                      onClick={() => remove.mutate(m.agent_id)}
                    >
                      {t('squads.confirmRemove')}
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
                    onClick={() => setRemovingId(m.agent_id)}
                  >
                    {t('common.remove')}
                  </Button>
                )}
              </li>
            )
          })}
        </ul>
      )}
      {memberError && <p className="text-xs text-red-600 dark:text-red-400">{memberError}</p>}

      {addOpen && (
        <AddMemberDialog
          squadId={squadId}
          existingIds={new Set((members.data ?? []).map((m) => m.agent_id))}
          onClose={() => setAddOpen(false)}
          onAdded={onChanged}
        />
      )}
    </section>
  )
}
function RoleEditor({
  squadId,
  agentId,
  role,
  onChanged,
}: {
  squadId: string
  agentId: string
  role: string
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState(role)
  const save = useMutation({
    mutationFn: () => updateSquadMemberRole(squadId, agentId, value.trim() || 'member'),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['squad-members', squadId] })
      setEditing(false)
      onChanged()
    },
  })
  if (!editing) {
    return (
      <button
        type="button"
        title={t('squads.editRoleHint')}
        className="shrink-0 cursor-default rounded-full bg-zinc-100 px-2 py-0.5 text-xs text-zinc-600 outline-none hover:bg-zinc-200 dark:bg-zinc-800 dark:text-zinc-300 dark:hover:bg-zinc-700"
        onClick={() => setEditing(true)}
      >
        {role}
      </button>
    )
  }
  return (
    <span className="flex shrink-0 items-center gap-1">
      <Input
        aria-label={t('squads.role')}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        maxLength={80}
        autoFocus
        className="h-7 w-24 text-xs"
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault()
            save.mutate()
          }
          if (e.key === 'Escape') {
            setValue(role)
            setEditing(false)
          }
        }}
      />
      <button
        type="button"
        disabled={save.isPending}
        className="text-xs text-zinc-500 underline-offset-2 hover:text-zinc-700 hover:underline dark:text-zinc-400 dark:hover:text-zinc-200"
        onClick={() => save.mutate()}
      >
        {t('common.save')}
      </button>
      <button
        type="button"
        className="text-xs text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-300"
        onClick={() => {
          setValue(role)
          setEditing(false)
        }}
      >
        {t('common.cancel')}
      </button>
    </span>
  )
}
function AddMemberDialog({
  squadId,
  existingIds,
  onClose,
  onAdded,
}: {
  squadId: string
  existingIds: Set<string>
  onClose: () => void
  onAdded: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [agentId, setAgentId] = useState('')
  const [role, setRole] = useState('')
  const [error, setError] = useState<string | null>(null)
  const agents = useQuery({ queryKey: ['agents'], queryFn: listAgents, staleTime: 60000 })
  const candidates = (agents.data ?? []).filter((a) => !a.archived && !existingIds.has(a.id))
  const add = useMutation({
    mutationFn: () => addSquadMember(squadId, agentId, role),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['squad-members', squadId] })
      onAdded()
      onClose()
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : t('squads.addFailed')),
  })
  return (
    <Dialog.Root open onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Popup className="fixed top-1/2 left-1/2 z-50 w-full max-w-sm -translate-x-1/2 -translate-y-1/2 rounded-lg border border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
          <Dialog.Title className="mb-4 text-sm font-semibold">
            {t('squads.addMember')}
          </Dialog.Title>
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault()
              if (agentId && !add.isPending) add.mutate()
            }}
          >
            <div>
              <label htmlFor="member-agent" className={LABEL_CLASS}>
                {t('squads.agentLabel')}
              </label>
              <select
                id="member-agent"
                value={agentId}
                onChange={(e) => setAgentId(e.target.value)}
                className={SELECT_CLASS}
                autoFocus
              >
                <option value="">{t('squads.selectAgent')}</option>
                {candidates.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
              </select>
              {candidates.length === 0 && (
                <p className="mt-1 text-xs text-zinc-400">{t('squads.noCandidates')}</p>
              )}
            </div>
            <div>
              <label htmlFor="member-role" className={LABEL_CLASS}>
                {t('squads.roleLabel')}
              </label>
              <Input
                id="member-role"
                value={role}
                onChange={(e) => setRole(e.target.value)}
                maxLength={80}
                placeholder="member"
              />
            </div>
            {error && (
              <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                {error}
              </p>
            )}
            <div className="flex justify-end gap-2 pt-1">
              <Dialog.Close className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-800">
                {t('common.cancel')}
              </Dialog.Close>
              <button
                type="submit"
                disabled={!agentId || add.isPending}
                className="rounded-md bg-zinc-900 px-3 py-1.5 text-sm text-white disabled:opacity-50 dark:bg-zinc-100 dark:text-zinc-900"
              >
                {add.isPending ? t('squads.adding') : t('squads.add')}
              </button>
            </div>
          </form>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

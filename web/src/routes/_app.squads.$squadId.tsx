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
import {
  FormField,
  Skeleton,
  StatusBadge,
  toast,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
  useConfirm,
} from '@ploykit/ui'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { Select } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { ApiError } from '@ploykit/client'
export const Route = createFileRoute('/_app/squads/$squadId')({
  component: SquadDetailPage,
})
// 成员运行状态单轨呈现：色点+原生 title → kit StatusBadge（文字即状态，无需 hint）。
const STATUS_TONES: Record<string, { labelKey: string; tone: 'positive' | 'warning' | 'neutral' }> =
  {
    running: { labelKey: 'squads.statusRunning', tone: 'positive' },
    queued: { labelKey: 'squads.statusQueued', tone: 'warning' },
    idle: { labelKey: 'squads.statusIdle', tone: 'neutral' },
  }
// 组件具名导出（路由内仅注册）：测试用路由桩直接渲染，绕开 router 装配。
export function SquadDetailPage() {
  const { t } = useTranslation()
  const { squadId } = Route.useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const squad = useQuery({ queryKey: ['squad', squadId], queryFn: () => getSquad(squadId) })
  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ['squads'] })
    void queryClient.invalidateQueries({ queryKey: ['squad', squadId] })
    void queryClient.invalidateQueries({ queryKey: ['squad-members', squadId] })
  }
  const removeSquad = useMutation({
    mutationFn: () => deleteSquad(squadId),
    onSuccess: () => {
      toast.success(t('squads.deleteSuccess'))
      void queryClient.invalidateQueries({ queryKey: ['squads'] })
      void navigate({ to: '/squads' })
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : t('squads.deleteFailed')),
  })
  // 删除两段式 → useConfirm 统一形态：后果说明（deleteHint）只在弹窗 description 里说一次。
  const askDeleteSquad = async () => {
    const ok = await confirm({
      title: t('squads.deleteSquad'),
      description: t('squads.deleteHint'),
      confirmText: t('squads.confirmDelete'),
      cancelText: t('common.cancel'),
      danger: true,
    })
    if (ok) removeSquad.mutate()
  }
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
        <div className="ml-auto flex items-center gap-2">
          <Button
            size="sm"
            variant="ghost"
            className="text-red-600 dark:text-red-400"
            disabled={removeSquad.isPending}
            onClick={() => void askDeleteSquad()}
          >
            <Trash2 className="size-3.5" aria-hidden="true" />
            {t('squads.deleteSquad')}
          </Button>
          {removeSquad.isPending && (
            <Loader2 className="size-3.5 animate-spin text-zinc-400" aria-hidden="true" />
          )}
        </div>
      </div>

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
  const [leaderId, setLeaderId] = useState(squad.leader_agent_id || '')
  const agents = useQuery({ queryKey: ['agents'], queryFn: () => listAgents(), staleTime: 60000 })
  const activeAgents = (agents.data ?? []).filter((a) => !a.archived)
  const save = useMutation({
    mutationFn: () =>
      updateSquad(squad.id, {
        name: name.trim(),
        instructions,
        ...(leaderId ? { leader_agent_id: leaderId } : {}),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['squad', squad.id] })
      onSaved()
    },
  })
  const dirty =
    name !== squad.name ||
    instructions !== squad.instructions ||
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
          <FormField label={t('agents.name')} htmlFor="squad-name">
            <Input
              id="squad-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={100}
            />
          </FormField>
        </div>
        <div className="min-w-48 flex-1">
          <FormField label={t('squads.leaderLabel')} htmlFor="squad-leader">
            <Select
              id="squad-leader"
              value={leaderId}
              onChange={(e) => setLeaderId(e.target.value)}
            >
              <option value="">{t('squads.leaderUnset')}</option>
              {activeAgents.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </Select>
          </FormField>
        </div>
      </div>

      <FormField label={t('squads.instructionsLabel')} htmlFor="squad-instructions">
        <Textarea
          id="squad-instructions"
          value={instructions}
          onChange={(e) => setInstructions(e.target.value)}
          rows={3}
          className="resize-none"
        />
      </FormField>

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
  const confirm = useConfirm()
  const [addOpen, setAddOpen] = useState(false)
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
      toast.success(t('squads.removeSuccess'))
      onChanged()
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : t('squads.removeFailed')),
  })
  // 移除两段式 → useConfirm 统一形态：对象与后果在弹窗里说一次，行内不再放确认链。
  const askRemoveMember = async (agentId: string, name: string) => {
    const ok = await confirm({
      title: t('squads.confirmRemove'),
      description: t('squads.removeHint', { name }),
      confirmText: t('common.confirm'),
      cancelText: t('common.cancel'),
      danger: true,
    })
    if (ok) remove.mutate(agentId)
  }
  return (
    // 区块容器与基本信息表单统一为同一张边框卡。
    <section className="space-y-3 rounded-lg border border-zinc-200 p-4 dark:border-zinc-800">
      <div className="flex items-center justify-between">
        <p className="text-sm font-semibold">
          {t('squads.membersTitle', { n: members.data?.length ?? 0 })}
        </p>
        <Button size="sm" onClick={() => setAddOpen(true)}>
          <Plus className="size-3.5" aria-hidden="true" />
          {t('squads.addMember')}
        </Button>
      </div>

      {members.isPending && (
        <div className="space-y-2" aria-hidden="true">
          <Skeleton className="h-11 rounded-lg" />
          <Skeleton className="h-11 rounded-lg" />
          <Skeleton className="h-11 rounded-lg" />
        </div>
      )}
      {members.isError && (
        <PageError
          title={t('squads.membersFailed')}
          message={members.error instanceof Error ? members.error.message : undefined}
          onRetry={() => void members.refetch()}
        />
      )}

      {members.data && members.data.length === 0 && (
        <PageEmpty title={t('squads.noMembers')} className="py-8" />
      )}

      {members.data && members.data.length > 0 && (
        <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
          {members.data.map((m) => {
            const st = statusByAgent.get(m.agent_id)
            const meta = STATUS_TONES[st?.status ?? 'idle'] ?? STATUS_TONES.idle!
            return (
              <li key={m.agent_id} className="flex items-center gap-3 px-3 py-2.5">
                <StatusBadge
                  status={st?.status}
                  label={t(meta.labelKey)}
                  tone={meta.tone}
                  className="shrink-0"
                />
                <div className="min-w-0 flex-1">
                  <p className={'truncate text-sm font-medium ' + (m.archived ? 'opacity-55' : '')}>
                    {m.name}
                    {m.archived && (
                      <Badge variant="neutral" className="ml-1.5 align-middle">
                        {t('squads.memberArchived')}
                      </Badge>
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
                <Button
                  size="sm"
                  variant="ghost"
                  className="shrink-0 text-red-600 dark:text-red-400"
                  onClick={() => void askRemoveMember(m.agent_id, m.name)}
                >
                  {t('common.remove')}
                </Button>
              </li>
            )
          })}
        </ul>
      )}

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
    // 角色 chip：手写 pill+原生 title → Badge secondary + kit Tooltip；可点编辑的
    // affordance 交给真实按钮的 hover 态，不再用常驻教学文案。
    return (
      <Tooltip>
        <TooltipTrigger
          render={
            <Badge
              variant="secondary"
              render={
                <button
                  type="button"
                  className="cursor-pointer"
                  aria-label={t('squads.role')}
                  onClick={() => setEditing(true)}
                />
              }
            />
          }
        >
          {role}
        </TooltipTrigger>
        <TooltipContent>{t('squads.role')}</TooltipContent>
      </Tooltip>
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
  const agents = useQuery({ queryKey: ['agents'], queryFn: () => listAgents(), staleTime: 60000 })
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
    <Dialog open onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('squads.addMember')}</DialogTitle>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (agentId && !add.isPending) add.mutate()
          }}
        >
          <FormField
            label={t('squads.agentLabel')}
            htmlFor="member-agent"
            description={candidates.length === 0 ? t('squads.noCandidates') : undefined}
          >
            <Select
              id="member-agent"
              value={agentId}
              onChange={(e) => setAgentId(e.target.value)}
              autoFocus
            >
              <option value="">{t('squads.selectAgent')}</option>
              {candidates.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </Select>
          </FormField>
          <FormField label={t('squads.roleLabel')} htmlFor="member-role">
            <Input
              id="member-role"
              value={role}
              onChange={(e) => setRole(e.target.value)}
              maxLength={80}
              placeholder="member"
            />
          </FormField>
          {error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {error}
            </p>
          )}
          <div className="flex justify-end gap-2">
            <DialogClose render={<Button variant="outline" type="button" />}>
              {t('common.cancel')}
            </DialogClose>
            <Button type="submit" disabled={!agentId || add.isPending}>
              {add.isPending ? t('squads.adding') : t('squads.add')}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}

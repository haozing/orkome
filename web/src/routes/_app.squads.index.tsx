import { Dialog } from '@base-ui/react/dialog'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link, useNavigate } from '@tanstack/react-router'
import { Plus, Users } from 'lucide-react'
import { useState } from 'react'
import { listAgents } from '@/api/orchestration'
import { createSquad, listSquads, type Squad } from '@/api/squads'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { ApiError } from '@ploykit/client'
import { useTranslation } from 'react-i18next'
export const Route = createFileRoute('/_app/squads/')({
  component: SquadsPage,
})
const LABEL_CLASS = 'mb-1 block text-xs text-zinc-500'
function SquadsPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [createOpen, setCreateOpen] = useState(false)
  const squads = useQuery({ queryKey: ['squads'], queryFn: listSquads })
  const agents = useQuery({
    queryKey: ['agents'],
    queryFn: listAgents,
    staleTime: 60000,
  })
  const agentName = new Map((agents.data ?? []).map((a) => [a.id, a.name]))
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">{t('nav.squads')}</h1>
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          <Plus className="size-3.5" aria-hidden="true" />
          {t('squadsList.new')}
        </Button>
      </div>

      {squads.isPending && <PageLoading label={t('squads.loading')} />}

      {squads.isError && (
        <PageError
          title={t('common.error')}
          message={squads.error instanceof Error ? squads.error.message : undefined}
          onRetry={() => void squads.refetch()}
        />
      )}

      {squads.isSuccess && squads.data.length === 0 && (
        <PageEmpty title={t('squadsList.emptyTitle')} description={t('squadsList.emptyDesc')} />
      )}

      {squads.data && squads.data.length > 0 && (
        <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
          {squads.data.map((sq) => (
            <li key={sq.id}>
              <Link
                to="/squads/$squadId"
                params={{ squadId: sq.id }}
                className="flex items-center gap-3 px-3 py-2.5 text-sm outline-none hover:bg-zinc-50 dark:hover:bg-zinc-900/50"
              >
                <Users className="size-4 shrink-0 text-zinc-400" aria-hidden="true" />
                <span className="min-w-0 flex-1 truncate font-medium">{sq.name}</span>
                <span className="hidden shrink-0 text-xs text-zinc-500 sm:inline dark:text-zinc-400">
                  {t('squadsList.leaderPrefix')}
                  {sq.leader_agent_id
                    ? (agentName.get(sq.leader_agent_id) ?? t('squadsList.unnamed'))
                    : t('squadsList.unset')}
                </span>
                <span className="shrink-0 text-xs text-zinc-400">
                  {t('squadsList.membersCount', { n: sq.members?.length ?? 0 })}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}

      <CreateSquadDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(sq) => void navigate({ to: '/squads/$squadId', params: { squadId: sq.id } })}
      />
    </div>
  )
}
function CreateSquadDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (squad: Squad) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [instructions, setInstructions] = useState('')
  const create = useMutation({
    mutationFn: () =>
      createSquad({ name: name.trim(), instructions: instructions.trim() }),
    onSuccess: (sq) => {
      void queryClient.invalidateQueries({ queryKey: ['squads'] })
      setName('')
      setInstructions('')
      onOpenChange(false)
      onCreated(sq)
    },
  })
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Popup className="fixed top-1/2 left-1/2 z-50 w-full max-w-sm -translate-x-1/2 -translate-y-1/2 rounded-lg border border-zinc-200 bg-white p-5 shadow-xl dark:border-zinc-700 dark:bg-zinc-900">
          <Dialog.Title className="mb-4 text-sm font-semibold">{t('squadsList.new')}</Dialog.Title>
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault()
              if (name.trim() && !create.isPending) create.mutate()
            }}
          >
            <div>
              <label htmlFor="squad-name" className={LABEL_CLASS}>
                {t('agents.nameRequired')}
              </label>
              <Input
                id="squad-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={100}
                autoFocus
                placeholder={t('squadsList.namePlaceholder')}
              />
            </div>
            <div>
              <label htmlFor="squad-instructions" className={LABEL_CLASS}>
                {t('squadsList.instructionsLabel')}
              </label>
              <textarea
                id="squad-instructions"
                value={instructions}
                onChange={(e) => setInstructions(e.target.value)}
                rows={3}
                placeholder={t('squadsList.instructionsPlaceholder')}
                className="w-full resize-none rounded-md border border-zinc-300 bg-white px-3 py-1.5 text-sm outline-none focus:border-zinc-400 dark:border-zinc-700 dark:bg-zinc-900"
              />
            </div>
            {create.error && (
              <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                {create.error instanceof ApiError ? create.error.message : t('skills.createFailed')}
              </p>
            )}
            <p className="text-xs text-zinc-400">{t('squadsList.createHint')}</p>
            <div className="flex justify-end gap-2 pt-1">
              <Dialog.Close className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm hover:bg-zinc-100 dark:border-zinc-700 dark:hover:bg-zinc-800">
                {t('common.cancel')}
              </Dialog.Close>
              <button
                type="submit"
                disabled={!name.trim() || create.isPending}
                className="rounded-md bg-zinc-900 px-3 py-1.5 text-sm text-white disabled:opacity-50 dark:bg-zinc-100 dark:text-zinc-900"
              >
                {create.isPending ? t('skills.creating') : t('common.create')}
              </button>
            </div>
          </form>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

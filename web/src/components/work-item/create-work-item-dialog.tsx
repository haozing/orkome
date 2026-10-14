import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Suspense, lazy, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { createWorkItem, listProjects, listStatuses, type WorkItemCreateInput } from '@/api/work'
import { membersQueryOptions } from '@/api/identity'
import { listAgents } from '@/api/orchestration'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { PRIORITY_META, statusMetaOf } from '@/components/work-item/meta'
import { useWorkspaceStore } from '@/stores/workspace'
const RichEditor = lazy(() =>
  import('@/components/editor/rich-editor').then((m) => ({ default: m.RichEditor })),
)
const SELECT_CLASS =
  'h-8 rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900'
export interface CreateWorkItemDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated?: (item: { id: string; title: string }) => void
}
export function CreateWorkItemDialog({ open, onOpenChange, onCreated }: CreateWorkItemDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const workspaceId = useWorkspaceStore((s) => s.currentId) ?? ''
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [projectId, setProjectId] = useState('')
  const [status, setStatus] = useState('todo')
  const [priority, setPriority] = useState(0)
  const [assigneeType, setAssigneeType] = useState<'none' | 'user' | 'agent'>('none')
  const [assigneeId, setAssigneeId] = useState('')
  const projects = useQuery({ queryKey: ['projects'], queryFn: listProjects, enabled: open })
  const statuses = useQuery({
    queryKey: ['work-item-statuses'],
    queryFn: listStatuses,
    enabled: open,
  })
  const members = useQuery(membersQueryOptions(workspaceId))
  const agents = useQuery({ queryKey: ['agents'], queryFn: () => listAgents(), staleTime: 60000 })
  const statusName = (key: string) => {
    const def = statuses.data?.find((s) => s.key === key)
    return def ? (def.is_builtin ? t(statusMetaOf(def.key).labelKey) : def.name) : key
  }
  const resetEditorial = () => {
    setTitle('')
    setDescription('')
  }
  const close = () => {
    onOpenChange(false)
    resetEditorial()
  }
  const create = useMutation({
    mutationFn: (input: WorkItemCreateInput) => createWorkItem(input),
    onSuccess: (item) => {
      void queryClient.invalidateQueries({ queryKey: ['work-items'] })
      onCreated?.(item)
      resetEditorial()
    },
  })
  const payload = (): WorkItemCreateInput => ({
    title: title.trim(),
    description,
    status: status || undefined,
    priority,
    project_id: projectId || undefined,
    assignee_type: assigneeType,
    assignee_id: assigneeType === 'none' ? '' : assigneeId,
  })
  const submit = (keepOpen: boolean) => {
    if (!title.trim() || create.isPending) return
    create.mutate(payload(), {
      onSuccess: () => {
        if (!keepOpen) close()
      },
    })
  }
  return (
    <Dialog open={open} onOpenChange={(o) => (o ? undefined : close())}>
      <DialogContent className="flex max-h-[85dvh] flex-col gap-3 overflow-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('work.createWorkItem')}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-wrap items-center gap-2">
          <select
            value={projectId}
            aria-label={t('work.project')}
            onChange={(e) => setProjectId(e.target.value)}
            className={SELECT_CLASS}
          >
            <option value="">{t('work.noProject')}</option>
            {projects.data?.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
          <select
            value={status}
            aria-label={t('work.colStatus')}
            onChange={(e) => setStatus(e.target.value)}
            className={SELECT_CLASS}
          >
            {statuses.data?.map((s) => (
              <option key={s.key} value={s.key}>
                {statusName(s.key)}
              </option>
            ))}
          </select>
          <select
            value={priority}
            aria-label={t('work.colPriority')}
            onChange={(e) => setPriority(Number(e.target.value))}
            className={SELECT_CLASS}
          >
            {Object.entries(PRIORITY_META).map(([value, m]) => (
              <option key={value} value={value}>
                {t(m.labelKey)}
              </option>
            ))}
          </select>
          <select
            value={assigneeType === 'none' ? '' : `${assigneeType}:${assigneeId}`}
            aria-label={t('work.colAssignee')}
            onChange={(e) => {
              const v = e.target.value
              if (!v) {
                setAssigneeType('none')
                setAssigneeId('')
              } else {
                const [kind, ...rest] = v.split(':')
                setAssigneeType(kind as 'user' | 'agent')
                setAssigneeId(rest.join(':'))
              }
            }}
            className={SELECT_CLASS}
          >
            <option value="">{t('work.unassigned')}</option>
            {members.data && members.data.length > 0 && (
              <optgroup label={t('agents.member')}>
                {members.data.map((m) => (
                  <option key={m.user_id} value={`user:${m.user_id}`}>
                    {m.display_name || m.email}
                  </option>
                ))}
              </optgroup>
            )}
            {agents.data && agents.data.length > 0 && (
              <optgroup label={t('nav.agents')}>
                {agents.data.map((a) => (
                  <option key={a.id} value={`agent:${a.id}`}>
                    {a.name}
                  </option>
                ))}
              </optgroup>
            )}
          </select>
        </div>

        <Input
          autoFocus
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault()
              submit(false)
            }
          }}
          placeholder={t('work.titlePlaceholder')}
          aria-label={t('work.titleAria')}
          maxLength={500}
          className="h-10 text-base"
        />

        <div className="min-h-40 rounded-md border border-zinc-200 dark:border-zinc-800">
          <Suspense
            fallback={<div className="p-3 text-sm text-zinc-400">{t('work.editorLoading')}</div>}
          >
            <RichEditor
              content={description}
              onChange={setDescription}
              placeholder={t('work.descPlaceholder')}
              ariaLabel={t('work.descAria')}
              minRows={6}
            />
          </Suspense>
        </div>

        {create.error && (
          <p role="alert" className="text-xs text-red-600 dark:text-red-400">
            {create.error instanceof Error ? create.error.message : t('work.createFailed')}
          </p>
        )}

        <div className="flex items-center gap-2">
          {create.isSuccess && !title.trim() && (
            <span role="status" className="text-xs text-zinc-400">
              {t('work.createdContinue')}
            </span>
          )}
          <div className="ml-auto flex items-center gap-2">
            <Button
              size="sm"
              disabled={!title.trim() || create.isPending}
              onClick={() => submit(false)}
            >
              {create.isPending ? t('work.creating') : t('common.create')}
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={!title.trim() || create.isPending}
              onClick={() => submit(true)}
            >
              {t('work.submitAndContinue')}
            </Button>
            <Button variant="ghost" size="sm" onClick={close}>
              {t('common.cancel')}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

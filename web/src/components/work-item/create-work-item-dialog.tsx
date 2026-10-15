import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, CircleDashed } from 'lucide-react'
import { Suspense, lazy, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { createWorkItem, listProjects, listStatuses, type WorkItemCreateInput } from '@/api/work'
import { getWorkflow } from '@/api/workflow'
import { meQueryOptions, membersQueryOptions } from '@/api/identity'
import { listAgents } from '@/api/orchestration'
import { BadgeDot } from '@/components/work-item/property-rows'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { PRIORITY_META, statusMetaOf } from '@/components/work-item/meta'
import { useWorkspaceStore } from '@/stores/workspace'
const RichEditor = lazy(() =>
  import('@/components/editor/rich-editor').then((m) => ({ default: m.RichEditor })),
)

// Linear 风格属性行：触发器是圆角胶囊（彩色点/图标 + 文字 + chevron），
// 点开是 RadioGroup 菜单；主体留给标题与描述。
const TRIGGER_CLASS =
  'flex h-7 items-center gap-1.5 rounded-md border border-transparent px-2 text-sm text-zinc-600 outline-none hover:bg-zinc-100 data-[popup-open]:bg-zinc-100 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:data-[popup-open]:bg-zinc-800'

function PropertyTrigger({
  label,
  menuLabel,
  children,
}: {
  label: React.ReactNode
  menuLabel: string
  children: React.ReactNode
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className={TRIGGER_CLASS} aria-label={menuLabel}>
        {label}
        <ChevronDown className="size-3.5 text-zinc-400" aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-44">
        {children}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function RadioItemWithDot({
  value,
  label,
  dot,
}: {
  value: string
  label: string
  dot?: React.ReactNode
}) {
  return (
    <DropdownMenuRadioItem value={value} className="pl-1.5">
      {dot}
      {label}
    </DropdownMenuRadioItem>
  )
}
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
  // 负责人默认 = 当前登录用户：me 到达且用户未手动改过时套用（渲染期调整状态模式）。
  const [assigneeTouched, setAssigneeTouched] = useState(false)
  const projects = useQuery({ queryKey: ['projects'], queryFn: listProjects, enabled: open })
  // 选中的项目有无流程定义（有则属性行提示「流程 N 阶段」Badge；404=无定义 → null）
  const workflow = useQuery({
    queryKey: ['project-workflow', projectId],
    queryFn: () => getWorkflow(projectId),
    enabled: open && projectId !== '',
    retry: false,
  })
  const statuses = useQuery({
    queryKey: ['work-item-statuses'],
    queryFn: listStatuses,
    enabled: open,
  })
  const members = useQuery(membersQueryOptions(workspaceId))
  const me = useQuery(meQueryOptions)
  const agents = useQuery({ queryKey: ['agents'], queryFn: () => listAgents(), staleTime: 60000 })
  const meUserId = me.data?.user.id
  if (meUserId && !assigneeTouched && assigneeType === 'none') {
    setAssigneeType('user')
    setAssigneeId(meUserId)
  }
  const statusName = (key: string) => {
    const def = statuses.data?.find((s) => s.key === key)
    return def ? (def.is_builtin ? t(statusMetaOf(def.key).labelKey) : def.name) : key
  }
  const assigneeLabel = (type: 'user' | 'agent', id: string): string | null => {
    if (type === 'user') {
      const m = members.data?.find((x) => x.user_id === id)
      return m ? (m.display_name || m.email) : null
    }
    return agents.data?.find((x) => x.id === id)?.name ?? null
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

        {/* 属性行沉底：标题+描述是创建主语，属性是次要元数据（Linear 同构）。
            顺序=状态→优先级→负责人→项目（用户裁决：项目选择放后面）。 */}
        <div className="-mx-1 flex flex-wrap items-center gap-1 border-t border-zinc-100 pt-2 dark:border-zinc-800">
          <PropertyTrigger
            menuLabel={t('work.colStatus')}
            label={
              <>
                <BadgeDot status={status} />
                {statusName(status)}
              </>
            }
          >
            <DropdownMenuRadioGroup value={status} onValueChange={(v) => setStatus(String(v))}>
              {statuses.data?.map((s) => (
                <RadioItemWithDot
                  key={s.key}
                  value={s.key}
                  label={statusName(s.key)}
                  dot={<BadgeDot status={s.key} />}
                />
              ))}
            </DropdownMenuRadioGroup>
          </PropertyTrigger>

          <PropertyTrigger
            menuLabel={t('work.colPriority')}
            label={
              <>
                <span
                  className={
                    'text-xs ' + (PRIORITY_META[priority]?.className ?? 'text-zinc-400')
                  }
                >
                  ●
                </span>
                {priority === 0 ? t('priority.unset') : t(PRIORITY_META[priority]?.labelKey ?? 'priority.none')}
              </>
            }
          >
            <DropdownMenuRadioGroup
              value={String(priority)}
              onValueChange={(v) => setPriority(Number(v))}
            >
              {Object.entries(PRIORITY_META).map(([value, m]) => (
                <RadioItemWithDot
                  key={value}
                  value={value}
                  label={Number(value) === 0 ? t('priority.unset') : t(m.labelKey)}
                  dot={<span className={'text-xs ' + m.className}>●</span>}
                />
              ))}
            </DropdownMenuRadioGroup>
          </PropertyTrigger>

          <PropertyTrigger
            menuLabel={t('work.colAssignee')}
            label={
              assigneeType === 'none'
                ? t('work.unassigned')
                : (assigneeLabel(assigneeType, assigneeId) ?? t('work.colAssignee'))
            }
          >
            <DropdownMenuRadioGroup
              value={assigneeType === 'none' ? '' : `${assigneeType}:${assigneeId}`}
              onValueChange={(v) => {
                setAssigneeTouched(true)
                if (!v) {
                  setAssigneeType('none')
                  setAssigneeId('')
                } else {
                  const [kind, ...rest] = String(v).split(':')
                  setAssigneeType(kind as 'user' | 'agent')
                  setAssigneeId(rest.join(':'))
                }
              }}
            >
              <RadioItemWithDot
                value=""
                label={t('work.unassigned')}
                dot={<CircleDashed className="size-3.5 text-zinc-400" aria-hidden="true" />}
              />
              {members.data && members.data.length > 0 && (
                <>
                  <p className="px-2 py-1 text-xs text-zinc-400">{t('agents.member')}</p>
                  {members.data.map((m) => (
                    <RadioItemWithDot
                      key={m.user_id}
                      value={`user:${m.user_id}`}
                      label={m.display_name || m.email}
                    />
                  ))}
                </>
              )}
              {agents.data && agents.data.length > 0 && (
                <>
                  <p className="px-2 py-1 text-xs text-zinc-400">{t('nav.agents')}</p>
                  {agents.data.map((a) => (
                    <RadioItemWithDot key={a.id} value={`agent:${a.id}`} label={a.name} />
                  ))}
                </>
              )}
            </DropdownMenuRadioGroup>
          </PropertyTrigger>

          <PropertyTrigger
            menuLabel={t('work.project')}
            label={
              <>
                {projectId
                  ? (projects.data?.find((p) => p.id === projectId)?.name ?? t('work.project'))
                  : t('work.noProject')}
              </>
            }
          >
            <DropdownMenuRadioGroup
              value={projectId}
              onValueChange={(v) => setProjectId(String(v))}
            >
              <RadioItemWithDot
                value=""
                label={t('work.noProject')}
                dot={<CircleDashed className="size-3.5 text-zinc-400" aria-hidden="true" />}
              />
              {projects.data?.map((p) => (
                <RadioItemWithDot
                  key={p.id}
                  value={p.id}
                  label={p.name}
                  dot={
                    <span
                      className="size-2 shrink-0 rounded-[3px] bg-indigo-500"
                      aria-hidden="true"
                    />
                  }
                />
              ))}
            </DropdownMenuRadioGroup>
          </PropertyTrigger>

          {/* 选中的项目挂了流程：不可点提示 Badge（auto 模式创建即自动进入） */}
          {projectId && workflow.data && (
            <span
              className="inline-flex h-5 items-center rounded-4xl bg-secondary px-2 text-xs text-secondary-foreground"
              title={workflow.data.stages.map((s) => s.name).join(' → ')}
            >
              {t('work.workflowBadge', { n: workflow.data.stages.length })}
            </span>
          )}
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

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useRouterState } from '@tanstack/react-router'
import { ChevronRight, Plus, Star, Workflow } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { createProject, listProjects, type Project } from '@/api/work'
import { WorkflowEditor } from '@/components/workflow/workflow-editor'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { ApiError } from '@ploykit/client'
import { cn } from '@/lib/utils'
import { useWorkspaceStore } from '@/stores/workspace'
const COLLAPSED_COUNT = 5
const AVATAR_COLORS = [
  'bg-sky-500',
  'bg-emerald-500',
  'bg-violet-500',
  'bg-amber-500',
  'bg-rose-500',
  'bg-cyan-500',
  'bg-indigo-500',
  'bg-lime-500',
] as const
export function avatarColorOf(id: string) {
  let hash = 0
  for (let i = 0; i < id.length; i++) hash = (hash * 31 + id.charCodeAt(i)) | 0
  return AVATAR_COLORS[Math.abs(hash) % AVATAR_COLORS.length]!
}
const starredKey = (workspaceId: string) => `orkome.starred-projects:${workspaceId}`
export function loadStarred(workspaceId: string | null): string[] {
  if (!workspaceId) return []
  try {
    const raw = localStorage.getItem(starredKey(workspaceId))
    const parsed = raw ? (JSON.parse(raw) as unknown) : []
    return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === 'string') : []
  } catch {
    return []
  }
}
export function ProjectNav() {
  const { t } = useTranslation()
  const workspaceId = useWorkspaceStore((s) => s.currentId)
  const projects = useQuery({ queryKey: ['projects'], queryFn: listProjects })
  const [createOpen, setCreateOpen] = useState(false)
  const [expanded, setExpanded] = useState(false)
  const [starred, setStarred] = useState<string[]>([])
  // W1 项目流程编辑器入口：当前打开编辑器的项目 id（null = 关闭）。
  const [workflowProjectId, setWorkflowProjectId] = useState<string | null>(null)
  const routerSearch = useRouterState({ select: (s) => s.location.search }) as {
    project?: string
  }
  const activeProjectId = routerSearch.project
  useEffect(() => {
    setStarred(workspaceId ? loadStarred(workspaceId) : [])
  }, [workspaceId])
  const toggleStar = (projectId: string) => {
    if (!workspaceId) return
    setStarred((prev) => {
      const next = prev.includes(projectId)
        ? prev.filter((id) => id !== projectId)
        : [...prev, projectId]
      try {
        localStorage.setItem(starredKey(workspaceId), JSON.stringify(next))
        // eslint-disable-next-line no-empty
      } catch {}
      return next
    })
  }
  const sorted = [...(projects.data ?? [])].sort((a, b) => {
    const sa = starred.includes(a.id) ? 0 : 1
    const sb = starred.includes(b.id) ? 0 : 1
    if (sa !== sb) return sa - sb
    return b.created_at.localeCompare(a.created_at)
  })
  const visible = expanded ? sorted : sorted.slice(0, COLLAPSED_COUNT)
  const hiddenCount = sorted.length - visible.length
  return (
    <div className="flex flex-col">
      <div className="flex items-center justify-between pt-3 pr-2 pl-3">
        <span className="text-xs text-zinc-400">{t('projects.recent')}</span>
        <button
          type="button"
          aria-label={t('projects.newAria')}
          className="flex size-5 items-center justify-center rounded text-zinc-400 outline-none hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
          onClick={() => setCreateOpen(true)}
        >
          <Plus className="size-3.5" aria-hidden="true" />
        </button>
      </div>

      {projects.isError && (
        <p role="alert" className="px-2 pt-2 text-xs text-red-600 dark:text-red-400">
          {t('projects.loadFailed')}
        </p>
      )}

      <nav aria-label={t('projects.recent')} className="mt-1 max-h-60 space-y-0.5 overflow-y-auto">
        {visible.map((p) => (
          <ProjectRow
            key={p.id}
            project={p}
            active={p.id === activeProjectId}
            starred={starred.includes(p.id)}
            onToggleStar={() => toggleStar(p.id)}
            onOpenWorkflow={() => setWorkflowProjectId(p.id)}
          />
        ))}
        {projects.isSuccess && sorted.length === 0 && (
          <p className="px-3 py-1 text-xs text-zinc-400">{t('projects.empty')}</p>
        )}
      </nav>

      {hiddenCount > 0 && (
        <button
          type="button"
          className="flex items-center gap-1 rounded-md px-3 py-1 text-xs text-zinc-400 outline-none hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
          onClick={() => setExpanded(true)}
        >
          {t('common.more')} <ChevronRight className="size-3" aria-hidden="true" />
        </button>
      )}
      {expanded && sorted.length > COLLAPSED_COUNT && (
        <button
          type="button"
          className="flex items-center gap-1 rounded-md px-3 py-1 text-xs text-zinc-400 outline-none hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
          onClick={() => setExpanded(false)}
        >
          {t('projects.collapse')}
        </button>
      )}

      <CreateProjectDialog open={createOpen} onOpenChange={setCreateOpen} />
      <WorkflowDialog projectId={workflowProjectId} onOpenChange={setWorkflowProjectId} />
    </div>
  )
}
function ProjectRow({
  project,
  active,
  starred,
  onToggleStar,
  onOpenWorkflow,
}: {
  project: Project
  active: boolean
  starred: boolean
  onToggleStar: () => void
  onOpenWorkflow: () => void
}) {
  const { t } = useTranslation()
  return (
    <div
      className={cn(
        'group flex items-center rounded-md pr-1',
        active ? 'bg-zinc-100 dark:bg-zinc-800' : 'hover:bg-zinc-100 dark:hover:bg-zinc-800',
      )}
    >
      <Link
        to="/work-items"
        search={{ project: project.id }}
        className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-3 py-1 outline-none"
        title={project.name}
      >
        <span
          className={cn('size-4 shrink-0 rounded', avatarColorOf(project.id))}
          aria-hidden="true"
        />
        <span className="min-w-0 truncate text-sm text-zinc-700 dark:text-zinc-300">
          {project.name}
        </span>
      </Link>
      <button
        type="button"
        aria-label={t('workflow.openEditorAria', { name: project.name })}
        title={t('workflow.openEditorAria', { name: project.name })}
        className="flex size-6 shrink-0 items-center justify-center rounded text-zinc-400 outline-none opacity-0 group-hover:opacity-100 hover:bg-zinc-100 hover:text-zinc-600 focus-visible:opacity-100 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
        onClick={onOpenWorkflow}
      >
        <Workflow className="size-3.5" aria-hidden="true" />
      </button>
      <button
        type="button"
        aria-label={starred ? t('projects.unstar') : t('projects.star')}
        className={cn(
          'flex size-6 shrink-0 items-center justify-center rounded outline-none',
          starred
            ? 'text-amber-400'
            : 'text-zinc-400 opacity-0 group-hover:opacity-100 hover:text-zinc-600 focus-visible:opacity-100 dark:hover:text-zinc-300',
        )}
        onClick={onToggleStar}
      >
        <Star className={cn('size-3.5', starred && 'fill-current')} aria-hidden="true" />
      </button>
    </div>
  )
}

// W1 项目流程编辑器挂载点：项目尚无详情页（project 行直接链到 /work-items?project=），
// 侧栏行内的 Workflow 图标 + Dialog 是最小侵入入口；项目详情页落地后（W3）把
// WorkflowEditor 平移进页面区块即可，组件本身不感知载体。
function WorkflowDialog({
  projectId,
  onOpenChange,
}: {
  projectId: string | null
  onOpenChange: (projectId: string | null) => void
}) {
  const { t } = useTranslation()
  const project = useQuery({
    queryKey: ['projects'],
    queryFn: listProjects,
    enabled: projectId !== null,
    select: (projects: Project[]) => projects.find((p) => p.id === projectId),
  })
  return (
    <Dialog
      open={projectId !== null}
      onOpenChange={(open) => {
        if (!open) onOpenChange(null)
      }}
    >
      <DialogContent className="sm:max-w-xl">
        <DialogTitle>{t('workflow.editorTitle')}</DialogTitle>
        <DialogDescription className="text-xs">
          {project.data ? project.data.name : ''}
        </DialogDescription>
        <div className="max-h-[60vh] overflow-y-auto pr-1">
          {projectId !== null && <WorkflowEditor projectId={projectId} />}
        </div>
        <DialogFooter>
          <DialogClose className="border-border bg-background hover:bg-muted inline-flex h-9 items-center justify-center rounded-lg border px-3.5 text-sm font-medium outline-none">
            {t('common.close')}
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
function CreateProjectDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const create = useMutation({
    mutationFn: () => createProject({ name: name.trim(), description: description.trim() }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['projects'] })
      setName('')
      setDescription('')
      onOpenChange(false)
    },
  })
  const submit = () => {
    if (!name.trim() || create.isPending) return
    create.mutate()
  }
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogTitle>{t('projects.newTitle')}</DialogTitle>
        <DialogDescription className="text-xs">{t('projects.newDesc')}</DialogDescription>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <div>
            <label htmlFor="project-name" className="text-muted-foreground mb-1 block text-xs">
              {t('projects.nameLabel')}
            </label>
            <Input
              id="project-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={100}
              autoFocus
              placeholder={t('projects.namePlaceholder')}
            />
          </div>
          <div>
            <label htmlFor="project-desc" className="text-muted-foreground mb-1 block text-xs">
              {t('projects.descLabel')}
            </label>
            <textarea
              id="project-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              rows={3}
              placeholder={t('projects.descPlaceholder')}
              className="border-input focus-visible:border-ring dark:bg-input/30 w-full resize-none rounded-md border bg-transparent px-3 py-1.5 text-sm outline-none"
            />
          </div>
          {create.error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {create.error instanceof ApiError ? create.error.message : t('skills.createFailed')}
            </p>
          )}
          <DialogFooter>
            <DialogClose className="border-border bg-background hover:bg-muted inline-flex h-9 items-center justify-center rounded-lg border px-3.5 text-sm font-medium outline-none">
              {t('common.cancel')}
            </DialogClose>
            <Button type="submit" disabled={!name.trim() || create.isPending}>
              {create.isPending ? t('skills.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

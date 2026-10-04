import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import {
  listProjects,
  listStatuses,
  workItemsInfiniteQuery,
  type Project,
  type WorkItem,
  type WorkItemStatus,
  type WorkItemListFilters,
} from '@/api/work'
import { meQueryOptions } from '@/api/identity'
import { Button } from '@/components/ui/button'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { CreateWorkItemDialog } from '@/components/work-item/create-work-item-dialog'
import { statusMetaOf } from '@/components/work-item/meta'
import {
  WorkItemList,
  WorkItemRow,
  WorkItemTableHeader,
} from '@/components/work-item/work-item-list'
const searchSchema = z.object({
  scope: z.enum(['todo', 'done', 'created', 'subscribed']).optional(),
  project: z.string().optional(),
  group: z.enum(['time', 'project', 'status']).optional(),
})
export const Route = createFileRoute('/_app/my-work')({
  validateSearch: (search) => {
    const parsed = searchSchema.safeParse(search)
    return parsed.success ? parsed.data : {}
  },
  component: MyWorkPage,
})
const SCOPES = [
  { key: 'todo', label: 'myWork.scopeTodo' },
  { key: 'done', label: 'myWork.scopeDone' },
  { key: 'created', label: 'myWork.scopeCreated' },
  { key: 'subscribed', label: 'myWork.scopeSubscribed' },
] as const
type Scope = (typeof SCOPES)[number]['key']
const GROUP_MODES = [
  { key: 'time', label: 'myWork.groupTime' },
  { key: 'project', label: 'myWork.groupProject' },
  { key: 'status', label: 'myWork.groupStatus' },
] as const
type GroupMode = (typeof GROUP_MODES)[number]['key']
interface ListSection {
  key: string
  label: string
  items: WorkItem[]
}
function groupByProjects(
  items: WorkItem[],
  projects: Project[],
  noProjectLabel: string,
): ListSection[] {
  const byKey = new Map<string, WorkItem[]>()
  for (const it of items) {
    const k = it.project_id ?? ''
    byKey.set(k, [...(byKey.get(k) ?? []), it])
  }
  const sections: ListSection[] = projects
    .filter((p) => byKey.has(p.id))
    .map((p) => ({ key: `p:${p.id}`, label: p.name, items: byKey.get(p.id)! }))
  const none = byKey.get('')
  if (none) sections.push({ key: 'p:none', label: noProjectLabel, items: none })
  return sections
}
function groupByStatuses(
  items: WorkItem[],
  statuses: WorkItemStatus[],
  translateBuiltin: (key: string) => string,
): ListSection[] {
  const byKey = new Map<string, WorkItem[]>()
  for (const it of items) {
    byKey.set(it.status, [...(byKey.get(it.status) ?? []), it])
  }
  const sections: ListSection[] = [...statuses]
    .sort((a, b) => a.position - b.position)
    .filter((s) => byKey.has(s.key))
    .map((s) => ({
      key: `s:${s.key}`,
      label: s.is_builtin ? translateBuiltin(s.key) : s.name,
      items: byKey.get(s.key)!,
    }))
  const known = new Set(statuses.map((s) => s.key))
  for (const [k, arr] of byKey) {
    if (!known.has(k)) sections.push({ key: `s:${k}`, label: k, items: arr })
  }
  return sections
}
function MyWorkPage() {
  const { t } = useTranslation()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const scope: Scope = search.scope ?? 'todo'
  const projectId = search.project
  const groupMode: GroupMode = search.group ?? 'time'
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const [createOpen, setCreateOpen] = useState(false)
  const me = useQuery(meQueryOptions)
  const userId = me.data?.user.id
  const projects = useQuery({ queryKey: ['projects'], queryFn: listProjects })
  const statuses = useQuery({ queryKey: ['work-item-statuses'], queryFn: listStatuses })
  const openKeys = statuses.data
    ?.filter((s) => s.category === 'unstarted' || s.category === 'started')
    .map((s) => s.key)
  const closedKeys = statuses.data
    ?.filter((s) => s.category === 'done' || s.category === 'closed')
    .map((s) => s.key)
  const scoped: WorkItemListFilters | null = !userId
    ? null
    : scope === 'created'
      ? { creator: userId }
      : scope === 'subscribed'
        ? { subscriber: userId }
        : scope === 'todo'
          ? openKeys
            ? { assignee: userId, statuses: openKeys }
            : null
          : closedKeys
            ? { assignee: userId, statuses: closedKeys }
            : null
  const filters: WorkItemListFilters | null =
    scoped && (projectId ? { ...scoped, project: projectId } : scoped)
  const list = useInfiniteQuery({
    ...workItemsInfiniteQuery(30, filters ?? {}),
    enabled: !!filters,
  })
  const items = list.data?.pages.flatMap((p) => p.items) ?? []
  const sections: ListSection[] =
    groupMode === 'project'
      ? groupByProjects(items, projects.data ?? [], t('work.noProjectGroup'))
      : groupMode === 'status'
        ? groupByStatuses(items, statuses.data ?? [], (key) => t(statusMetaOf(key).labelKey))
        : []
  const flatItems =
    groupMode === 'time'
      ? [...items].sort((a, b) => b.last_activity_at.localeCompare(a.last_activity_at))
      : items
  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold">{t('nav.myWork')}</h1>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            aria-haspopup="dialog"
            onClick={() => setCreateOpen(true)}
          >
            <Plus className="size-4" aria-hidden="true" />
            {t('work.createWorkItem')}
          </Button>
          <div className="flex gap-1" role="tablist" aria-label={t('myWork.scopeAria')}>
            {SCOPES.map((s) => (
              <button
                key={s.key}
                type="button"
                role="tab"
                aria-selected={s.key === scope}
                className={
                  'cursor-default rounded-md px-3 py-1.5 text-sm outline-none ' +
                  (s.key === scope
                    ? 'bg-zinc-100 font-medium text-zinc-900 dark:bg-zinc-800 dark:text-zinc-100'
                    : 'text-zinc-500 hover:bg-zinc-100 hover:text-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-200')
                }
                onClick={() =>
                  void navigate({
                    search: {
                      scope: s.key === 'todo' ? undefined : s.key,
                      project: projectId,
                      group: groupMode === 'time' ? undefined : groupMode,
                    },
                    replace: true,
                  })
                }
              >
                {t(s.label)}
              </button>
            ))}
          </div>
        </div>
        <div className="flex items-center gap-2">
          {list.isSuccess && (
            <span className="text-xs text-zinc-400" aria-label={t('myWork.countAria')}>
              {t('work.totalN', { n: items.length })}
            </span>
          )}
          <select
            value={groupMode}
            aria-label={t('myWork.groupAria')}
            onChange={(e) => {
              const mode = e.target.value as GroupMode
              void navigate({
                search: {
                  scope: scope === 'todo' ? undefined : scope,
                  project: projectId,
                  group: mode === 'time' ? undefined : mode,
                },
                replace: true,
              })
            }}
            className="h-8 rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900"
          >
            {GROUP_MODES.map((m) => (
              <option key={m.key} value={m.key}>
                {t(m.label)}
              </option>
            ))}
          </select>
          {projects.data && projects.data.length > 0 && (
            <select
              value={projectId ?? ''}
              aria-label={t('myWork.projectAria')}
              onChange={(e) =>
                void navigate({
                  search: {
                    scope: scope === 'todo' ? undefined : scope,
                    project: e.target.value || undefined,
                    group: groupMode === 'time' ? undefined : groupMode,
                  },
                  replace: true,
                })
              }
              className="h-8 rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900"
            >
              <option value="">{t('work.allProjects')}</option>
              {projects.data.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          )}
        </div>
      </div>

      <CreateWorkItemDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={() => {
          if (scope === 'todo' || scope === 'done') {
            void navigate({ search: { scope: 'created' }, replace: true })
          }
        }}
      />

      {list.isPending && <PageLoading label={t('myWork.loading')} />}

      {list.isError && (
        <PageError
          title={t('common.error')}
          message={list.error instanceof Error ? list.error.message : undefined}
          onRetry={() => void list.refetch()}
        />
      )}

      {list.isSuccess && items.length === 0 && (
        <PageEmpty
          title={t(`myWork.empty_${scope}_title`)}
          description={t(`myWork.empty_${scope}_desc`)}
        />
      )}

      {items.length > 0 && (
        <>
          {groupMode === 'time' ? (
            <WorkItemList items={flatItems} />
          ) : (
            <div className="space-y-3">
              <WorkItemTableHeader />
              {sections.map((sec) => {
                const isCollapsed = collapsed[sec.key] ?? false
                return (
                  <div
                    key={sec.key}
                    className="rounded-lg border border-zinc-200 dark:border-zinc-800"
                  >
                    <button
                      type="button"
                      aria-expanded={!isCollapsed}
                      onClick={() => setCollapsed((c) => ({ ...c, [sec.key]: !isCollapsed }))}
                      className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-zinc-50 dark:hover:bg-zinc-900"
                    >
                      <span className="w-3 shrink-0 text-xs text-zinc-400" aria-hidden="true">
                        {isCollapsed ? '▸' : '▾'}
                      </span>
                      <span className="text-sm font-medium">{sec.label}</span>
                      <span className="text-xs text-zinc-400">({sec.items.length})</span>
                    </button>
                    {!isCollapsed && (
                      <ul className="divide-y divide-zinc-200 border-t border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
                        {sec.items.map((item) => (
                          <WorkItemRow key={item.id} item={item} />
                        ))}
                      </ul>
                    )}
                  </div>
                )
              })}
            </div>
          )}
          {list.hasNextPage && (
            <div className="flex justify-center">
              <button
                type="button"
                disabled={list.isFetchingNextPage}
                className="rounded-md border border-zinc-300 px-3 py-1.5 text-sm text-zinc-600 outline-none hover:bg-zinc-100 disabled:opacity-50 dark:border-zinc-700 dark:text-zinc-400 dark:hover:bg-zinc-800"
                onClick={() => void list.fetchNextPage()}
              >
                {list.isFetchingNextPage ? t('common.loading') : t('runLogs.loadMore')}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  )
}

import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { Plus, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import {
  listProjects,
  listStatuses,
  workItemsInfiniteQuery,
  type WorkItemListFilters,
} from '@/api/work'
import { membersQueryOptions } from '@/api/identity'
import { listAgents } from '@/api/orchestration'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { CreateWorkItemDialog } from '@/components/work-item/create-work-item-dialog'
import { statusMetaOf } from '@/components/work-item/meta'
import { WorkItemList } from '@/components/work-item/work-item-list'
import { useWorkspaceStore } from '@/stores/workspace'
const searchSchema = z.object({
  q: z.string().optional(),
  project: z.string().optional(),
  status: z.string().optional(),
  assignee: z.string().optional(),
})
export const Route = createFileRoute('/_app/work-items/')({
  validateSearch: (search) => {
    const parsed = searchSchema.safeParse(search)
    return parsed.success ? parsed.data : {}
  },
  component: WorkItemsPage,
})
const SELECT_CLASS =
  'h-8 rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900'
function WorkItemsPage() {
  const { t } = useTranslation()
  const navigate = Route.useNavigate()
  const search = Route.useSearch()
  const workspaceId = useWorkspaceStore((s) => s.currentId) ?? ''
  const [createOpen, setCreateOpen] = useState(false)
  const [qInput, setQInput] = useState(search.q ?? '')
  const [debouncedQ, setDebouncedQ] = useState(search.q ?? '')
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedQ(qInput), 300)
    return () => clearTimeout(timer)
  }, [qInput])
  useEffect(() => {
    if (debouncedQ !== (search.q ?? '')) {
      void navigate({
        search: { q: debouncedQ || undefined },
        replace: true,
      })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedQ])
  const projects = useQuery({ queryKey: ['projects'], queryFn: listProjects })
  const statuses = useQuery({ queryKey: ['work-item-statuses'], queryFn: listStatuses })
  const members = useQuery(membersQueryOptions(workspaceId))
  const agents = useQuery({ queryKey: ['agents'], queryFn: listAgents, staleTime: 60000 })
  const filters: WorkItemListFilters = {
    project: search.project,
    search: search.q,
    statuses: search.status ? [search.status] : undefined,
    assignee: search.assignee?.startsWith('user:')
      ? search.assignee.slice(5)
      : search.assignee?.startsWith('agent:')
        ? search.assignee.slice(6)
        : undefined,
  }
  const list = useInfiniteQuery(workItemsInfiniteQuery(30, filters))
  const patchSearch = (patch: Partial<z.infer<typeof searchSchema>>) =>
    void navigate({
      search: {
        q: search.q,
        project: search.project,
        status: search.status,
        assignee: search.assignee,
        ...patch,
      },
      replace: true,
    })
  const reset = () => {
    setQInput('')
    void navigate({ search: {}, replace: true })
  }
  const items = list.data?.pages.flatMap((p) => p.items) ?? []
  const hasFilter = !!(search.q || search.project || search.status || search.assignee)
  const statusName = (key: string) => {
    const def = statuses.data?.find((s) => s.key === key)
    return def ? (def.is_builtin ? t(statusMetaOf(def.key).labelKey) : def.name) : key
  }
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        <h1 className="text-lg font-semibold">{t('nav.allWork')}</h1>
        <Button size="sm" aria-haspopup="dialog" onClick={() => setCreateOpen(true)}>
          <Plus className="size-4" aria-hidden="true" />
          {t('work.newItem')}
        </Button>
      </div>

      <CreateWorkItemDialog open={createOpen} onOpenChange={setCreateOpen} />

      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={qInput}
          onChange={(e) => setQInput(e.target.value)}
          placeholder={t('work.searchPlaceholder')}
          aria-label={t('work.searchWorkItems')}
          className="h-8 w-56"
        />
        <select
          value={search.project ?? ''}
          aria-label={t('myWork.projectAria')}
          onChange={(e) => patchSearch({ project: e.target.value || undefined })}
          className={SELECT_CLASS}
        >
          <option value="">{t('work.allProjects')}</option>
          {projects.data?.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
        <select
          value={search.status ?? ''}
          aria-label={t('work.statusFilterAria')}
          onChange={(e) => patchSearch({ status: e.target.value || undefined })}
          className={SELECT_CLASS}
        >
          <option value="">{t('work.allStatuses')}</option>
          {statuses.data?.map((s) => (
            <option key={s.key} value={s.key}>
              {statusName(s.key)}
            </option>
          ))}
        </select>
        <select
          value={search.assignee ?? ''}
          aria-label={t('work.assigneeFilterAria')}
          onChange={(e) => patchSearch({ assignee: e.target.value || undefined })}
          className={SELECT_CLASS}
        >
          <option value="">{t('work.allAssignees')}</option>
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
        {hasFilter && (
          <Button variant="ghost" size="sm" onClick={reset}>
            <X className="size-3.5" aria-hidden="true" />
            {t('work.resetFilters')}
          </Button>
        )}
        {list.isSuccess && (
          <span className="ml-auto text-xs text-zinc-400" aria-label={t('myWork.countAria')}>
            {t('work.totalN', { n: items.length })}
          </span>
        )}
      </div>

      {list.isPending && <PageLoading label={t('work.loadingItems')} />}

      {list.isError && (
        <PageError
          title={t('work.itemsFailed')}
          message={list.error instanceof Error ? list.error.message : undefined}
          onRetry={() => void list.refetch()}
        />
      )}

      {list.isSuccess && items.length === 0 && (
        <PageEmpty
          title={hasFilter ? t('work.noMatch') : t('work.emptyTitle')}
          description={hasFilter ? t('work.noMatchDesc') : t('work.emptyDesc')}
        />
      )}

      {items.length > 0 && <WorkItemList items={items} />}

      {list.hasNextPage && (
        <div className="text-center">
          <Button
            variant="outline"
            size="sm"
            disabled={list.isFetchingNextPage}
            onClick={() => void list.fetchNextPage()}
          >
            {list.isFetchingNextPage ? t('common.loading') : t('runLogs.loadMore')}
          </Button>
        </div>
      )}
    </div>
  )
}

import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { Plus, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'
import { PageHeader } from '@ploykit/ui'
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
import { Select } from '@/components/ui/select'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { CreateWorkItemDialog } from '@/components/work-item/create-work-item-dialog'
import { statusMetaOf } from '@/components/work-item/meta'
import { StarterChecklist } from '@/components/work-item/starter-checklist'
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
  const agents = useQuery({ queryKey: ['agents'], queryFn: () => listAgents(), staleTime: 60000 })
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
      <PageHeader
        title={t('nav.allWork')}
        actions={
          <Button size="sm" aria-haspopup="dialog" onClick={() => setCreateOpen(true)}>
            <Plus className="size-4" aria-hidden="true" />
            {t('work.newItem')}
          </Button>
        }
      />

      <CreateWorkItemDialog open={createOpen} onOpenChange={setCreateOpen} />

      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={qInput}
          onChange={(e) => setQInput(e.target.value)}
          placeholder={t('work.searchPlaceholder')}
          aria-label={t('work.searchWorkItems')}
          className="h-8 w-56"
        />
        <Select
          value={search.project ?? ''}
          aria-label={t('myWork.projectAria')}
          className="w-auto"
          onChange={(e) => patchSearch({ project: e.target.value || undefined })}
        >
          <option value="">{t('work.allProjects')}</option>
          {projects.data?.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </Select>
        <Select
          value={search.status ?? ''}
          aria-label={t('work.statusFilterAria')}
          className="w-auto"
          onChange={(e) => patchSearch({ status: e.target.value || undefined })}
        >
          <option value="">{t('work.allStatuses')}</option>
          {statuses.data?.map((s) => (
            <option key={s.key} value={s.key}>
              {statusName(s.key)}
            </option>
          ))}
        </Select>
        <Select
          value={search.assignee ?? ''}
          aria-label={t('work.assigneeFilterAria')}
          className="w-auto"
          onChange={(e) => patchSearch({ assignee: e.target.value || undefined })}
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
        </Select>
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

      {list.isSuccess && items.length === 0 && !hasFilter && <StarterChecklist />}

      {list.isSuccess && items.length === 0 && hasFilter && (
        /* 「或清空筛选」复述重置按钮，删（ui-sweep #6：work.noMatchDesc 弃用） */
        <PageEmpty title={t('work.noMatch')} />
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

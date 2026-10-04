import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { Tag } from 'lucide-react'
import { z } from 'zod'
import { getWorkItem } from '@/api/work'
import { PageError, PageLoading } from '@/components/ui/states'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { DescriptionEditor } from '@/components/work-item/description-editor'
import { LabelsBlock } from '@/components/work-item/label-chips'
import { InfoPanel } from '@/components/work-item/info-panel'
import { MoreMenu } from '@/components/work-item/more-menu'
import { StatusToggle } from '@/components/work-item/property-rows'
import { CommentThread } from '@/components/work-item/comment-thread'
import { SubWorkItemsList } from '@/components/work-item/sub-work-items'
import { TaskPanel } from '@/components/work-item/task-panel'
import { TimelineList } from '@/components/work-item/timeline-list'
import { useTranslation } from 'react-i18next'
const searchSchema = z.object({
  tab: z.enum(['detail', 'sub', 'tasks', 'timeline']).optional(),
  task: z.string().optional(),
})
type DetailTab = z.infer<typeof searchSchema>['tab']
export const Route = createFileRoute('/_app/work-items/$id')({
  validateSearch: (search) => {
    const parsed = searchSchema.safeParse(search)
    return parsed.success ? parsed.data : {}
  },
  component: WorkItemDetailPage,
})
function WorkItemDetailPage() {
  const { t } = useTranslation()
  const { id } = Route.useParams()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const item = useQuery({
    queryKey: ['work-item', id],
    queryFn: () => getWorkItem(id),
  })
  if (item.isPending) return <PageLoading label={t('work.loadingDetail')} />
  if (item.isError) {
    return (
      <PageError
        title={t('work.notFound')}
        message={item.error instanceof Error ? item.error.message : undefined}
      />
    )
  }
  const wi = item.data
  const patchSearch = (patch: { tab?: DetailTab; task?: string }) =>
    void navigate({ search: (prev) => ({ ...prev, ...patch }), replace: true })
  const expandedTaskId = search.task ?? null
  const toggleTask = (taskId: string) =>
    patchSearch({ task: expandedTaskId === taskId ? undefined : taskId, tab: 'tasks' })
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b border-zinc-200 px-1 pb-3 dark:border-zinc-800">
        <StatusToggle item={wi} />
        <h1 className="min-w-0 truncate text-lg font-semibold">{wi.title}</h1>
        <span className="shrink-0 font-mono text-sm text-zinc-400">#{wi.number}</span>
        <div className="ml-auto shrink-0">
          <MoreMenu item={wi} />
        </div>
      </div>

      <div className="flex min-h-0 flex-1 gap-6 pt-4">
        <div className="min-w-0 flex-1 overflow-auto">
          <Tabs
            value={search.tab ?? 'detail'}
            onValueChange={(v) => patchSearch({ tab: String(v) as DetailTab })}
            className="shrink-0"
          >
            <TabsList variant="line" className="pb-px">
              {(
                [
                  ['detail', 'work.tabDetail'],
                  ['sub', 'work.tabSub'],
                  ['tasks', 'work.tabTasks'],
                  ['timeline', 'work.tabTimeline'],
                ] as const
              ).map(([key, label]) => (
                <TabsTrigger key={key} value={key} className="px-3">
                  {t(label)}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>

          <div className="pt-4">
            {(!search.tab || search.tab === 'detail') && <DescriptionEditor item={wi} />}
            {search.tab === 'sub' && <SubWorkItemsList workItemId={id} />}
            {search.tab === 'tasks' && (
              <TaskPanel
                workItemId={id}
                expandedTaskId={expandedTaskId}
                onToggleTask={toggleTask}
              />
            )}
            {search.tab === 'timeline' && <TimelineList workItemId={id} />}
          </div>

          <div className="mt-5 flex items-center gap-2">
            <span className="flex shrink-0 items-center gap-1 text-xs text-zinc-400">
              <Tag className="size-3" aria-hidden="true" />
              {t('labels.title')}
            </span>
            <LabelsBlock workItemId={id} />
          </div>

          <div className="mt-6 border-t border-zinc-200 pt-4 dark:border-zinc-800">
            <h2 className="mb-3 text-sm font-semibold">{t('comment.title')}</h2>
            <CommentThread workItemId={id} />
          </div>
        </div>

        <InfoPanel item={wi} />
      </div>
    </div>
  )
}

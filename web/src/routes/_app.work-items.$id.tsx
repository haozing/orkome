import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { Tag } from 'lucide-react'
import { z } from 'zod'
import { getWorkItem } from '@/api/work'
import { PageError, PageLoading } from '@/components/ui/states'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { PackSlotFrame } from '@/components/pack-slot-frame'
import { usePackSlots } from '@/components/use-pack-slots'
import { DescriptionEditor } from '@/components/work-item/description-editor'
import { OutcomeBlock } from '@/components/work-item/outcome-block'
import { AgentReportsTab } from '@/components/work-item/agent-reports-tab'
import { LabelsBlock } from '@/components/work-item/label-chips'
import { InfoPanel } from '@/components/work-item/info-panel'
import { MoreMenu } from '@/components/work-item/more-menu'
import { StatusToggle } from '@/components/work-item/property-rows'
import { CommentThread } from '@/components/work-item/comment-thread'
import { SubWorkItemsList } from '@/components/work-item/sub-work-items'
import { TaskPanel } from '@/components/work-item/task-panel'
import { BatchPanel } from '@/components/work-item/batch-panel'
import { TimelineList } from '@/components/work-item/timeline-list'
import { WorkflowPanel } from '@/components/workflow/workflow-panel'
import { useTranslation } from 'react-i18next'

// 动态 slot tab（C2/F3）：静态枚举 + pack:<pack> 前缀动态值（前端设计 §1.3 审计修订）。
// validateSearch 失败即丢弃整个 search——pack:* 值必须被 schema 显式放行，
// 否则用户停在 slot tab 时任何 search 更新都会把 tab 洗掉。
const STATIC_TABS = ['detail', 'reports', 'sub', 'tasks', 'batches', 'timeline'] as const
const searchSchema = z.object({
  tab: z.enum(STATIC_TABS).or(z.string().regex(/^pack:/)).optional(),
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
export function WorkItemDetailPage() {
  const { t } = useTranslation()
  const { id } = Route.useParams()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const item = useQuery({
    queryKey: ['work-item', id],
    queryFn: () => getWorkItem(id),
  })
  // 动态 slot tab 数据源：GET /api/ui-entries 的 slots 随行（无 slots 的 pack 不出 tab）。
  const slots = usePackSlots(id)
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
  const slotTabs = slots.data ?? []
  const activeSlot = slotTabs.find((s) => s.key === search.tab)
  // 内容区兜底：tab 值不属于任何已知 tab（如 pack 已卸载后 URL 残留 pack:ghost）
  // 时按 detail 呈现——与 zod 丢弃 garbage 后回 detail 的语义一致。
  const knownStatic = STATIC_TABS.includes(search.tab as (typeof STATIC_TABS)[number])
  const showDetail = !search.tab || search.tab === 'detail' || (!knownStatic && !activeSlot)
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
                  ['reports', 'work.tabReports'],
                  ['sub', 'work.tabSub'],
                  ['tasks', 'work.tabTasks'],
                  ['batches', 'work.tabBatches'],
                  ['timeline', 'work.tabTimeline'],
                ] as const
              ).map(([key, label]) => (
                <TabsTrigger key={key} value={key} className="px-3">
                  {t(label)}
                </TabsTrigger>
              ))}
              {slotTabs.map((s) => (
                <TabsTrigger key={s.key} value={s.key} className="px-3">
                  {s.slot.label}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>

          <div className="pt-4">
            {showDetail && <DescriptionEditor item={wi} />}
            {showDetail && <OutcomeBlock item={wi} />}
            {search.tab === 'reports' && <AgentReportsTab workItemId={id} />}
            {search.tab === 'sub' && <SubWorkItemsList workItemId={id} />}
            {search.tab === 'tasks' && (
              <TaskPanel
                workItemId={id}
                expandedTaskId={expandedTaskId}
                onToggleTask={toggleTask}
              />
            )}
            {search.tab === 'batches' && <BatchPanel workItemId={id} />}
            {search.tab === 'timeline' && <TimelineList workItemId={id} />}
            {activeSlot && (
              <PackSlotFrame pack={activeSlot.pack} slot={activeSlot.slot} workItemId={id} />
            )}
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

        {/* 属性区在上（最高频编辑入口），流程光标卡在其下（2026-10-14 裁决：
            流程是"动态化的状态+负责人"，常驻侧栏但不抢占属性首位） */}
        <InfoPanel item={wi} />
        <WorkflowPanel workItemId={id} projectId={wi.project_id ?? ''} />
      </div>
    </div>
  )
}

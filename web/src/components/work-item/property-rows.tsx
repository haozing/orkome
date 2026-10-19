import { useQuery } from '@tanstack/react-query'
import { Check, ChevronDown, Search } from 'lucide-react'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  getWorkItem,
  listProjects,
  listStatuses,
  listWorkItems,
  type WorkItem,
  type WorkItemStatus,
} from '@/api/work'
import { listAgents } from '@/api/orchestration'
import { membersQueryOptions } from '@/api/identity'
import { useFieldUpdate } from '@/components/work-item/use-field-update'
import { useWorkspaceStore } from '@/stores/workspace'
import { PRIORITY_META, STATUS_META, statusMetaOf, TONE_DOT_CLASS } from '@/components/work-item/meta'
import { Badge } from '@/components/ui/badge'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
const ROW_CLASS =
  'flex w-full cursor-default items-center justify-between gap-2 rounded-md px-2 py-1.5 text-sm outline-none hover:bg-accent data-[popup-open]:bg-accent'
// Value side of a property row: shrinks below content size so long labels
// (raw agent IDs, long parent titles) truncate instead of overflowing the
// panel; the row label stays fixed.
const VALUE_CLASS = 'flex min-w-0 items-center gap-1'
const VALUE_TEXT_CLASS = 'min-w-0 truncate'
function Checkmark({ on }: { on: boolean }) {
  return (
    <Check
      className={'size-3.5 shrink-0 ' + (on ? 'opacity-100' : 'opacity-0')}
      aria-hidden="true"
    />
  )
}
export function AssigneeRow({ item }: { item: WorkItem }) {
  const { t } = useTranslation()
  const workspaceId = useWorkspaceStore((s) => s.currentId) ?? ''
  const members = useQuery(membersQueryOptions(workspaceId))
  const agents = useQuery({ queryKey: ['agents'], queryFn: () => listAgents(), staleTime: 60000 })
  const update = useFieldUpdate(item)
  const resolveName = (userId?: string) => {
    if (!userId) return ''
    const m = members.data?.find((x) => x.user_id === userId)
    return m ? m.display_name || m.email : userId.slice(0, 8) + '…'
  }
  const currentLabel =
    item.assignee_type === 'user'
      ? resolveName(item.assignee_id) || t('work.unassigned')
      : item.assignee_type === 'agent'
        ? (agents.data?.find((a) => a.id === item.assignee_id)?.name ??
          `Agent ${item.assignee_id?.slice(0, 8) ?? ''}…`)
        : t('work.unassigned')
  const pick = (type: 'none' | 'user' | 'agent', id = '') => {
    if (item.assignee_type === type && (item.assignee_id ?? '') === id) return
    update.mutate({ assignee_type: type, assignee_id: id })
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className={ROW_CLASS}>
        <span className="shrink-0 text-muted-foreground">{t('work.colAssignee')}</span>
        <span className={VALUE_CLASS}>
          <span className={VALUE_TEXT_CLASS + (item.assignee_id ? '' : ' text-muted-foreground')}>
            {currentLabel}
          </span>
          <ChevronDown className="size-3.5 shrink-0 text-zinc-400" aria-hidden="true" />
        </span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-40">
        <DropdownMenuItem onClick={() => pick('none')}>
          <Checkmark on={item.assignee_type === 'none'} />
          {t('work.unassigned')}
        </DropdownMenuItem>

        {(members.data?.length ?? 0) > 0 && (
          <DropdownMenuGroup>
            <DropdownMenuLabel>{t('agents.member')}</DropdownMenuLabel>
            {members.data!.map((m) => (
              <DropdownMenuItem key={m.user_id} onClick={() => pick('user', m.user_id)}>
                <Checkmark on={item.assignee_type === 'user' && item.assignee_id === m.user_id} />
                {m.display_name || m.email}
              </DropdownMenuItem>
            ))}
          </DropdownMenuGroup>
        )}

        {(agents.data?.filter((a) => !a.archived).length ?? 0) > 0 && (
          <DropdownMenuGroup>
            <DropdownMenuLabel>{t('nav.agents')}</DropdownMenuLabel>
            {agents
              .data!.filter((a) => !a.archived)
              .map((a) => (
                <DropdownMenuItem key={a.id} onClick={() => pick('agent', a.id)}>
                  <Checkmark on={item.assignee_type === 'agent' && item.assignee_id === a.id} />
                  {a.name}
                </DropdownMenuItem>
              ))}
          </DropdownMenuGroup>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
function useStatusCatalog() {
  return useQuery({
    queryKey: ['work-item-statuses'],
    queryFn: listStatuses,
    staleTime: 5 * 60000,
  })
}
function useBuiltinStatusLabel() {
  const { t } = useTranslation()
  return (item: WorkItem, fallback: string | undefined) =>
    (STATUS_META[item.status] ? t(STATUS_META[item.status]!.labelKey) : undefined) ??
    fallback ??
    item.status
}
// 项目行：可换项目（PATCH project_id；挂上有流程的项目时 autostart 由后端
// 幂等兜底，前端只管改字段）。无项目选项 = 清空归属（脱离流程是服务端语义）。
export function ProjectRow({ item }: { item: WorkItem }) {
  const { t } = useTranslation()
  const update = useFieldUpdate(item)
  const projects = useQuery({ queryKey: ['projects'], queryFn: listProjects, staleTime: 60000 })
  const current = projects.data?.find((p) => p.id === item.project_id)
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className={ROW_CLASS}>
        <span className="shrink-0 text-muted-foreground">{t('work.projectRow')}</span>
        <span className={VALUE_CLASS}>
          <span
            className="size-2 shrink-0 rounded-[3px] bg-indigo-500"
            aria-hidden="true"
            data-project-dot
          />
          <span className={VALUE_TEXT_CLASS}>
            {current?.name ?? (item.project_id ? item.project_id.slice(0, 8) + '…' : t('work.noProject'))}
          </span>
          <ChevronDown className="size-3.5 shrink-0 text-zinc-400" aria-hidden="true" />
        </span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-40">
        <DropdownMenuItem
          onClick={() => {
            if (item.project_id) update.mutate({ project_id: '' })
          }}
        >
          <Checkmark on={!item.project_id} />
          {t('work.noProject')}
        </DropdownMenuItem>
        {(projects.data ?? []).map((p) => (
          <DropdownMenuItem
            key={p.id}
            onClick={() => {
              if (p.id !== item.project_id) update.mutate({ project_id: p.id })
            }}
          >
            <Checkmark on={p.id === item.project_id} />
            {p.name}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function StatusRow({ item }: { item: WorkItem }) {
  const { t } = useTranslation()
  const catalog = useStatusCatalog()
  const update = useFieldUpdate(item)
  const builtinLabel = useBuiltinStatusLabel()
  const current = catalog.data?.find((s) => s.key === item.status)
  const label = current?.is_builtin
    ? builtinLabel(item, current.name)
    : (current?.name ?? item.status)
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className={ROW_CLASS}>
        <span className="shrink-0 text-muted-foreground">{t('work.colStatus')}</span>
        <span className={VALUE_CLASS}>
          <BadgeDot status={item.status} />
          <span className={VALUE_TEXT_CLASS}>{label}</span>
          <ChevronDown className="size-3.5 shrink-0 text-zinc-400" aria-hidden="true" />
        </span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-40">
        {(catalog.data ?? []).map((s: WorkItemStatus) => (
          <DropdownMenuItem
            key={s.key}
            onClick={() => {
              if (s.key !== item.status) update.mutate({ status: s.key })
            }}
          >
            <Checkmark on={s.key === item.status} />
            {s.name}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
export function StatusToggle({ item }: { item: WorkItem }) {
  const catalog = useStatusCatalog()
  const update = useFieldUpdate(item)
  const builtinLabel = useBuiltinStatusLabel()
  const current = catalog.data?.find((s) => s.key === item.status)
  const label = current?.is_builtin
    ? builtinLabel(item, current.name)
    : (current?.name ?? item.status)
  return (
    <DropdownMenu>
      {/* 触发器以 kit Badge 为底（ui-sweep #11 状态 chip 三轨归一：StatusToggle
          手写 pill / 详情行 BadgeDot / 列表 Badge 三态共用一套状态视觉） */}
      <DropdownMenuTrigger
        render={<Badge variant="secondary" className="gap-1 hover:bg-secondary/80" />}
      >
        <BadgeDot status={item.status} />
        {label}
        <ChevronDown className="size-3 text-zinc-400" aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-40">
        {(catalog.data ?? []).map((s: WorkItemStatus) => (
          <DropdownMenuItem
            key={s.key}
            onClick={() => {
              if (s.key !== item.status) update.mutate({ status: s.key })
            }}
          >
            <Checkmark on={s.key === item.status} />
            {s.name}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
export function PriorityRow({ item }: { item: WorkItem }) {
  const { t } = useTranslation()
  const update = useFieldUpdate(item)
  const meta = PRIORITY_META[item.priority] ?? PRIORITY_META[0]!
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className={ROW_CLASS}>
        <span className="shrink-0 text-muted-foreground">{t('work.colPriority')}</span>
        <span className={VALUE_CLASS}>
          <span className={VALUE_TEXT_CLASS + ' ' + meta.className}>{t(meta.labelKey)}</span>
          <ChevronDown className="size-3.5 shrink-0 text-zinc-400" aria-hidden="true" />
        </span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-40">
        {Object.entries(PRIORITY_META).map(([value, m]) => (
          <DropdownMenuItem
            key={value}
            onClick={() => {
              const n = Number(value)
              if (n !== item.priority) update.mutate({ priority: n })
            }}
          >
            <Checkmark on={Number(value) === item.priority} />
            <span className={m.className}>{t(m.labelKey)}</span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
export function DateRow({
  item,
  field,
  label,
}: {
  item: WorkItem
  field: 'start_date' | 'due_date'
  label: string
}) {
  const update = useFieldUpdate(item)
  const value = item[field] ?? ''
  return (
    <div className={ROW_CLASS}>
      <span className="text-muted-foreground">{label}</span>
      <input
        type="date"
        value={value}
        aria-label={label}
        onChange={(e) => {
          const v = e.target.value
          if (v !== value) update.mutate({ [field]: v })
        }}
        className="text-foreground cursor-default rounded bg-transparent text-right text-sm outline-none [&::-webkit-calendar-picker-indicator]:cursor-pointer dark:[&::-webkit-calendar-picker-indicator]:invert"
      />
    </div>
  )
}
const PARENT_SEARCH_DEBOUNCE_MS = 300
export function ParentRow({ item }: { item: WorkItem }) {
  const { t } = useTranslation()
  const update = useFieldUpdate(item)
  const [query, setQuery] = useState('')
  const [candidates, setCandidates] = useState<WorkItem[] | null>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const doSearch = (q: string) => {
    listWorkItems({ limit: 50 })
      .then((page) => {
        const pool = page.items.filter((wi) => wi.id !== item.id)
        const needle = q.trim().toLowerCase()
        const filtered = needle
          ? pool.filter(
              (wi) => wi.title.toLowerCase().includes(needle) || String(wi.number) === needle,
            )
          : pool
        setCandidates(filtered.slice(0, 8))
      })
      .catch(() => setCandidates([]))
  }
  const onQueryChange = (v: string) => {
    setQuery(v)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => doSearch(v), PARENT_SEARCH_DEBOUNCE_MS)
  }
  const parent = useQuery({
    queryKey: ['work-item', item.parent_work_item_id],
    queryFn: () => getWorkItem(item.parent_work_item_id!),
    enabled: !!item.parent_work_item_id,
    staleTime: 5 * 60000,
  })
  const parentLabel = item.parent_work_item_id
    ? parent.data
      ? `#${parent.data.number} ${parent.data.title}`
      : item.parent_work_item_id.slice(0, 8) + '…'
    : t('common.none')
  const pick = (id: string) => {
    if ((item.parent_work_item_id ?? '') === id) return
    update.mutate({ parent_work_item_id: id })
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className={ROW_CLASS}>
        <span className="shrink-0 text-muted-foreground">{t('work.parentItem')}</span>
        <span className={VALUE_CLASS}>
          <span
            className={VALUE_TEXT_CLASS + (item.parent_work_item_id ? '' : ' text-muted-foreground')}
          >
            {parentLabel}
          </span>
          <ChevronDown className="size-3.5 shrink-0 text-zinc-400" aria-hidden="true" />
        </span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-56">
        <div className="flex items-center gap-1.5 px-2 py-1">
          <Search className="size-3.5 shrink-0 text-zinc-400" aria-hidden="true" />
          <input
            value={query}
            onChange={(e) => onQueryChange(e.target.value)}
            placeholder={t('work.searchWorkItems')}
            aria-label={t('work.searchParent')}
            onKeyDown={(e) => e.stopPropagation()}
            className="w-full rounded bg-transparent py-1 text-sm outline-none"
          />
        </div>
        <DropdownMenuItem onClick={() => update.mutate({ parent_work_item_id: '' })}>
          <Checkmark on={!item.parent_work_item_id} />
          {t('common.none')}
        </DropdownMenuItem>
        {(candidates ?? []).map((wi) => (
          <DropdownMenuItem key={wi.id} onClick={() => pick(wi.id)}>
            <Checkmark on={item.parent_work_item_id === wi.id} />
            <span className="font-mono text-xs text-zinc-400">#{wi.number}</span>
            <span className="min-w-0 flex-1 truncate">{wi.title}</span>
          </DropdownMenuItem>
        ))}
        {candidates !== null && candidates.length === 0 && (
          <p className="text-muted-foreground px-2 py-1.5 text-xs">{t('work.noMatch')}</p>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
export function BadgeDot({ status }: { status: string }) {
  // 圆点底色走 meta.ts 的 TONE_DOT_CLASS（tone 映射唯一出处，ui-sweep #11）。
  return (
    <span
      className={'size-2 rounded-full ' + TONE_DOT_CLASS[statusMetaOf(status).tone]}
      aria-hidden="true"
    />
  )
}

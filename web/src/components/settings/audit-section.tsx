import { useQuery } from '@tanstack/react-query'
import { isApiError } from '@ploykit/client'
import {
  DataTable,
  FormField,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
  type Column,
} from '@ploykit/ui'
import { ChevronDown, ChevronRight, Download } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { auditCsvDownload, listAudit, type AuditEntry, type AuditFilters } from '@/api/audit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { PageEmpty, PageError } from '@/components/ui/states'
import { INLINE_EMPTY_CLS, INLINE_ERROR_CLS } from '@/components/settings/shared'

// 审计日志区块（settings 域设计 §3.7）：过滤栏 + 表格（metadata 点击展开）+
// 分页（limit=50 按 offset）+「导出 CSV」。权限 audit:read 仅 owner/admin——
// member 调用 403，query catch 后整块显权限空态（PageEmpty），不是整页崩。
// 视觉（ui-sweep）：区块自身无边框（壳层 divide-y 分隔，security 范式）；
// 表格用 kit DataTable（renderExpanded 展开行 + pagination，text-xs 紧凑密度）；
// 截断单元格的原生 title 换 kit Tooltip。

export interface AuditSectionProps {
  workspaceId: string
}

const PAGE_SIZE = 50

/** date 输入值（YYYY-MM-DD）→ 当日起止的 RFC3339（服务端要求 RFC3339 解析）。 */
function dayToRfc3339(day: string, endOfDay: boolean): string | undefined {
  if (!day) return undefined
  const d = new Date(`${day}T00:00:00`)
  if (Number.isNaN(d.getTime())) return undefined
  if (endOfDay) d.setHours(23, 59, 59, 999)
  return d.toISOString()
}

export function AuditSection({ workspaceId }: AuditSectionProps) {
  const { t, i18n } = useTranslation()
  const [action, setAction] = useState('')
  const [resourceType, setResourceType] = useState('')
  const [fromDay, setFromDay] = useState('')
  const [toDay, setToDay] = useState('')
  // 「应用过滤」前草稿不进 query key，避免每敲一个字符发一次请求。
  const [applied, setApplied] = useState<AuditFilters>({})
  const [offset, setOffset] = useState(0)
  // metadata 展开行：记录展开的条目 id（一次一条，简单直接）。
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [exporting, setExporting] = useState(false)

  const filters: AuditFilters = {
    ...applied,
    from: dayToRfc3339(fromDay, false),
    to: dayToRfc3339(toDay, true),
  }
  const audit = useQuery({
    queryKey: ['audit', workspaceId, applied, fromDay, toDay, offset] as const,
    queryFn: () => listAudit(workspaceId, filters, PAGE_SIZE, offset),
    retry: false,
  })
  // 403（audit:read 缺失，member 访问）→ 权限空态；其他错误 → 行内错误态。
  const forbidden = audit.isError && isApiError(audit.error) && audit.error.status === 403

  const applyFilters = () => {
    setApplied({
      action: action.trim(),
      resource_type: resourceType.trim(),
    })
    setOffset(0)
    setExpandedId(null)
  }
  const fmtTime = (iso: string): string =>
    new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })
  const total = audit.data?.total ?? 0
  const items = audit.data?.items ?? []

  // 表格密度对齐 orkome text-xs 紧凑风：列级 className 覆盖 DataTable 的
  // px-4 py-3（kit cn 走 tailwind-merge，py-1.5/pr-3 生效）。
  const columns: Column<AuditEntry>[] = [
    {
      key: 'time',
      header: t('settingsWs.auditColTime'),
      className: 'py-1.5 pr-3 whitespace-nowrap text-xs text-zinc-500 dark:text-zinc-400',
      render: (entry) => fmtTime(entry.created_at),
    },
    {
      key: 'actor',
      header: t('settingsWs.auditColActor'),
      className: 'py-1.5 pr-3 text-xs',
      render: (entry) => {
        const email = entry.actor_snapshot?.email
        const text = email || entry.actor_id || '–'
        return email ? (
          <Tooltip>
            <TooltipTrigger render={<span className="block max-w-48 truncate" />}>
              {text}
            </TooltipTrigger>
            <TooltipContent>{email}</TooltipContent>
          </Tooltip>
        ) : (
          text
        )
      },
    },
    {
      key: 'actor_type',
      header: t('settingsWs.auditColActorType'),
      className: 'py-1.5 pr-3 text-xs',
      render: (entry) => t(`settingsWs.actorType.${entry.actor_type}`),
    },
    {
      key: 'action',
      header: t('settingsWs.auditColAction'),
      className: 'py-1.5 pr-3 font-mono text-xs',
      render: (entry) => entry.action,
    },
    {
      key: 'resource',
      header: t('settingsWs.auditColResource'),
      className: 'py-1.5 pr-3 text-xs',
      render: (entry) => (
        <>
          <span className="font-mono">{entry.resource_type}</span>
          {entry.resource_id && (
            <Tooltip>
              <TooltipTrigger
                render={
                  <span className="ml-1 max-w-32 truncate align-bottom font-mono text-zinc-400" />
                }
              >
                {entry.resource_id}
              </TooltipTrigger>
              <TooltipContent>{entry.resource_id}</TooltipContent>
            </Tooltip>
          )}
        </>
      ),
    },
    {
      key: 'metadata',
      header: '',
      className: 'py-1.5 text-right text-xs',
      render: (entry) => {
        const hasMetadata = entry.metadata && Object.keys(entry.metadata).length > 0
        if (!hasMetadata) return null
        const expanded = expandedId === entry.id
        return (
          <Button
            size="xs"
            variant="ghost"
            aria-expanded={expanded}
            aria-label={t('settingsWs.auditMetadataAria', { action: entry.action })}
            data-testid="ws-audit-metadata-toggle"
            onClick={() => setExpandedId((cur) => (cur === entry.id ? null : entry.id))}
          >
            {expanded ? (
              <ChevronDown className="size-3" aria-hidden="true" />
            ) : (
              <ChevronRight className="size-3" aria-hidden="true" />
            )}
            {t('settingsWs.auditColMetadata')}
          </Button>
        )
      },
    },
  ]

  return (
    <section id="audit" className="space-y-3" aria-label={t('settingsWs.auditTitle')} data-testid="ws-audit-section">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">{t('settingsWs.auditTitle')}</h2>
        <Button
          size="sm"
          variant="outline"
          data-testid="ws-audit-export"
          disabled={exporting}
          onClick={() => {
            setExporting(true)
            auditCsvDownload(workspaceId, filters)
              .catch(() => {})
              .finally(() => setExporting(false))
          }}
        >
          <Download className="size-3.5" aria-hidden="true" />
          {t('settingsWs.auditExport')}
        </Button>
      </div>

      {forbidden ? (
        <PageEmpty
          title={t('settingsWs.auditForbiddenTitle')}
          description={t('settingsWs.auditForbiddenDesc')}
          className="pt-4"
        />
      ) : (
        <>
          {/* 过滤栏：kit FormField 接管 label/控件（ui-sweep 裁决④） */}
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              applyFilters()
            }}
          >
            <div className="w-40">
              <FormField label={t('settingsWs.auditActionLabel')} htmlFor="ws-audit-action">
                <Input
                  id="ws-audit-action"
                  value={action}
                  onChange={(e) => setAction(e.target.value)}
                  placeholder="work_item.created"
                  data-testid="ws-audit-action"
                />
              </FormField>
            </div>
            <div className="w-40">
              <FormField label={t('settingsWs.auditResourceLabel')} htmlFor="ws-audit-rtype">
                <Input
                  id="ws-audit-rtype"
                  value={resourceType}
                  onChange={(e) => setResourceType(e.target.value)}
                  placeholder="work_item"
                  data-testid="ws-audit-rtype"
                />
              </FormField>
            </div>
            <div className="w-36">
              <FormField label={t('settingsWs.auditFromLabel')} htmlFor="ws-audit-from">
                <Input
                  id="ws-audit-from"
                  type="date"
                  value={fromDay}
                  onChange={(e) => {
                    setFromDay(e.target.value)
                    setOffset(0)
                  }}
                  data-testid="ws-audit-from"
                />
              </FormField>
            </div>
            <div className="w-36">
              <FormField label={t('settingsWs.auditToLabel')} htmlFor="ws-audit-to">
                <Input
                  id="ws-audit-to"
                  type="date"
                  value={toDay}
                  onChange={(e) => {
                    setToDay(e.target.value)
                    setOffset(0)
                  }}
                  data-testid="ws-audit-to"
                />
              </FormField>
            </div>
            <Button type="submit" size="default" data-testid="ws-audit-apply">
              {t('settingsWs.auditApply')}
            </Button>
          </form>

          {audit.isError && !forbidden ? (
            <PageError
              title={t('settingsWs.loadFailed')}
              message={isApiError(audit.error) ? audit.error.message : undefined}
              onRetry={() => void audit.refetch()}
              className={INLINE_ERROR_CLS}
            />
          ) : (
            <DataTable
              columns={columns}
              rows={items}
              rowKey={(entry) => entry.id}
              loading={audit.isPending}
              empty={
                <PageEmpty
                  title={t('settingsWs.auditEmpty')}
                  className={INLINE_EMPTY_CLS}
                />
              }
              renderExpanded={(entry) => {
                const hasMetadata = entry.metadata && Object.keys(entry.metadata).length > 0
                if (expandedId !== entry.id || !hasMetadata) return null
                return (
                  <pre
                    className="overflow-x-auto rounded-md bg-zinc-50 p-2 font-mono text-xs leading-relaxed dark:bg-zinc-900"
                    data-testid="ws-audit-metadata"
                  >
                    {JSON.stringify(entry.metadata, null, 2)}
                  </pre>
                )
              }}
              pagination={{
                page: Math.floor(offset / PAGE_SIZE) + 1,
                pageSize: PAGE_SIZE,
                total,
                onPageChange: (page) => {
                  setOffset((page - 1) * PAGE_SIZE)
                  setExpandedId(null)
                },
              }}
            />
          )}
        </>
      )}
    </section>
  )
}

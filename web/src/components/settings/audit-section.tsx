import { useQuery } from '@tanstack/react-query'
import { isApiError } from '@ploykit/client'
import { ChevronDown, ChevronRight, Download } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { auditCsvDownload, listAudit, type AuditEntry, type AuditFilters } from '@/api/audit'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { PageEmpty } from '@/components/ui/states'

// 审计日志区块（settings 域设计 §3.7）：过滤栏 + 表格（metadata 点击展开）+
// 分页（limit=50 按 offset）+「导出 CSV」。权限 audit:read 仅 owner/admin——
// member 调用 403，query catch 后整块显权限空态（PageEmpty），不是整页崩。
// 视觉：过滤栏 flex-wrap items-end，label/宽度统一 token；表格行分隔回 zinc-100。

export interface AuditSectionProps {
  workspaceId: string
}

const PAGE_SIZE = 50

const LABEL_CLS = 'mb-1 block text-xs text-zinc-500 dark:text-zinc-400'

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
  // 403（audit:read 缺失，member 访问）→ 权限空态；其他错误 → 普通错误行。
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
  const hasPrev = offset > 0
  const hasNext = offset + PAGE_SIZE < total

  return (
    <section
      id="audit"
      className="space-y-3 rounded-lg border border-zinc-200 p-4 dark:border-zinc-800"
      aria-label={t('settingsWs.auditTitle')}
      data-testid="ws-audit-section"
    >
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
          {/* 过滤栏：label 统一 mb-1 text-xs zinc-500；宽度 action/rtype w-40、date w-36 */}
          <form
            className="flex flex-wrap items-end gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              applyFilters()
            }}
          >
            <div>
              <label htmlFor="ws-audit-action" className={LABEL_CLS}>
                {t('settingsWs.auditActionLabel')}
              </label>
              <Input
                id="ws-audit-action"
                value={action}
                onChange={(e) => setAction(e.target.value)}
                placeholder="work_item.created"
                className="w-40"
                data-testid="ws-audit-action"
              />
            </div>
            <div>
              <label htmlFor="ws-audit-rtype" className={LABEL_CLS}>
                {t('settingsWs.auditResourceLabel')}
              </label>
              <Input
                id="ws-audit-rtype"
                value={resourceType}
                onChange={(e) => setResourceType(e.target.value)}
                placeholder="work_item"
                className="w-40"
                data-testid="ws-audit-rtype"
              />
            </div>
            <div>
              <label htmlFor="ws-audit-from" className={LABEL_CLS}>
                {t('settingsWs.auditFromLabel')}
              </label>
              <Input
                id="ws-audit-from"
                type="date"
                value={fromDay}
                onChange={(e) => {
                  setFromDay(e.target.value)
                  setOffset(0)
                }}
                className="w-36"
                data-testid="ws-audit-from"
              />
            </div>
            <div>
              <label htmlFor="ws-audit-to" className={LABEL_CLS}>
                {t('settingsWs.auditToLabel')}
              </label>
              <Input
                id="ws-audit-to"
                type="date"
                value={toDay}
                onChange={(e) => {
                  setToDay(e.target.value)
                  setOffset(0)
                }}
                className="w-36"
                data-testid="ws-audit-to"
              />
            </div>
            <Button type="submit" size="default" data-testid="ws-audit-apply">
              {t('settingsWs.auditApply')}
            </Button>
          </form>

          {audit.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
          {audit.isError && !forbidden && (
            <p role="alert" className="text-sm text-red-600 dark:text-red-400">
              {isApiError(audit.error) ? audit.error.message : t('settingsWs.loadFailed')}
            </p>
          )}
          {!audit.isPending && !audit.isError && items.length === 0 && (
            <p className="py-8 text-center text-xs text-zinc-400">{t('settingsWs.auditEmpty')}</p>
          )}

          {items.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-xs" data-testid="ws-audit-table">
                <thead>
                  <tr className="border-b border-zinc-200 text-xs text-zinc-500 dark:border-zinc-800">
                    <th className="py-1.5 pr-3 font-medium">{t('settingsWs.auditColTime')}</th>
                    <th className="py-1.5 pr-3 font-medium">{t('settingsWs.auditColActor')}</th>
                    <th className="py-1.5 pr-3 font-medium">{t('settingsWs.auditColActorType')}</th>
                    <th className="py-1.5 pr-3 font-medium">{t('settingsWs.auditColAction')}</th>
                    <th className="py-1.5 pr-3 font-medium">{t('settingsWs.auditColResource')}</th>
                    <th className="py-1.5 font-medium" aria-label={t('settingsWs.auditColMetadata')} />
                  </tr>
                </thead>
                <tbody>
                  {items.map((entry: AuditEntry) => (
                    <AuditRow
                      key={entry.id}
                      entry={entry}
                      expanded={expandedId === entry.id}
                      fmtTime={fmtTime}
                      onToggle={() => setExpandedId((cur) => (cur === entry.id ? null : entry.id))}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {/* 分页：limit=50，上一页/下一页按 offset（total 来自信封） */}
          {(hasPrev || hasNext) && (
            <div className="flex items-center justify-between text-xs text-zinc-500 dark:text-zinc-400">
              <span data-testid="ws-audit-total">
                {t('settingsWs.auditTotal', { total })}
              </span>
              <span className="flex gap-2">
                <Button
                  size="xs"
                  variant="outline"
                  disabled={!hasPrev}
                  data-testid="ws-audit-prev"
                  onClick={() => {
                    setOffset(Math.max(0, offset - PAGE_SIZE))
                    setExpandedId(null)
                  }}
                >
                  {t('settingsWs.auditPrev')}
                </Button>
                <Button
                  size="xs"
                  variant="outline"
                  disabled={!hasNext}
                  data-testid="ws-audit-next"
                  onClick={() => {
                    setOffset(offset + PAGE_SIZE)
                    setExpandedId(null)
                  }}
                >
                  {t('settingsWs.auditNext')}
                </Button>
              </span>
            </div>
          )}
        </>
      )}
    </section>
  )
}

/** 审计表一行 + metadata 展开行（JSON.stringify 缩进，pre 折叠）。 */
function AuditRow({
  entry,
  expanded,
  fmtTime,
  onToggle,
}: {
  entry: AuditEntry
  expanded: boolean
  fmtTime: (iso: string) => string
  onToggle: () => void
}) {
  const { t } = useTranslation()
  const hasMetadata = entry.metadata && Object.keys(entry.metadata).length > 0
  return (
    <>
      <tr
        className="border-b border-zinc-100 dark:border-zinc-800/60"
        data-testid="ws-audit-row"
        data-entry-id={entry.id}
      >
        <td className="py-1.5 pr-3 whitespace-nowrap text-zinc-500 dark:text-zinc-400">
          {fmtTime(entry.created_at)}
        </td>
        <td className="max-w-48 truncate py-1.5 pr-3" title={entry.actor_snapshot?.email}>
          {entry.actor_snapshot?.email || entry.actor_id || '–'}
        </td>
        <td className="py-1.5 pr-3">{entry.actor_type}</td>
        <td className="py-1.5 pr-3 font-mono">{entry.action}</td>
        <td className="py-1.5 pr-3">
          <span className="font-mono">{entry.resource_type}</span>
          {entry.resource_id && (
            <span
              className="ml-1 max-w-32 truncate align-bottom font-mono text-zinc-400"
              title={entry.resource_id}
            >
              {entry.resource_id}
            </span>
          )}
        </td>
        <td className="py-1.5 text-right">
          {hasMetadata && (
            <Button
              size="xs"
              variant="ghost"
              aria-expanded={expanded}
              aria-label={t('settingsWs.auditMetadataAria', { action: entry.action })}
              data-testid="ws-audit-metadata-toggle"
              onClick={onToggle}
            >
              {expanded ? (
                <ChevronDown className="size-3" aria-hidden="true" />
              ) : (
                <ChevronRight className="size-3" aria-hidden="true" />
              )}
              {t('settingsWs.auditColMetadata')}
            </Button>
          )}
        </td>
      </tr>
      {expanded && hasMetadata && (
        <tr className="border-b border-zinc-100 last:border-0 dark:border-zinc-800/60">
          <td colSpan={6} className="py-2 pl-6 pr-3">
            <pre
              className="overflow-x-auto rounded-md bg-zinc-50 p-2 font-mono text-xs leading-relaxed dark:bg-zinc-900"
              data-testid="ws-audit-metadata"
            >
              {JSON.stringify(entry.metadata, null, 2)}
            </pre>
          </td>
        </tr>
      )}
    </>
  )
}

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  deletePackEntry,
  listPackEntries,
  upsertPackEntry,
  type PackUIEntry,
} from '@/api/packs'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'

// 集成页段③ 页面入口（docs/integrations-page-design-2026-10-15.md §2.3）：
// pack_ui_entry 的读写面——侧栏深链入口 + 工单详情页签（v0 目录冻结
// workitem.detail.tab）。读 member、写 admin（端点自带 requireWorkspaceAdmin，
// 前端按 role 隐写控件）。
// 缓存键 ['pack-ui-entries', workspaceId] 与侧栏 PackEntries、工单 tab
// usePackSlots 三方共享——本段增删后必须 invalidate，否则最长 60s 不刷新。
// 手动入口：pack 名 custom-<slug>、pack_id=""（后端按 (workspace_id, pack)
// 幂等覆盖，同名重 PUT 不新建行）。

const LABEL_CLS = 'mb-1 block text-xs text-zinc-500 dark:text-zinc-400'

export interface UiEntriesSectionProps {
  workspaceId: string | null
  /** 当前用户在该工作区的 role；owner/admin 可见新增/移除控件，member 只读。 */
  role: string | undefined
}

// 手动入口内部键：custom-<label slug>（非 [a-z0-9] 折叠为 '-'、去首尾 '-'，
// slug 空则回退 entry-<ts>），整体限长 60；custom- 前缀保证不撞 provision 包名。
function packNameFromLabel(label: string): string {
  const slug = label
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return `custom-${slug || `entry-${Date.now()}`}`.slice(0, 60).replace(/-+$/, '')
}

/** 与后端 validateURLTemplate 同规则：绝对 http(s) 且 host 非空（防 iframe XSS）。 */
function isAbsoluteHttpUrl(raw: string): boolean {
  try {
    const u = new URL(raw)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.host !== ''
  } catch {
    return false
  }
}

export function UiEntriesSection({ workspaceId, role }: UiEntriesSectionProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const canWrite = role === 'owner' || role === 'admin'
  const [createOpen, setCreateOpen] = useState(false)
  const [removingId, setRemovingId] = useState<string | null>(null)
  const entries = useQuery({
    queryKey: ['pack-ui-entries', workspaceId],
    queryFn: listPackEntries,
    enabled: !!workspaceId,
    staleTime: 60000,
  })
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['pack-ui-entries', workspaceId] })
  const remove = useMutation({
    mutationFn: (pack: string) => deletePackEntry(pack),
    onSuccess: () => {
      setRemovingId(null)
      void invalidate()
    },
  })

  return (
    <section className="space-y-3" data-testid="ui-entries-section">
      {canWrite && (
        <div className="flex items-center justify-end">
          <Button
            size="sm"
            variant="outline"
            disabled={!workspaceId}
            data-testid="ui-entry-add"
            onClick={() => setCreateOpen(true)}
          >
            <Plus className="size-3.5" aria-hidden="true" />
            {t('tools.uiEntriesAdd')}
          </Button>
        </div>
      )}

      {entries.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
      {entries.isError && (
        <p role="alert" className="text-sm text-red-600 dark:text-red-400">
          {entries.error instanceof Error ? entries.error.message : t('common.requestFailed')}
        </p>
      )}
      {entries.data && entries.data.length === 0 && (
        <p className="py-2 text-sm text-zinc-400" data-testid="ui-entries-empty">
          {t('tools.uiEntriesEmpty')}
        </p>
      )}
      {entries.data && entries.data.length > 0 && (
        <ul className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
          {entries.data.map((e: PackUIEntry) => {
            const tab = (e.slots ?? []).find((s) => s.slot === 'workitem.detail.tab')
            const fromPack = typeof e.pack_id === 'string' && e.pack_id !== ''
            return (
              <li
                key={e.id}
                className="flex items-center gap-3 px-3 py-2.5"
                data-testid="ui-entry-row"
              >
                <div className="min-w-0 flex-1">
                  <p className="flex items-center gap-2 text-sm font-medium">
                    <span className="truncate">{e.label}</span>
                    {tab && (
                      <Badge variant="outline" className="shrink-0">
                        {t('tools.uiEntriesTabBadge', { height: tab.height })}
                      </Badge>
                    )}
                  </p>
                  <p className="truncate font-mono text-xs text-zinc-400">{e.url_template}</p>
                </div>
                {fromPack ? (
                  <Badge
                    variant="secondary"
                    className="shrink-0"
                    data-testid="ui-entry-from-pack"
                    render={<Link to="/packs/$packId" params={{ packId: e.pack_id! }} />}
                  >
                    {t('tools.uiEntriesFromPack')}
                  </Badge>
                ) : (
                  <Badge variant="outline" className="shrink-0" data-testid="ui-entry-manual">
                    {t('tools.uiEntriesManual')}
                  </Badge>
                )}
                {canWrite &&
                  (removingId === e.id ? (
                    <span className="flex shrink-0 items-center gap-1">
                      <Button
                        size="xs"
                        variant="danger"
                        disabled={remove.isPending}
                        data-testid="ui-entry-remove-confirm"
                        onClick={() => remove.mutate(e.pack)}
                      >
                        {t('tools.uiEntriesRemoveConfirm')}
                      </Button>
                      <Button size="xs" variant="ghost" onClick={() => setRemovingId(null)}>
                        {t('common.cancel')}
                      </Button>
                    </span>
                  ) : (
                    <Button
                      size="xs"
                      variant="ghost"
                      className="shrink-0 text-red-600 hover:text-red-600 dark:text-red-400 dark:hover:text-red-400"
                      aria-label={t('tools.uiEntriesRemove')}
                      data-testid="ui-entry-remove"
                      onClick={() => setRemovingId(e.id)}
                    >
                      <Trash2 className="size-3" aria-hidden="true" />
                    </Button>
                  ))}
              </li>
            )
          })}
        </ul>
      )}
      {remove.error && (
        <p role="alert" className="text-xs text-red-600 dark:text-red-400">
          {remove.error instanceof Error ? remove.error.message : t('common.requestFailed')}
        </p>
      )}

      {createOpen && workspaceId && (
        <CreateEntryDialog
          onClose={() => setCreateOpen(false)}
          onCreated={() => {
            void invalidate()
            setCreateOpen(false)
          }}
        />
      )}
    </section>
  )
}

// ---- 新增 Dialog：label + URL 模板必填；可选「工单详情页签」（height 默认 480）----
// 侧栏按钮与 tab 共用同一 url_template（占位符注入见 uiEntriesUrlHint）。

function CreateEntryDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void
  onCreated: () => void
}) {
  const { t } = useTranslation()
  const [label, setLabel] = useState('')
  const [url, setUrl] = useState('')
  const [asTab, setAsTab] = useState(false)
  const [height, setHeight] = useState(480)
  const [validation, setValidation] = useState<string | null>(null)

  const create = useMutation({
    mutationFn: () => {
      const trimmedLabel = label.trim()
      const urlTemplate = url.trim()
      // 后端 height 契约 (0, 4096]：空/非法输入回退默认 480，其余夹取到 [1, 4096]
      const rawHeight = Number(height)
      const slotHeight = Number.isFinite(rawHeight)
        ? Math.min(4096, Math.max(1, Math.round(rawHeight)))
        : 480
      return upsertPackEntry(packNameFromLabel(trimmedLabel), {
        pack_id: '',
        label: trimmedLabel,
        url_template: urlTemplate,
        // 不勾选 = 不加 slots 键（后端 nil = 无 slots，覆盖旧值）
        ...(asTab
          ? {
              slots: [
                {
                  slot: 'workitem.detail.tab',
                  label: trimmedLabel,
                  url_template: urlTemplate,
                  height: slotHeight,
                },
              ],
            }
          : {}),
      })
    },
    onSuccess: onCreated,
    onError: (err) =>
      setValidation(err instanceof Error ? err.message : t('common.requestFailed')),
  })
  const submit = () => {
    if (!isAbsoluteHttpUrl(url.trim())) {
      setValidation(t('tools.uiEntriesUrlInvalid'))
      return
    }
    setValidation(null)
    create.mutate()
  }

  return (
    <Dialog open onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <DialogContent data-testid="ui-entry-create-dialog">
        <DialogHeader>
          <DialogTitle>{t('tools.uiEntriesDialogTitle')}</DialogTitle>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <div>
            <label htmlFor="ui-entry-label" className={LABEL_CLS}>
              {t('tools.uiEntriesLabelField')}
            </label>
            <Input
              id="ui-entry-label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              autoFocus
              data-testid="ui-entry-label"
            />
          </div>
          <div>
            <label htmlFor="ui-entry-url" className={LABEL_CLS}>
              {t('tools.uiEntriesUrlField')}
            </label>
            <Input
              id="ui-entry-url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://vendor.example.com/console?ws={workspaceId}&t={uiToken}"
              data-testid="ui-entry-url"
            />
            <p className="mt-1 text-xs text-zinc-400 dark:text-zinc-500">
              {t('tools.uiEntriesUrlHint')}
            </p>
          </div>
          <div className="space-y-2">
            <label className="flex cursor-pointer items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400">
              <input
                type="checkbox"
                checked={asTab}
                onChange={(e) => setAsTab(e.target.checked)}
                className="size-3 accent-zinc-700 dark:accent-zinc-300"
                data-testid="ui-entry-tab-toggle"
              />
              {t('tools.uiEntriesTabToggle')}
            </label>
            {asTab && (
              <div>
                <label htmlFor="ui-entry-height" className={LABEL_CLS}>
                  {t('tools.uiEntriesTabHeight')}
                </label>
                <Input
                  id="ui-entry-height"
                  type="number"
                  min={1}
                  max={4096}
                  value={height}
                  onChange={(e) => setHeight(Number(e.target.value))}
                  className="w-28"
                  data-testid="ui-entry-height"
                />
              </div>
            )}
          </div>
          {validation && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {validation}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button
              type="submit"
              disabled={!label.trim() || !url.trim() || create.isPending}
              data-testid="ui-entry-submit"
            >
              {create.isPending ? t('common.loading') : t('common.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

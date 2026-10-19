import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ChevronDown, PackagePlus, Pencil, Plus, Trash2, Wrench } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Checkbox, FormField, Label, useConfirm } from '@ploykit/ui'
import {
  createWorkspaceMcp,
  deleteWorkspaceMcp,
  listPackEntries,
  listWorkspaceMcp,
  probeWorkspaceMcpTools,
  updateWorkspaceMcp,
  type McpProbeTool,
  type PackUIEntry,
  type WorkspaceMCPInput,
  type WorkspaceMCPServer,
} from '@/api/packs'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { cn } from '@/lib/utils'

// states 组合件的行内规格（ui-sweep 裁决⑤）：PageLoading/PageError 默认是页面级
// （pt-24 居中），区块内三态用这组 className 压成行内形态。与 settings/shared.tsx
// 的 INLINE_* 同源；不直接 import（shared 拉入 router-singleton，会把整棵路由图
// 拖进本组件的导入链，破坏组件级测试的模块解析）。
const INLINE_LOADING_CLS = 'flex-row items-center gap-2 py-2 text-left'
const INLINE_ERROR_CLS = 'flex-row items-center gap-2 py-2 text-left'
const INLINE_EMPTY_CLS = 'items-start gap-1 py-2 text-left'

// Packs 与集成页的组件拆分（docs/pack-frontend-design-2026-10-11.md §3.1）：
// 第 1 段 PacksInstalledSection —— 只读列表（pack 名 + entry label + installed_at）；
//     v0 明确不做：健康列 / 版本号 / 工具数 / 订阅数（数据源不存在，审计确认）。
//     P1（pack-first-class-design §2.2）后此段保持不动——已装 pack 的管理面
//     迁往 /packs 页，这里只留侧栏语义的只读快照。
// 第 2 段 WorkspaceMcpSection —— 工作区级 MCP 服务器注册表（新写，非平移；
//     agent 级管理 UI 在 agent 详情页 MCP tab，两者并存不搬动）。
//     P1 起行带来源列：pack_id 非空 = pack 安装产出，只读 + 「管理 →」链详情；
//     手工行 CRUD 不变。
// 两段拆成独立组件以便组件级测试（三态 / 增删交互）。

export function PacksInstalledSection({ className }: { className?: string }) {
  const { t, i18n } = useTranslation()
  const entries = useQuery({
    queryKey: ['pack-ui-entries-page'],
    queryFn: listPackEntries,
  })
  const fmtDate = (iso: string): string =>
    new Date(iso).toLocaleDateString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })
  return (
    <section className={cn('space-y-3', className)} aria-labelledby="packs-installed-title">
      <div className="flex items-center justify-between gap-2">
        <div>
          <h2 id="packs-installed-title" className="text-sm font-semibold">
            {t('packs.installedTitle')}
          </h2>
          <p className="text-xs text-zinc-500 dark:text-zinc-400">
            {t('packs.installedDesc')}
          </p>
        </div>
      </div>
      {entries.isPending && (
        <PageLoading label={t('common.loading')} className={INLINE_LOADING_CLS} />
      )}
      {entries.isError && (
        <PageError
          title={t('common.error')}
          message={
            entries.error instanceof Error ? entries.error.message : t('common.requestFailed')
          }
          className={INLINE_ERROR_CLS}
        />
      )}
      {entries.data && entries.data.length === 0 && (
        <div
          className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-zinc-300 px-4 py-8 text-center dark:border-zinc-700"
          data-testid="packs-empty"
        >
          <PackagePlus className="size-5 text-zinc-400" aria-hidden="true" />
          <p className="text-sm text-zinc-500 dark:text-zinc-400">{t('packs.emptyTitle')}</p>
          <p className="max-w-md text-xs text-zinc-400 dark:text-zinc-500">
            {t('packs.emptyDesc')}
          </p>
        </div>
      )}
      {entries.data && entries.data.length > 0 && (
        <ul
          className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800"
          data-testid="packs-list"
        >
          {entries.data.map((e: PackUIEntry) => (
            <li key={e.id} className="flex items-center gap-3 px-3 py-2.5" data-testid="packs-row">
              <div className="min-w-0 flex-1">
                <p className="flex items-center gap-2 text-sm font-medium">
                  <span className="font-mono">{e.pack}</span>
                  <span className="text-zinc-400" aria-hidden="true">
                    ·
                  </span>
                  <span className="truncate">{e.label}</span>
                </p>
              </div>
              <span className="shrink-0 text-xs text-zinc-500 dark:text-zinc-400">
                {t('packs.installedAt', { date: fmtDate(e.installed_at) })}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

interface ServerFormState {
  name: string
  url: string
  transport: string
  apiKey: string
}

// transport 合法值统一（2026-10-17 裁决）：streamable_http | sse，缺省
// streamable_http。旧 'http' 不再出现在下拉（v0 无兼容原则——存量行的
// transport 值由后端统一迁移口径，前端不再兜底展示）。
const TRANSPORTS = ['streamable_http', 'sse'] as const
// 下拉展示标签：值仍是协议串（直传后端），标签给人读。
const TRANSPORT_LABELS: Record<(typeof TRANSPORTS)[number], string> = {
  streamable_http: 'Streamable HTTP',
  sse: 'SSE',
}

// 单台服务器的工具清单面板：点击「查看工具」时才发起 probe（实时建连，
// 非缓存），结果在面板存活期间保留——收起再展开会重新 probe（服务器端
// 工具面可能已变化，重查即最新态）。
// P1 提取为按 serverId 取数 + label 键可注入，供 /packs/$packId 详情页
// 「实时探针」直接复用（pack-first-class-design §2.3 ②）。
export function ServerToolsPanel({
  workspaceId,
  serverId,
  i18nPrefix = 'packs',
}: {
  workspaceId: string
  /** 工作区 MCP 注册表行 id（probe 端点按此寻址，非服务器名） */
  serverId: string
  /** i18n 前缀：/tools 页用 packs.*，pack 详情页传 packs2.* */
  i18nPrefix?: 'packs' | 'packs2'
}) {
  const { t } = useTranslation()
  const tools = useQuery({
    queryKey: ['workspace-mcp-probe', workspaceId, serverId],
    queryFn: () => probeWorkspaceMcpTools(workspaceId, serverId),
    gcTime: 0, // 面板关闭即弃——下次展开重新 probe
    retry: false,
  })
  if (tools.isPending) {
    return <p className="px-3 py-2 text-xs text-zinc-500">{t(`${i18nPrefix}.toolsLoading`)}</p>
  }
  if (tools.isError) {
    return (
      <p role="alert" className="px-3 py-2 text-xs text-red-600 dark:text-red-400">
        {t(`${i18nPrefix}.toolsFailed`)}
        {tools.error instanceof Error ? `：${tools.error.message}` : ''}
      </p>
    )
  }
  const list: McpProbeTool[] = tools.data?.tools ?? []
  if (list.length === 0) {
    return <p className="px-3 py-2 text-xs text-zinc-500">{t(`${i18nPrefix}.toolsEmpty`)}</p>
  }
  return (
    <div className="px-3 pb-3">
      <p className="mb-1.5 text-xs font-medium text-zinc-500 dark:text-zinc-400">
        {t(`${i18nPrefix}.toolCount`, { count: list.length })}
      </p>
      <ul className="space-y-1.5">
        {list.map((tool) => (
          <li key={tool.name} className="rounded-md bg-zinc-50 px-2.5 py-1.5 dark:bg-zinc-800/60">
            <code className="font-mono text-xs font-medium">{tool.name}</code>
            {tool.description && (
              <p className="mt-0.5 line-clamp-2 text-xs text-zinc-500 dark:text-zinc-400">
                {tool.description}
              </p>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}

export function WorkspaceMcpSection({
  workspaceId,
  className,
}: {
  workspaceId: string | null
  className?: string
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const [editing, setEditing] = useState<WorkspaceMCPServer | 'new' | null>(null)
  const [toolsOpenId, setToolsOpenId] = useState<string | null>(null)
  const servers = useQuery({
    queryKey: ['workspace-mcp', workspaceId],
    queryFn: () => listWorkspaceMcp(workspaceId as string),
    enabled: !!workspaceId,
  })
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['workspace-mcp', workspaceId] })
  const remove = useMutation({
    mutationFn: (serverId: string) => deleteWorkspaceMcp(workspaceId as string, serverId),
    onSuccess: () => void invalidate(),
  })
  // 删除两段式 → useConfirm 统一形态（ui-sweep 裁决①）：标题复用行 aria-label
  // 的同键文案，确认钮沿用 packs.confirmDelete。
  const handleRemove = async (s: WorkspaceMCPServer) => {
    const ok = await confirm({
      title: t('packs.deleteServer', { name: s.name }),
      danger: true,
      confirmText: t('packs.confirmDelete'),
    })
    if (ok) remove.mutate(s.id)
  }
  return (
    <section className={cn('space-y-3', className)}>
      <div className="flex items-center justify-end">
        <Button size="sm" onClick={() => setEditing('new')} disabled={!workspaceId}>
          <Plus className="size-3.5" aria-hidden="true" />
          {t('packs.addServer')}
        </Button>
      </div>

      {servers.isPending && (
        <PageLoading label={t('common.loading')} className={INLINE_LOADING_CLS} />
      )}
      {servers.isError && (
        <PageError
          title={t('common.error')}
          message={
            servers.error instanceof Error ? servers.error.message : t('common.requestFailed')
          }
          className={INLINE_ERROR_CLS}
        />
      )}
      {servers.data && servers.data.length === 0 && (
        <div data-testid="ws-mcp-empty">
          <PageEmpty title={t('packs.noServers')} className={INLINE_EMPTY_CLS} />
        </div>
      )}
      {servers.data && servers.data.length > 0 && (
        <ul
          className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800"
          data-testid="ws-mcp-list"
        >
          {servers.data.map((s) => {
            const toolsOpen = toolsOpenId === s.id
            // 来源列（pack-first-class-design §2.2）：pack_id 非空 = pack 安装
            // 产出，只读（隐藏编辑/删除，管理动作在包详情页）；空 = 手工注册，
            // 现有 CRUD 行为不变。
            const fromPack = typeof s.pack_id === 'string' && s.pack_id !== ''
            return (
              <li
                key={s.id}
                className="px-3 py-2.5"
                data-testid="ws-mcp-row"
                data-from-pack={fromPack || undefined}
              >
                <div className="flex items-center gap-3">
                  <div className="min-w-0 flex-1">
                    <p className="flex items-center gap-2 text-sm font-medium">
                      {s.name}
                      <Badge variant="neutral">{s.transport}</Badge>
                      {s.has_credentials && <Badge variant="success">{t('packs.storedCred')}</Badge>}
                      {fromPack && <Badge variant="secondary">{t('packs2.fromPackBadge')}</Badge>}
                    </p>
                    <p className="truncate font-mono text-xs text-zinc-400">{s.url}</p>
                  </div>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-expanded={toolsOpen}
                    onClick={() => setToolsOpenId(toolsOpen ? null : s.id)}
                  >
                    <Wrench className="size-3.5" aria-hidden="true" />
                    {toolsOpen ? t('packs.hideTools') : t('packs.viewTools')}
                    <ChevronDown
                      className={
                        'size-3.5 shrink-0 text-zinc-400 transition-transform' +
                        (toolsOpen ? ' rotate-180' : '')
                      }
                      aria-hidden="true"
                    />
                  </Button>
                  {fromPack ? (
                    <Link
                      to="/packs/$packId"
                      params={{ packId: s.pack_id! }}
                      className="flex h-7 shrink-0 items-center rounded-[min(var(--radius-md),12px)] px-2.5 text-[0.8rem] font-medium text-zinc-600 outline-none hover:bg-zinc-100 hover:text-zinc-900 dark:text-zinc-400 dark:hover:bg-zinc-800 dark:hover:text-zinc-100"
                      data-testid="ws-mcp-pack-link"
                    >
                      {t('packs2.manageLink')}
                    </Link>
                  ) : (
                    <>
                      <Button size="sm" variant="ghost" onClick={() => setEditing(s)}>
                        <Pencil className="size-3.5" aria-hidden="true" />
                        {t('common.edit')}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="shrink-0 text-red-600 dark:text-red-400"
                        aria-label={t('packs.deleteServer', { name: s.name })}
                        onClick={() => void handleRemove(s)}
                      >
                        <Trash2 className="size-3.5" aria-hidden="true" />
                      </Button>
                    </>
                  )}
                </div>
                {toolsOpen && workspaceId && (
                  <div className="mt-2 rounded-md border border-zinc-200 pt-2 dark:border-zinc-800">
                    <ServerToolsPanel workspaceId={workspaceId} serverId={s.id} />
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}

      {editing && (
        <WorkspaceMcpDialog
          workspaceId={workspaceId as string}
          server={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => void invalidate()}
        />
      )}
    </section>
  )
}

function WorkspaceMcpDialog({
  workspaceId,
  server,
  onClose,
  onSaved,
}: {
  workspaceId: string
  server: WorkspaceMCPServer | null
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState<ServerFormState>({
    name: server?.name ?? '',
    url: server?.url ?? '',
    // 旧值 'http' 归一到 streamable_http（不在下拉合法值内会渲染成空选中）。
    transport: server?.transport === 'sse' ? 'sse' : 'streamable_http',
    apiKey: '',
  })
  const [clearCreds, setClearCreds] = useState(false)
  const set = <K extends keyof ServerFormState>(key: K, value: ServerFormState[K]) =>
    setForm((f) => ({ ...f, [key]: value }))
  const save = useMutation({
    mutationFn: () => {
      // 三态凭证（W2-3 / FP-09 P1-4，与 agent 级统一）：
      //   编辑留空 = 不传字段（保留库中现值）；勾选清除 = 显式 null（清空）；
      //   输入新值 = 覆盖（凭证形状与 pack-provision/main.go 对齐：
      //   Authorization: Bearer <key>，后端 sealx 加密落库）。
      // 序列化前显式不设置键，而不是传 undefined 占位——语义即"字段缺失"。
      const key = form.apiKey.trim()
      const input: WorkspaceMCPInput = {
        name: form.name.trim(),
        url: form.url.trim(),
        transport: form.transport,
      }
      if (key) {
        input.credentials = { headers: { Authorization: `Bearer ${key}` } }
      } else if (clearCreds) {
        input.credentials = null
      }
      return server
        ? updateWorkspaceMcp(workspaceId, server.id, input)
        : createWorkspaceMcp(workspaceId, input)
    },
    onSuccess: () => {
      onSaved()
      onClose()
    },
  })
  const submit = () => {
    if (form.name.trim() && form.url.trim() && !save.isPending) save.mutate()
  }
  return (
    <Dialog open onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{server ? t('packs.editServer') : t('packs.addServer')}</DialogTitle>
        </DialogHeader>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <FormField label={t('packs.serverNameLabel')} htmlFor="ws-mcp-name">
            <Input
              id="ws-mcp-name"
              value={form.name}
              onChange={(e) => set('name', e.target.value)}
              autoFocus
              placeholder="web-search"
            />
          </FormField>
          <FormField label={t('packs.serverUrlLabel')} htmlFor="ws-mcp-url">
            <Input
              id="ws-mcp-url"
              value={form.url}
              onChange={(e) => set('url', e.target.value)}
              placeholder="https://mcp.example.com/mcp"
            />
          </FormField>
          <FormField label={t('packs.transportLabel')} htmlFor="ws-mcp-transport">
            <Select
              id="ws-mcp-transport"
              value={form.transport}
              onChange={(e) => set('transport', e.target.value)}
            >
              {TRANSPORTS.map((tr) => (
                <option key={tr} value={tr}>
                  {TRANSPORT_LABELS[tr]}
                </option>
              ))}
            </Select>
          </FormField>
          <FormField
            label={server ? t('packs.apiKeyKeepLabel') : t('packs.apiKeyLabel')}
            htmlFor="ws-mcp-key"
          >
            <Input
              id="ws-mcp-key"
              type="password"
              value={form.apiKey}
              onChange={(e) => {
                set('apiKey', e.target.value)
                if (e.target.value) setClearCreds(false)
              }}
              placeholder="sk-…"
            />
          </FormField>
          {server && server.has_credentials && !form.apiKey.trim() && (
            <Label className="flex cursor-pointer items-center gap-1.5 text-xs font-normal text-zinc-500 dark:text-zinc-400">
              <Checkbox checked={clearCreds} onCheckedChange={(v) => setClearCreds(v === true)} />
              {t('packs.apiKeyClearToggle')}
            </Label>
          )}
          {clearCreds && (
            <p className="text-xs text-amber-600 dark:text-amber-400">
              {t('packs.apiKeyClearWarning')}
            </p>
          )}
          {save.error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {save.error.message}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={!form.name.trim() || !form.url.trim() || save.isPending}>
              {save.isPending ? t('common.saving') : t('common.save')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

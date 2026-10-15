import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Boxes, ChevronDown, PackageOpen, PackagePlus, Pencil, Plus, Trash2, Wrench } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  createWorkspaceMcp,
  deleteWorkspaceMcp,
  listPackEntries,
  listPacks,
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
import { cn } from '@/lib/utils'

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
      {entries.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
      {entries.isError && (
        <p role="alert" className="text-sm text-red-600 dark:text-red-400">
          {entries.error instanceof Error
            ? entries.error.message
            : t('common.requestFailed')}
        </p>
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

const TRANSPORTS = ['http', 'sse'] as const

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
  const [editing, setEditing] = useState<WorkspaceMCPServer | 'new' | null>(null)
  const [removingId, setRemovingId] = useState<string | null>(null)
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
    onSuccess: () => {
      setRemovingId(null)
      void invalidate()
    },
  })
  return (
    <section className={cn('space-y-3', className)}>
      <div className="flex items-center justify-end">
        <Button size="sm" onClick={() => setEditing('new')} disabled={!workspaceId}>
          <Plus className="size-3.5" aria-hidden="true" />
          {t('packs.addServer')}
        </Button>
      </div>

      {servers.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
      {servers.isError && (
        <p role="alert" className="text-sm text-red-600 dark:text-red-400">
          {servers.error instanceof Error ? servers.error.message : t('common.requestFailed')}
        </p>
      )}
      {servers.data && servers.data.length === 0 && (
        <p className="text-sm text-zinc-500 dark:text-zinc-400" data-testid="ws-mcp-empty">
          {t('packs.noServers')}
        </p>
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
                      {removingId === s.id ? (
                        <span className="flex shrink-0 items-center gap-1">
                          <Button
                            size="sm"
                            variant="danger"
                            disabled={remove.isPending}
                            onClick={() => remove.mutate(s.id)}
                          >
                            {t('packs.confirmDelete')}
                          </Button>
                          <Button size="sm" variant="ghost" onClick={() => setRemovingId(null)}>
                            {t('common.cancel')}
                          </Button>
                        </span>
                      ) : (
                        <Button
                          size="sm"
                          variant="ghost"
                          className="shrink-0 text-red-600 dark:text-red-400"
                          aria-label={t('packs.deleteServer', { name: s.name })}
                          onClick={() => setRemovingId(s.id)}
                        >
                          <Trash2 className="size-3.5" aria-hidden="true" />
                        </Button>
                      )}
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
    transport: server?.transport ?? 'http',
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
          <div>
            <label htmlFor="ws-mcp-name" className="mb-1 block text-xs text-zinc-500">
              {t('packs.serverNameLabel')}
            </label>
            <Input
              id="ws-mcp-name"
              value={form.name}
              onChange={(e) => set('name', e.target.value)}
              autoFocus
              placeholder="web-search"
            />
          </div>
          <div>
            <label htmlFor="ws-mcp-url" className="mb-1 block text-xs text-zinc-500">
              {t('packs.serverUrlLabel')}
            </label>
            <Input
              id="ws-mcp-url"
              value={form.url}
              onChange={(e) => set('url', e.target.value)}
              placeholder="https://mcp.example.com/mcp"
            />
          </div>
          <div>
            <label htmlFor="ws-mcp-transport" className="mb-1 block text-xs text-zinc-500">
              {t('packs.transportLabel')}
            </label>
            <select
              id="ws-mcp-transport"
              value={form.transport}
              onChange={(e) => set('transport', e.target.value)}
              className="h-9 w-full rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900"
            >
              {TRANSPORTS.map((tr) => (
                <option key={tr} value={tr}>
                  {tr}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label htmlFor="ws-mcp-key" className="mb-1 block text-xs text-zinc-500">
              {server ? t('packs.apiKeyKeepLabel') : t('packs.apiKeyLabel')}
            </label>
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
            {server && server.has_credentials && !form.apiKey.trim() && (
              <label className="mt-1 flex cursor-pointer items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400">
                <input
                  type="checkbox"
                  checked={clearCreds}
                  onChange={(e) => setClearCreds(e.target.checked)}
                  className="size-3 accent-zinc-700"
                />
                {t('packs.apiKeyClearToggle')}
              </label>
            )}
            {clearCreds && (
              <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                {t('packs.apiKeyClearWarning')}
              </p>
            )}
          </div>
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

// —— 集成页（/tools）上段：已装能力包卡片（原 /packs 列表页主体平移）——
// 一行一卡（名/版本/安装日期/资产计数），点击进 /packs/{id} 五区块详情；
// 安装向导入口（平台化 P2）随卡片区头部走。
export function PacksCardsSection() {
  const { t, i18n } = useTranslation()
  const packs = useQuery({ queryKey: ['packs'], queryFn: listPacks })
  const fmtDate = (iso: string): string =>
    new Date(iso).toLocaleDateString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })
  return (
    <section className="space-y-3" aria-labelledby="tools-packs-title">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 id="tools-packs-title" className="text-sm font-semibold">
            {t('tools.packsSectionTitle')}
          </h2>
          <p className="text-xs text-zinc-500 dark:text-zinc-400">
            {t('tools.packsSectionIntro')}
          </p>
        </div>
        <Link
          to="/packs/install"
          className="inline-flex h-8 shrink-0 items-center gap-1.5 rounded-lg bg-primary px-2.5 text-sm font-medium text-primary-foreground outline-none hover:bg-primary/80 focus-visible:ring-3 focus-visible:ring-ring/50"
          data-testid="packs-install-link"
        >
          <PackagePlus className="size-4" aria-hidden="true" />
          {t('packsInstall.wizardTitle')}
        </Link>
      </div>

      {packs.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
      {packs.isError && (
        <p role="alert" className="text-sm text-red-600 dark:text-red-400">
          {packs.error instanceof Error ? packs.error.message : t('common.requestFailed')}
        </p>
      )}
      {packs.data && packs.data.length === 0 && (
        <p className="text-sm text-zinc-500 dark:text-zinc-400">{t('packs2.emptyTitle')}</p>
      )}
      {packs.data && packs.data.length > 0 && (
        <ul
          className="divide-y divide-zinc-200 rounded-lg border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800"
          data-testid="packs2-list"
        >
          {packs.data.map((p) => (
            <li key={p.id}>
              <Link
                to="/packs/$packId"
                params={{ packId: p.id }}
                className="flex items-center gap-3 px-3 py-2.5 text-sm outline-none hover:bg-zinc-50 dark:hover:bg-zinc-900/50"
                data-testid="packs2-row"
              >
                <PackageOpen className="size-4 shrink-0 text-zinc-400" aria-hidden="true" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-mono font-medium">{p.name}</span>
                  <span className="block truncate text-xs text-zinc-500 dark:text-zinc-400">
                    v{p.version || '–'} · {t('packs2.installedAtLabel')} {fmtDate(p.installed_at)}
                  </span>
                </span>
                <span className="hidden shrink-0 items-center gap-2 text-xs text-zinc-500 sm:flex dark:text-zinc-400">
                  <span className="flex items-center gap-1" title={t('packs2.colServers')}>
                    <Boxes className="size-3.5 text-zinc-400" aria-hidden="true" />
                    {p.server_count}
                  </span>
                  <span className="flex items-center gap-1" title={t('packs2.colAgents')}>
                    <PackageOpen className="size-3.5 text-zinc-400" aria-hidden="true" />
                    {p.agent_count}
                  </span>
                  <span>
                    {t('packs2.colSkills')} {p.skill_count}
                  </span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import {
  ArrowLeft,
  BookOpen,
  ChevronDown,
  ChevronRight,
  Copy,
  HeartPulse,
  Pause,
  Play,
  Radio,
  RotateCw,
  Trash2,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { getPack, listWorkspaceMcp, probeWorkspaceMcpTools, type PackDetail } from '@/api/packs'
import { PackUsageDialog } from '@/components/pack-export-dialog'
import {
  listWebhookDeliveries,
  listWebhookEvents,
  pingWebhookSubscription,
  redeliverWebhookDelivery,
  rotateWebhookSecret,
  setWebhookSubscriptionActive,
  type WebhookDelivery,
} from '@/api/webhooks'
import { ServerToolsPanel } from '@/components/packs-sections'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { PageError, PageLoading } from '@/components/ui/states'
import { useWorkspaceStore } from '@/stores/workspace'

// Pack 详情页五区块视图（Pack 一等公民化 P1，docs/pack-first-class-design-2026-10-13.md §2.3）：
//   ① 概览（版本/安装时间/健康态）② 工具（存档清单 + 逐服务器实时探针）
//   ③ 事件（订阅列表 + ping/轮换密钥/暂停恢复/投递日志 + 事件目录）
//   ④ 界面（控制台入口 + slots）⑤ 资产（agents 含 archived 灰显 + skills 名单）。
// 卸载 P1 只弹 CLI 命令提示（不调接口，server 端卸载在 P2/R4）。
// 独立成组件文件的原因同 task-transcript-drawer：router 插件的
// autoCodeSplitting 会拆走路由模块的非 Route 导出，页面级组件测试须从这里引用。

/** 探活三态（§2.3 ①健康态：全部 ok / 部分失败 / 全失败）。 */
type ProbeHealth = 'healthy' | 'partial' | 'down'

/** 纯逻辑：探活结果聚合为三态（可测）。 */
export function aggregateHealth(total: number, ok: number): ProbeHealth {
  if (total === 0) return 'healthy' // 无服务器可探 = 无失联项
  if (ok === 0) return 'down'
  return ok === total ? 'healthy' : 'partial'
}

export function PackDetailView({ packId }: { packId: string }) {
  const { t, i18n } = useTranslation()
  const workspaceId = useWorkspaceStore((s) => s.currentId)
  const detail = useQuery({ queryKey: ['pack', packId], queryFn: () => getPack(packId) })

  // 契约里 servers 只回 {name,transport,url,has_credentials}（无 id），而探针
  // 端点按注册表行 id 寻址——故借 listWorkspaceMcp 建名→id 映射。
  const registry = useQuery({
    queryKey: ['workspace-mcp', workspaceId],
    queryFn: () => listWorkspaceMcp(workspaceId as string),
    enabled: !!workspaceId && !!detail.data,
  })

  // 头部「探活」：逐 server 并发跑 probe，全 settle 后汇总三态（P1 手动触发，
  // 不自动轮询）。每台的结果也喂给区块②的「最近探针」标注。
  const [probing, setProbing] = useState(false)
  const [health, setHealth] = useState<ProbeHealth | null>(null)
  const [healthDetail, setHealthDetail] = useState<{ ok: number; total: number } | null>(null)
  const [probeStatus, setProbeStatus] = useState<Map<string, 'ok' | 'fail'>>(new Map())
  const runProbe = async () => {
    if (!workspaceId || !detail.data || probing) return
    const nameToId = new Map((registry.data ?? []).map((s) => [s.name, s.id]))
    const targets = detail.data.servers
      .map((s) => ({ name: s.name, id: nameToId.get(s.name) }))
      .filter((s): s is { name: string; id: string } => !!s.id)
    const total = targets.length
    setProbing(true)
    let ok = 0
    const status = new Map<string, 'ok' | 'fail'>()
    await Promise.all(
      targets.map(async ({ name, id }) => {
        try {
          await probeWorkspaceMcpTools(workspaceId, id)
          status.set(name, 'ok')
          ok++
        } catch {
          status.set(name, 'fail')
        }
      }),
    )
    setProbeStatus(status)
    setHealthDetail({ ok, total })
    setHealth(aggregateHealth(total, ok))
    setProbing(false)
  }

  const [uninstallOpen, setUninstallOpen] = useState(false)
  // 使用文档 Dialog（平台化 P2）：打开才挂载组件（查询随 Dialog 生命周期）
  const [usageOpen, setUsageOpen] = useState(false)
  // 探针寻址依赖注册表名→id 映射；注册表未就绪时先禁用探活（否则映射为空，
  // 探活空转成 0/0 健康，误导）。
  const probeReady = !!workspaceId && !!detail.data && registry.isSuccess
  const fmtDate = (iso: string): string =>
    new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })

  if (detail.isPending) return <PageLoading label={t('packs2.loading')} />
  if (detail.isError) {
    return (
      <PageError
        title={t('packs2.notFound')}
        message={detail.error instanceof Error ? detail.error.message : undefined}
      />
    )
  }
  const d: PackDetail = detail.data
  return (
    <div className="space-y-6">
      {/* 头部：返回 + 包名 + 版本 + 探活/卸载 */}
      <div className="flex flex-wrap items-center gap-2">
        <Link
          to="/packs"
          className="flex size-7 items-center justify-center rounded-md text-zinc-400 outline-none hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
          aria-label={t('packs2.backToList')}
        >
          <ArrowLeft className="size-4" aria-hidden="true" />
        </Link>
        <h1 className="min-w-0 truncate font-mono text-lg font-semibold">{d.pack.name}</h1>
        {d.pack.version && <Badge variant="neutral">v{d.pack.version}</Badge>}
        <div className="ml-auto flex items-center gap-2">
          {health && (
            <Badge
              variant={
                health === 'healthy' ? 'success' : health === 'partial' ? 'warning' : 'danger'
              }
              data-testid="pack-health-badge"
            >
              <HeartPulse className="size-3" aria-hidden="true" />
              {t(`packs2.probe${health.charAt(0).toUpperCase()}${health.slice(1)}`)}
            </Badge>
          )}
          <Button
            size="sm"
            variant="outline"
            disabled={probing || !probeReady}
            onClick={() => void runProbe()}
          >
            <HeartPulse className="size-3.5" aria-hidden="true" />
            {probing ? t('packs2.probing') : t('packs2.probeAction')}
          </Button>
          {/* 使用文档（平台化 P2）：member 可读端点，无权限门槛 */}
          <Button
            size="sm"
            variant="outline"
            data-testid="pack-usage-open"
            onClick={() => setUsageOpen(true)}
          >
            <BookOpen className="size-3.5" aria-hidden="true" />
            {t('packsInstall.usageAction')}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="text-red-600 dark:text-red-400"
            onClick={() => setUninstallOpen(true)}
          >
            <Trash2 className="size-3.5" aria-hidden="true" />
            {t('packs2.uninstallAction')}
          </Button>
        </div>
      </div>
      {health && healthDetail && (
        <p
          className="pl-9 text-xs text-zinc-500 dark:text-zinc-400"
          data-testid="pack-health-detail"
        >
          {t(
            health === 'healthy'
              ? 'packs2.probeHealthyDetail'
              : health === 'partial'
                ? 'packs2.probePartialDetail'
                : 'packs2.probeDownDetail',
            healthDetail,
          )}
        </p>
      )}

      <SectionBlock title={t('packs2.overviewTitle')}>
        <dl className="grid grid-cols-[max-content_1fr] items-baseline gap-x-4 gap-y-1.5 text-sm">
          <dt className="text-xs text-zinc-500 dark:text-zinc-400">{t('packs2.versionLabel')}</dt>
          <dd className="font-mono">{d.pack.version || '–'}</dd>
          <dt className="text-xs text-zinc-500 dark:text-zinc-400">
            {t('packs2.installedAtLabel')}
          </dt>
          <dd>{fmtDate(d.pack.installed_at)}</dd>
          <dt className="text-xs text-zinc-500 dark:text-zinc-400">{t('packs2.serversLabel')}</dt>
          <dd>{d.servers.length}</dd>
          <dt className="text-xs text-zinc-500 dark:text-zinc-400">{t('packs2.healthLabel')}</dt>
          <dd data-testid="pack-overview-health">
            {health
              ? t(
                  health === 'healthy'
                    ? 'packs2.probeHealthyDetail'
                    : health === 'partial'
                      ? 'packs2.probePartialDetail'
                      : 'packs2.probeDownDetail',
                  healthDetail ?? { ok: 0, total: 0 },
                )
              : t('packs2.healthUnknown')}
          </dd>
        </dl>
      </SectionBlock>

      {/* ② 工具：存档清单 + 逐服务器实时探针 */}
      <SectionBlock title={t('packs2.toolsTitle')}>
        <div className="space-y-3">
          <div>
            <p className="mb-1.5 text-xs text-zinc-500 dark:text-zinc-400">
              {t('packs2.toolsArchiveTitle')}
            </p>
            {d.tool_schemas.length === 0 ? (
              <p className="text-sm text-zinc-500">{t('packs2.toolsArchiveEmpty')}</p>
            ) : (
              <ul className="divide-y divide-zinc-200 rounded-md border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
                {d.tool_schemas.map((ts, i) => (
                  <li
                    key={`${ts.server}/${ts.tool_name}/${i}`}
                    className="flex items-center gap-3 px-3 py-2 text-sm"
                    data-testid="pack-tool-row"
                  >
                    <code className="font-mono text-xs font-medium">{ts.tool_name}</code>
                    <span className="truncate text-xs text-zinc-400">@{ts.server}</span>
                    <Badge variant="neutral" className="ml-auto shrink-0">
                      {t(
                        ts.source === 'protocol'
                          ? 'packs2.toolSourceProtocol'
                          : ts.source === 'pack_yaml'
                            ? 'packs2.toolSourcePackYaml'
                            : 'packs2.toolSourceOther',
                      )}
                    </Badge>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div>
            <p className="mb-1.5 text-xs text-zinc-500 dark:text-zinc-400">
              {t('packs2.liveProbeTitle')}
            </p>
            {d.servers.length === 0 ? (
              <p className="text-sm text-zinc-500">{t('packs2.probeNoServers')}</p>
            ) : (
              <ul className="space-y-2">
                {d.servers.map((s) => (
                  <PackServerProbeRow
                    key={s.name}
                    server={s}
                    workspaceId={workspaceId}
                    registryId={registry.data?.find((r) => r.name === s.name)?.id}
                    probeStatus={probeStatus.get(s.name)}
                  />
                ))}
              </ul>
            )}
          </div>
        </div>
      </SectionBlock>

      {/* ③ 事件：订阅列表 + 管理动作 + 事件目录 */}
      <SectionBlock title={t('packs2.eventsTitle')}>
        <div className="space-y-2">
          <p className="text-xs text-zinc-500 dark:text-zinc-400">
            {t('packs2.eventsHint')}{' '}
            {/* 订阅本体的家（settings 域设计 §3.6）：锚点 id="webhooks" 由
                集成页段②保证。 */}
            <Link
              to="/tools"
              hash="webhooks"
              className="text-primary underline-offset-2 hover:underline"
              data-testid="pack-subscriptions-manage-link"
            >
              {t('packs2.manageInSettings')}
            </Link>
          </p>
          {d.subscriptions.length === 0 ? (
            <p className="text-sm text-zinc-500">{t('packs2.eventsEmpty')}</p>
          ) : (
            <ul className="divide-y divide-zinc-200 rounded-md border border-zinc-200 dark:divide-zinc-800 dark:border-zinc-800">
              {d.subscriptions.map((sub) => (
                <PackSubscriptionRow
                  key={sub.subscription_id}
                  sub={sub}
                  workspaceId={workspaceId}
                  fmtDate={fmtDate}
                />
              ))}
            </ul>
          )}
          <PackEventsCatalog workspaceId={workspaceId} />
        </div>
      </SectionBlock>

      {/* ④ 界面：控制台入口 + 工单 tab slots */}
      <SectionBlock title={t('packs2.uiTitle')}>
        {d.ui_entry ? (
          <div className="space-y-2 text-sm">
            <p className="flex flex-wrap items-baseline gap-2">
              <span className="text-xs text-zinc-500 dark:text-zinc-400">
                {t('packs2.uiConsoleLabel')}
              </span>
              <span className="font-medium">{d.ui_entry.label}</span>
              <code className="block w-full truncate rounded bg-zinc-50 px-2 py-1 font-mono text-xs dark:bg-zinc-800/60">
                {d.ui_entry.url_template}
              </code>
            </p>
            {d.ui_entry.slots.length > 0 && (
              <div>
                <p className="mb-1 text-xs text-zinc-500 dark:text-zinc-400">
                  {t('packs2.uiSlotsLabel')}
                </p>
                <ul className="space-y-1">
                  {d.ui_entry.slots.map((slot) => (
                    <li
                      key={slot.slot}
                      className="flex items-center gap-2 text-sm"
                      data-testid="pack-slot-row"
                    >
                      <code className="rounded bg-zinc-50 px-1.5 py-0.5 font-mono text-xs dark:bg-zinc-800/60">
                        {slot.slot}
                      </code>
                      <span>{slot.label}</span>
                      <span className="text-xs text-zinc-400">
                        {t('packs2.uiSlotHeight', slot)}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        ) : (
          <p className="text-sm text-zinc-500">{t('packs2.uiEmpty')}</p>
        )}
      </SectionBlock>

      {/* ⑤ 资产（只读）：agents 含 archived 灰显 + skills 名单 */}
      <SectionBlock title={t('packs2.assetsTitle')}>
        <div className="grid gap-4 sm:grid-cols-2">
          <div>
            <p className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-zinc-500 dark:text-zinc-400">
              {t('packs2.agentsLabel', { n: d.agents.length })}
            </p>
            {d.agents.length === 0 ? (
              <p className="text-sm text-zinc-500">{t('packs2.agentsEmpty')}</p>
            ) : (
              <ul className="space-y-1">
                {d.agents.map((a) => (
                  <li
                    key={a.id}
                    className={a.archived ? 'text-sm text-zinc-400' : 'text-sm'}
                    data-testid="pack-agent-row"
                  >
                    <span className="flex items-center gap-1.5">
                      <Link
                        to="/agents/$agentId"
                        params={{ agentId: a.id }}
                        className="inline-flex items-center gap-1.5 rounded outline-none hover:underline focus-visible:ring-2 focus-visible:ring-zinc-400"
                        aria-label={t('packs2.viewAgent', { name: a.name })}
                      >
                        {a.name}
                        {a.archived && (
                          <span className="text-xs">{t('packs2.agentArchivedSuffix')}</span>
                        )}
                      </Link>
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <div>
            <p className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-zinc-500 dark:text-zinc-400">
              {t('packs2.skillsLabel', { n: d.skills.length })}
            </p>
            {d.skills.length === 0 ? (
              <p className="text-sm text-zinc-500">{t('packs2.skillsEmpty')}</p>
            ) : (
              <ul className="space-y-1">
                {d.skills.map((sk) => (
                  <li key={sk.id} className="text-sm" data-testid="pack-skill-row">
                    <span className="flex items-center gap-1.5">
                      <Link
                        to="/skills/$skillId"
                        params={{ skillId: sk.id }}
                        className="inline-flex rounded outline-none hover:underline focus-visible:ring-2 focus-visible:ring-zinc-400"
                        aria-label={t('packs2.viewSkill', { name: sk.name })}
                      >
                        {sk.name}
                      </Link>
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      </SectionBlock>

      {/* 卸载（P1 占位：只展示 CLI 命令，不调接口） */}
      <Dialog
        open={uninstallOpen}
        onOpenChange={(next) => (!next ? setUninstallOpen(false) : undefined)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('packs2.uninstallTitle')}</DialogTitle>
            <DialogDescription>{t('packs2.uninstallDesc')}</DialogDescription>
          </DialogHeader>
          <code
            className="block overflow-x-auto rounded-md border border-zinc-200 bg-zinc-50 px-3 py-2 font-mono text-xs whitespace-nowrap dark:border-zinc-700 dark:bg-zinc-900"
            data-testid="pack-uninstall-cmd"
          >
            {t('packs2.uninstallCmd', { workspaceId: workspaceId ?? '<id>' })}
          </code>
          <div className="flex justify-end">
            <Button variant="outline" onClick={() => setUninstallOpen(false)}>
              {t('common.ok')}
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      {/* 使用文档 Dialog（平台化 P2）：打开才挂载，查询随 Dialog 生命周期 */}
      {usageOpen && (
        <PackUsageDialog
          packId={d.pack.id}
          packName={d.pack.name}
          onClose={() => setUsageOpen(false)}
        />
      )}
    </div>
  )
}

/** 订阅行 ping 按钮的三态：空闲 / 进行中 / 结果短暂标注（成功/失败）。 */
type PingState = 'idle' | 'running' | 'ok' | 'fail'

/**
 * 区块③的单订阅行：URL + 描述/创建日期 + 事件类型徽标 + 管理动作
 * （ping / 轮换密钥 / 暂停恢复 / 投递日志）。动作均 admin 权限，member
 * 403 时只做行内错误标注（与页面"不隐藏权限"的现有做法一致）。
 */
function PackSubscriptionRow({
  sub,
  workspaceId,
  fmtDate,
}: {
  sub: PackDetail['subscriptions'][number]
  workspaceId: string | null
  fmtDate: (iso: string) => string
}) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const ws = workspaceId ?? ''
  const refresh = () => void qc.invalidateQueries({ queryKey: ['pack'] })
  const [actionErr, setActionErr] = useState<string | null>(null)
  const [ping, setPing] = useState<PingState>('idle')
  const [rotateOpen, setRotateOpen] = useState(false)
  const [rotatedSecret, setRotatedSecret] = useState<string | null>(null)
  const [deliveriesOpen, setDeliveriesOpen] = useState(false)

  const doPing = async () => {
    if (!workspaceId || ping === 'running') return
    setPing('running')
    try {
      await pingWebhookSubscription(ws, sub.subscription_id)
      setPing('ok')
    } catch {
      setPing('fail')
      setActionErr(t('packs2.subActionFailed'))
    } finally {
      // 结果只在按钮上短暂停留，随后回到空闲态
      setTimeout(() => setPing('idle'), 2000)
    }
  }

  const doRotate = useMutation({
    mutationFn: () => rotateWebhookSecret(ws, sub.subscription_id),
    onSuccess: (res) => {
      setRotatedSecret(res.secret)
      setActionErr(null)
    },
    onError: () => setActionErr(t('packs2.subActionFailed')),
  })

  const doToggle = useMutation({
    mutationFn: () => setWebhookSubscriptionActive(ws, sub.subscription_id, !sub.is_active),
    onSuccess: () => {
      setActionErr(null)
      refresh()
    },
    onError: () => setActionErr(t('packs2.subActionFailed')),
  })

  return (
    <li
      className="space-y-1.5 px-3 py-2.5 text-sm"
      data-testid="pack-sub-row"
      data-subscription-id={sub.subscription_id}
    >
      <p className="flex flex-wrap items-center gap-2">
        <code className="min-w-0 truncate font-mono text-xs">{sub.url}</code>
        <Badge variant={sub.is_active ? 'success' : 'neutral'} className="ml-auto shrink-0">
          {t(sub.is_active ? 'packs2.eventActive' : 'packs2.eventInactive')}
        </Badge>
      </p>
      {sub.description && <p className="text-xs text-zinc-500 dark:text-zinc-400">{sub.description}</p>}
      {sub.created_at && (
        <p className="text-xs text-zinc-400">{t('packs2.subCreatedAt', { date: fmtDate(sub.created_at) })}</p>
      )}
      <p className="flex flex-wrap gap-1">
        {sub.event_types.map((et) => (
          <Badge key={et} variant="neutral">
            {et}
          </Badge>
        ))}
      </p>
      <p className="flex flex-wrap items-center gap-1.5">
        <Button
          size="xs"
          variant="outline"
          disabled={!workspaceId || ping === 'running'}
          data-testid="pack-sub-ping"
          onClick={() => void doPing()}
        >
          <HeartPulse className="size-3" aria-hidden="true" />
          {ping === 'running'
            ? t('packs2.subPinging')
            : ping === 'ok'
              ? t('packs2.subPingOk')
              : ping === 'fail'
                ? t('packs2.subPingFail')
                : t('packs2.subPing')}
        </Button>
        <Button
          size="xs"
          variant="outline"
          disabled={!workspaceId || doRotate.isPending}
          data-testid="pack-sub-rotate"
          onClick={() => {
            setRotatedSecret(null)
            setRotateOpen(true)
            doRotate.mutate()
          }}
        >
          <RotateCw className="size-3" aria-hidden="true" />
          {t('packs2.subRotate')}
        </Button>
        <Button
          size="xs"
          variant="outline"
          disabled={!workspaceId || doToggle.isPending}
          data-testid="pack-sub-toggle"
          onClick={() => doToggle.mutate()}
        >
          {sub.is_active ? (
            <>
              <Pause className="size-3" aria-hidden="true" />
              {t('packs2.subPause')}
            </>
          ) : (
            <>
              <Play className="size-3" aria-hidden="true" />
              {t('packs2.subResume')}
            </>
          )}
        </Button>
        <Button
          size="xs"
          variant="ghost"
          data-testid="pack-sub-deliveries"
          onClick={() => setDeliveriesOpen(true)}
        >
          {t('packs2.subDeliveries')}
        </Button>
        {actionErr && (
          <span className="text-xs text-red-600 dark:text-red-400" data-testid="pack-sub-error">
            {actionErr}
          </span>
        )}
      </p>
      {/* 轮换密钥：两段式——未拿到新密钥前是确认警示，拿到后展示一次性明文 */}
      <Dialog open={rotateOpen} onOpenChange={(next) => (!next ? setRotateOpen(false) : undefined)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('packs2.rotateTitle')}</DialogTitle>
            <DialogDescription>
              {rotatedSecret ? t('packs2.rotateOnceDesc') : t('packs2.rotateConfirmDesc')}
            </DialogDescription>
          </DialogHeader>
          {rotatedSecret ? (
            <div className="flex items-center gap-2" data-testid="pack-rotate-secret">
              <code className="min-w-0 flex-1 overflow-x-auto rounded-md border border-zinc-200 bg-zinc-50 px-3 py-2 font-mono text-xs whitespace-nowrap dark:border-zinc-700 dark:bg-zinc-900">
                {rotatedSecret}
              </code>
              <Button
                size="icon-sm"
                variant="outline"
                aria-label={t('common.copy')}
                onClick={() => void navigator.clipboard.writeText(rotatedSecret)}
              >
                <Copy className="size-3.5" aria-hidden="true" />
              </Button>
            </div>
          ) : (
            <p className="flex items-center gap-1.5 text-xs text-amber-700 dark:text-amber-400">
              <Radio className="size-3.5 shrink-0" aria-hidden="true" />
              {doRotate.isPending ? t('packs2.rotateRotating') : t('packs2.rotateWarning')}
            </p>
          )}
          <div className="flex justify-end">
            <Button variant="outline" onClick={() => setRotateOpen(false)}>
              {t('common.close')}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
      {deliveriesOpen && (
        <PackDeliveriesDialog
          workspaceId={workspaceId}
          subscriptionId={sub.subscription_id}
          fmtDate={fmtDate}
          onClose={() => setDeliveriesOpen(false)}
        />
      )}
    </li>
  )
}

/** 投递状态 → 徽标色：delivered 绿 / pending 黄 / dead 红，其余中性。 */
function deliveryStatusVariant(status: string): 'success' | 'warning' | 'danger' | 'neutral' {
  if (status === 'delivered') return 'success'
  if (status === 'pending') return 'warning'
  if (status === 'dead') return 'danger'
  return 'neutral'
}

/** 投递日志 Dialog：按当前订阅过滤，dead/pending(重试过) 行可重放。 */
function PackDeliveriesDialog({
  workspaceId,
  subscriptionId,
  fmtDate,
  onClose,
}: {
  workspaceId: string | null
  subscriptionId: string
  fmtDate: (iso: string) => string
  onClose: () => void
}) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const deliveries = useQuery({
    queryKey: ['webhook-deliveries', workspaceId],
    queryFn: () => listWebhookDeliveries(workspaceId as string),
    enabled: !!workspaceId,
  })
  const redeliver = useMutation({
    mutationFn: (id: string) => redeliverWebhookDelivery(workspaceId as string, id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['webhook-deliveries', workspaceId] }),
  })
  const rows = (deliveries.data ?? []).filter((dl) => dl.subscription_id === subscriptionId)
  // 可重放：已死，或仍在重试但已尝试超过一次
  const canRedeliver = (dl: WebhookDelivery) => dl.status === 'dead' || (dl.status === 'pending' && dl.attempts > 1)
  return (
    <Dialog open onOpenChange={(next) => (!next ? onClose() : undefined)}>
      <DialogContent className="max-h-[80vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('packs2.deliveriesTitle')}</DialogTitle>
          <DialogDescription>{t('packs2.deliveriesDesc', { n: rows.length })}</DialogDescription>
        </DialogHeader>
        {deliveries.isPending ? (
          <p className="text-sm text-zinc-500">{t('packs2.deliveriesLoading')}</p>
        ) : deliveries.isError ? (
          <p className="text-sm text-red-600 dark:text-red-400">
            {t('packs2.deliveriesFailed')}
          </p>
        ) : rows.length === 0 ? (
          <p className="text-sm text-zinc-500">{t('packs2.deliveriesEmpty')}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs" data-testid="pack-deliveries-table">
              <thead>
                <tr className="border-b border-zinc-200 text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
                  <th className="py-1.5 pr-3 font-medium">{t('packs2.deliveriesColEvent')}</th>
                  <th className="py-1.5 pr-3 font-medium">{t('packs2.deliveriesColStatus')}</th>
                  <th className="py-1.5 pr-3 font-medium">{t('packs2.deliveriesColAttempts')}</th>
                  <th className="py-1.5 pr-3 font-medium">{t('packs2.deliveriesColCode')}</th>
                  <th className="py-1.5 pr-3 font-medium">{t('packs2.deliveriesColError')}</th>
                  <th className="py-1.5 pr-3 font-medium">{t('packs2.deliveriesColTime')}</th>
                  <th className="py-1.5 font-medium" aria-label={t('common.actions')} />
                </tr>
              </thead>
              <tbody>
                {rows.map((dl) => (
                  <tr
                    key={dl.id}
                    className="border-b border-zinc-100 align-top last:border-b-0 dark:border-zinc-800/60"
                    data-testid="pack-delivery-row"
                  >
                    <td className="py-1.5 pr-3 font-mono">{dl.event_type}</td>
                    <td className="py-1.5 pr-3">
                      <Badge variant={deliveryStatusVariant(dl.status)}>{dl.status}</Badge>
                    </td>
                    <td className="py-1.5 pr-3">{dl.attempts}</td>
                    <td className="py-1.5 pr-3 font-mono">{dl.last_status_code ?? '–'}</td>
                    <td className="max-w-40 truncate py-1.5 pr-3 text-zinc-500 dark:text-zinc-400" title={dl.last_error ?? undefined}>
                      {dl.last_error || '–'}
                    </td>
                    <td className="py-1.5 pr-3 whitespace-nowrap text-zinc-500 dark:text-zinc-400">
                      {fmtDate(dl.delivered_at ?? dl.created_at)}
                    </td>
                    <td className="py-1.5">
                      {canRedeliver(dl) && (
                        <Button
                          size="xs"
                          variant="ghost"
                          disabled={redeliver.isPending}
                          data-testid="pack-delivery-redeliver"
                          onClick={() => redeliver.mutate(dl.id)}
                        >
                          <RotateCw className="size-3" aria-hidden="true" />
                          {t('packs2.deliveriesRedeliver')}
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div className="flex justify-end">
          <Button variant="outline" onClick={onClose}>
            {t('common.close')}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}

/** 事件目录：底部一行可展开，平铺全部可订阅事件类型（member 可读）。 */
function PackEventsCatalog({ workspaceId }: { workspaceId: string | null }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const events = useQuery({
    queryKey: ['webhook-events', workspaceId],
    queryFn: () => listWebhookEvents(workspaceId as string),
    enabled: open && !!workspaceId,
  })
  return (
    <div className="pt-1">
      <Button
        size="sm"
        variant="ghost"
        aria-expanded={open}
        data-testid="pack-events-catalog"
        disabled={!workspaceId}
        onClick={() => setOpen((v) => !v)}
      >
        {open ? (
          <ChevronDown className="size-3.5 text-zinc-400" aria-hidden="true" />
        ) : (
          <ChevronRight className="size-3.5 text-zinc-400" aria-hidden="true" />
        )}
        {t('packs2.catalogToggle')}
      </Button>
      {open && (
        <div className="px-3 pb-2">
          {events.isPending ? (
            <p className="text-sm text-zinc-500">{t('packs2.catalogLoading')}</p>
          ) : events.isError ? (
            <p className="text-sm text-red-600 dark:text-red-400">{t('packs2.catalogFailed')}</p>
          ) : (events.data ?? []).length === 0 ? (
            <p className="text-sm text-zinc-500">{t('packs2.catalogEmpty')}</p>
          ) : (
            <dl className="grid gap-x-6 gap-y-1.5 text-xs sm:grid-cols-2">
              {events.data!.map((ev) => (
                <div key={ev.type} className="flex items-baseline gap-2" data-testid="pack-event-def">
                  <dt className="shrink-0">
                    <Badge variant="neutral" className="font-mono">
                      {ev.type}
                    </Badge>
                  </dt>
                  <dd className="min-w-0 text-zinc-500 dark:text-zinc-400">{ev.description}</dd>
                </div>
              ))}
            </dl>
          )}
        </div>
      )}
    </div>
  )
}

/** 区块②的单服务器行：探针展开按钮 + 复用 ServerToolsPanel + 头部探活标注。 */
function PackServerProbeRow({
  server,
  workspaceId,
  registryId,
  probeStatus,
}: {
  server: PackDetail['servers'][number]
  workspaceId: string | null
  registryId: string | undefined
  probeStatus: 'ok' | 'fail' | undefined
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <li
      className="rounded-md border border-zinc-200 dark:border-zinc-800"
      data-testid="pack-server-row"
    >
      <div className="flex items-center gap-2 px-3 py-2">
        <span className="min-w-0 flex-1 truncate text-sm font-medium">{server.name}</span>
        <Badge variant="neutral">{server.transport}</Badge>
        {probeStatus && (
          <Badge variant={probeStatus === 'ok' ? 'success' : 'danger'}>
            {t(probeStatus === 'ok' ? 'packs2.probeHealthy' : 'packs2.probeDown')}
          </Badge>
        )}
        <Button
          size="sm"
          variant="ghost"
          aria-expanded={open}
          disabled={!workspaceId || !registryId}
          onClick={() => setOpen((v) => !v)}
        >
          {open ? t('packs2.liveProbeHide') : t('packs2.liveProbeAction')}
          <ChevronDown
            className={
              'size-3.5 shrink-0 text-zinc-400 transition-transform' + (open ? ' rotate-180' : '')
            }
            aria-hidden="true"
          />
        </Button>
      </div>
      {open && workspaceId && registryId && (
        <div className="border-t border-zinc-200 pt-2 dark:border-zinc-800">
          <ServerToolsPanel workspaceId={workspaceId} serverId={registryId} i18nPrefix="packs2" />
        </div>
      )}
    </li>
  )
}

/** 区块通用骨架：小节标题 + 内边距容器。 */
function SectionBlock({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section
      className="rounded-lg border border-zinc-200 p-4 dark:border-zinc-800"
      aria-label={title}
    >
      <h2 className="mb-3 text-sm font-semibold">{title}</h2>
      {children}
    </section>
  )
}

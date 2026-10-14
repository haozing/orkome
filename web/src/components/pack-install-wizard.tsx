import { useMutation, useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { ArrowLeft, Copy, Plus, Radio, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  installPack,
  type PackInstallEvent,
  type PackInstallMcpServer,
  type PackInstallSpec,
  type PackInstallSubscriptionSecret,
} from '@/api/packs'
import { listWebhookEvents } from '@/api/webhooks'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useWorkspaceStore } from '@/stores/workspace'

// 安装向导（能力包平台化 P2）：单页分区表单（非步骤条），提交 POST
// /api/packs/install。三段 mcp/events/ui 均可空——空段序列化时整个省略
// （与"字段缺失"语义一致，而非传空数组占位）。成功后若有非空 secret 的
// 订阅，先进一次性密钥 Dialog（交互照 pack-detail 的轮换密钥），关闭后
// 跳详情页；无 secret 直接跳。
// 表单结构范本抄 packs-sections.tsx WorkspaceMcpDialog：受控对象 + set
// helper + 手写校验 + save.isPending；独立成文件的原因同 packs-page：
// router 的 autoCodeSplitting 会拆走路由模块的非 Route 导出，页面级
// 组件测试须从组件文件引用。

// transport 选项照抄 packs-sections.tsx 的 TRANSPORTS（契约："照抄选项"；
// wire 另有 streamable_http 形态，后端解析时归一到 http，这里不引入新值）。
const TRANSPORTS = ['http', 'sse'] as const

/** slot 目录仍冻结为 workitem.detail.tab（packs.ts PackUISlot 注释同参）。 */
const SLOT_KINDS = ['workitem.detail.tab'] as const

/** MCP 服务器行状态（credential 开关展开后才提交 provider/secret）。 */
interface McpRowState {
  name: string
  transport: string
  url: string
  needCred: boolean
  provider: string
  secret: string
}

/** 事件订阅行状态（types 用事件目录 checkbox 组多选）。 */
interface EventRowState {
  url: string
  types: string[]
  description: string
}

/** slot 行状态。 */
interface SlotRowState {
  slot: string
  label: string
  url_template: string
  height: string
}

const newMcpRow = (): McpRowState => ({
  name: '',
  transport: 'http',
  url: '',
  needCred: false,
  provider: '',
  secret: '',
})
const newEventRow = (): EventRowState => ({ url: '', types: [], description: '' })
const newSlotRow = (): SlotRowState => ({
  slot: SLOT_KINDS[0],
  label: '',
  url_template: '',
  height: '480',
})

export function PackInstallWizardView() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const workspaceId = useWorkspaceStore((s) => s.currentId)
  const [form, setForm] = useState({
    name: '',
    version: '0.1.0',
  })
  const set = <K extends keyof typeof form>(key: K, value: (typeof form)[K]) =>
    setForm((f) => ({ ...f, [key]: value }))
  const [mcpRows, setMcpRows] = useState<McpRowState[]>([])
  const [eventRows, setEventRows] = useState<EventRowState[]>([])
  const [uiOpen, setUiOpen] = useState(false)
  const [ui, setUi] = useState({ label: '', url_template: '' })
  const setUiField = <K extends keyof typeof ui>(key: K, value: (typeof ui)[K]) =>
    setUi((u) => ({ ...u, [key]: value }))
  const [slotRows, setSlotRows] = useState<SlotRowState[]>([])

  const setMcp = (i: number, patch: Partial<McpRowState>) =>
    setMcpRows((rows) => rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  const setEvent = (i: number, patch: Partial<EventRowState>) =>
    setEventRows((rows) => rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  const setSlot = (i: number, patch: Partial<SlotRowState>) =>
    setSlotRows((rows) => rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))

  // 事件目录（GET /api/webhooks 的 events 端点，member 可读）拉来做多选组。
  const eventsCatalog = useQuery({
    queryKey: ['webhook-events', workspaceId],
    queryFn: () => listWebhookEvents(workspaceId as string),
    enabled: eventRows.length > 0 && !!workspaceId,
  })

  // 基本校验沿用 WorkspaceMcpDialog 的手写风格：name 必填；动态行只在
  // 非空行参与校验（url 空的行视为未填写，提交时整行忽略——比硬性报错温和）。
  const [validation, setValidation] = useState<string | null>(null)
  const save = useMutation({
    mutationFn: () => {
      // 序列化：mcp/events 空段省略（不传空数组）；credential 仅在开关
      // 打开且 secret 非空时携带（开关开但 secret 空 = 不带凭证提交）。
      const mcp: PackInstallMcpServer[] = mcpRows
        .filter((r) => r.name.trim() && r.url.trim())
        .map((r) => {
          const server: PackInstallMcpServer = {
            name: r.name.trim(),
            transport: r.transport,
            url: r.url.trim(),
          }
          if (r.needCred && r.secret.trim()) {
            server.credential = { provider: r.provider.trim(), secret: r.secret.trim() }
          }
          return server
        })
      const events: PackInstallEvent[] = eventRows
        .filter((r) => r.url.trim() && r.types.length > 0)
        .map((r) => ({
          url: r.url.trim(),
          types: r.types,
          description: r.description.trim() || undefined,
        }))
      const spec: PackInstallSpec = { name: form.name.trim(), version: form.version.trim() }
      if (mcp.length > 0) spec.mcp = mcp
      if (events.length > 0) spec.events = events
      if (uiOpen && ui.url_template.trim()) {
        spec.ui = {
          label: ui.label.trim(),
          url_template: ui.url_template.trim(),
          slots: slotRows
            .filter((r) => r.url_template.trim())
            .map((r) => ({
              slot: r.slot,
              label: r.label.trim(),
              url_template: r.url_template.trim(),
              height: Number(r.height) || 480,
            })),
        }
      }
      return installPack(spec)
    },
    onSuccess: (res) => {
      setValidation(null)
      // 有非空 secret 才进一次性展示；否则直接跳详情（契约：secret 可能
      // 为空，跳过空密钥避免展示空洞面板）。
      const withSecret = (res.subscriptions ?? []).filter(
        (s: PackInstallSubscriptionSecret) => s.secret,
      )
      if (withSecret.length > 0) {
        setSecrets(withSecret)
      } else {
        void navigate({ to: '/packs/$packId', params: { packId: res.pack.id } })
      }
    },
  })

  const [secrets, setSecrets] = useState<PackInstallSubscriptionSecret[] | null>(null)
  const closeSecrets = () => {
    const packId = save.data?.pack.id
    setSecrets(null)
    if (packId) void navigate({ to: '/packs/$packId', params: { packId } })
  }

  const submit = () => {
    if (save.isPending) return
    // 手写校验：name 必填；事件订阅行若填了 url 但没勾 types 则拦下。
    if (!form.name.trim()) {
      setValidation(t('packsInstall.nameLabel'))
      return
    }
    if (eventRows.some((r) => r.url.trim() && r.types.length === 0)) {
      setValidation(t('packsInstall.eventTypesLabel'))
      return
    }
    setValidation(null)
    save.mutate()
  }

  const sectionTitle = 'text-sm font-semibold'
  const fieldLabel = 'mb-1 block text-xs text-zinc-500'
  const selectCls =
    'h-9 w-full rounded-md border border-zinc-300 bg-white px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-zinc-400 dark:border-zinc-700 dark:bg-zinc-900'

  return (
    <div className="space-y-5">
      {/* 头部：返回 + 标题 */}
      <div className="flex items-center gap-2">
        <Link
          to="/packs"
          className="flex size-7 items-center justify-center rounded-md text-zinc-400 outline-none hover:bg-zinc-100 hover:text-zinc-600 dark:hover:bg-zinc-800 dark:hover:text-zinc-300"
          aria-label={t('packsInstall.backToList')}
        >
          <ArrowLeft className="size-4" aria-hidden="true" />
        </Link>
        <h1 className="text-lg font-semibold">{t('packsInstall.wizardTitle')}</h1>
      </div>

      <form
        className="space-y-5"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        {/* 基本信息 */}
        <section
          className="space-y-3 rounded-lg border border-zinc-200 p-4 dark:border-zinc-800"
          aria-label={t('packsInstall.basicTitle')}
        >
          <h2 className={sectionTitle}>{t('packsInstall.basicTitle')}</h2>
          <div className="grid gap-3 sm:grid-cols-2">
            <div>
              <label htmlFor="pack-install-name" className={fieldLabel}>
                {t('packsInstall.nameLabel')}
              </label>
              <Input
                id="pack-install-name"
                value={form.name}
                onChange={(e) => set('name', e.target.value)}
                autoFocus
                placeholder="agentblog"
                data-testid="pack-install-name"
              />
            </div>
            <div>
              <label htmlFor="pack-install-version" className={fieldLabel}>
                {t('packsInstall.versionLabel')}
              </label>
              <Input
                id="pack-install-version"
                value={form.version}
                onChange={(e) => set('version', e.target.value)}
                placeholder="0.1.0"
                data-testid="pack-install-version"
              />
            </div>
          </div>
        </section>

        {/* MCP 服务器：动态行 */}
        <section
          className="space-y-3 rounded-lg border border-zinc-200 p-4 dark:border-zinc-800"
          aria-label={t('packsInstall.mcpTitle')}
        >
          <div className="flex items-center justify-between gap-2">
            <div>
              <h2 className={sectionTitle}>{t('packsInstall.mcpTitle')}</h2>
              <p className="text-xs text-zinc-500 dark:text-zinc-400">
                {t('packsInstall.mcpHint')}
              </p>
            </div>
            <Button
              type="button"
              size="sm"
              variant="outline"
              data-testid="pack-install-mcp-add"
              onClick={() => setMcpRows((rows) => [...rows, newMcpRow()])}
            >
              <Plus className="size-3.5" aria-hidden="true" />
              {t('packsInstall.mcpAdd')}
            </Button>
          </div>
          {mcpRows.length === 0 && (
            <p className="text-sm text-zinc-500" data-testid="pack-install-mcp-empty">
              {t('packsInstall.mcpEmpty')}
            </p>
          )}
          <ul className="space-y-3">
            {mcpRows.map((row, i) => (
              <li
                key={i}
                className="space-y-2 rounded-md border border-zinc-200 p-3 dark:border-zinc-800"
                data-testid="pack-install-mcp-row"
              >
                <div className="grid gap-2 sm:grid-cols-3">
                  <div>
                    <label htmlFor={`pack-install-mcp-name-${i}`} className={fieldLabel}>
                      {t('packsInstall.serverNameLabel')}
                    </label>
                    <Input
                      id={`pack-install-mcp-name-${i}`}
                      value={row.name}
                      onChange={(e) => setMcp(i, { name: e.target.value })}
                      placeholder="web-search"
                      data-testid="pack-install-mcp-name"
                    />
                  </div>
                  <div>
                    <label htmlFor={`pack-install-mcp-transport-${i}`} className={fieldLabel}>
                      {t('packsInstall.serverTransportLabel')}
                    </label>
                    <select
                      id={`pack-install-mcp-transport-${i}`}
                      value={row.transport}
                      onChange={(e) => setMcp(i, { transport: e.target.value })}
                      className={selectCls}
                    >
                      {TRANSPORTS.map((tr) => (
                        <option key={tr} value={tr}>
                          {tr}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div>
                    <label htmlFor={`pack-install-mcp-url-${i}`} className={fieldLabel}>
                      {t('packsInstall.serverUrlLabel')}
                    </label>
                    <Input
                      id={`pack-install-mcp-url-${i}`}
                      value={row.url}
                      onChange={(e) => setMcp(i, { url: e.target.value })}
                      placeholder="https://mcp.example.com/mcp"
                      data-testid="pack-install-mcp-url"
                    />
                  </div>
                </div>
                <div className="space-y-2">
                  <label className="flex cursor-pointer items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400">
                    <input
                      type="checkbox"
                      checked={row.needCred}
                      onChange={(e) => setMcp(i, { needCred: e.target.checked })}
                      className="size-3 accent-zinc-700"
                      data-testid="pack-install-mcp-cred-toggle"
                    />
                    {t('packsInstall.credToggle')}
                  </label>
                  {row.needCred && (
                    <div className="grid gap-2 sm:grid-cols-2">
                      <div>
                        <label htmlFor={`pack-install-mcp-provider-${i}`} className={fieldLabel}>
                          {t('packsInstall.credProviderLabel')}
                        </label>
                        <Input
                          id={`pack-install-mcp-provider-${i}`}
                          value={row.provider}
                          onChange={(e) => setMcp(i, { provider: e.target.value })}
                          placeholder="TOKEN_KEY"
                          data-testid="pack-install-mcp-provider"
                        />
                      </div>
                      <div>
                        <label htmlFor={`pack-install-mcp-secret-${i}`} className={fieldLabel}>
                          {t('packsInstall.credSecretLabel')}
                        </label>
                        <Input
                          id={`pack-install-mcp-secret-${i}`}
                          type="password"
                          value={row.secret}
                          onChange={(e) => setMcp(i, { secret: e.target.value })}
                          placeholder="sk-…"
                          data-testid="pack-install-mcp-secret"
                        />
                      </div>
                    </div>
                  )}
                </div>
                <div className="flex justify-end">
                  <Button
                    type="button"
                    size="xs"
                    variant="ghost"
                    className="text-red-600 dark:text-red-400"
                    aria-label={t('packsInstall.removeServer')}
                    data-testid="pack-install-mcp-remove"
                    onClick={() => setMcpRows((rows) => rows.filter((_, idx) => idx !== i))}
                  >
                    <Trash2 className="size-3" aria-hidden="true" />
                    {t('packsInstall.removeServer')}
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        </section>

        {/* 事件订阅：动态行 + 事件目录 checkbox 组 */}
        <section
          className="space-y-3 rounded-lg border border-zinc-200 p-4 dark:border-zinc-800"
          aria-label={t('packsInstall.eventsTitle')}
        >
          <div className="flex items-center justify-between gap-2">
            <div>
              <h2 className={sectionTitle}>{t('packsInstall.eventsTitle')}</h2>
              <p className="text-xs text-zinc-500 dark:text-zinc-400">
                {t('packsInstall.eventsHint')}
              </p>
            </div>
            <Button
              type="button"
              size="sm"
              variant="outline"
              data-testid="pack-install-event-add"
              onClick={() => setEventRows((rows) => [...rows, newEventRow()])}
            >
              <Plus className="size-3.5" aria-hidden="true" />
              {t('packsInstall.eventAdd')}
            </Button>
          </div>
          {eventRows.length === 0 && (
            <p className="text-sm text-zinc-500" data-testid="pack-install-event-empty">
              {t('packsInstall.eventEmpty')}
            </p>
          )}
          <ul className="space-y-3">
            {eventRows.map((row, i) => (
              <li
                key={i}
                className="space-y-2 rounded-md border border-zinc-200 p-3 dark:border-zinc-800"
                data-testid="pack-install-event-row"
              >
                <div className="grid gap-2 sm:grid-cols-[1fr_2fr]">
                  <div>
                    <label htmlFor={`pack-install-event-url-${i}`} className={fieldLabel}>
                      {t('packsInstall.eventUrlLabel')}
                    </label>
                    <Input
                      id={`pack-install-event-url-${i}`}
                      value={row.url}
                      onChange={(e) => setEvent(i, { url: e.target.value })}
                      placeholder="https://vendor.example.com/hooks"
                      data-testid="pack-install-event-url"
                    />
                  </div>
                  <div>
                    <label htmlFor={`pack-install-event-desc-${i}`} className={fieldLabel}>
                      {t('packsInstall.eventDescLabel')}
                    </label>
                    <Input
                      id={`pack-install-event-desc-${i}`}
                      value={row.description}
                      onChange={(e) => setEvent(i, { description: e.target.value })}
                      placeholder="pack x"
                      data-testid="pack-install-event-desc"
                    />
                  </div>
                </div>
                <div>
                  <p className={fieldLabel}>{t('packsInstall.eventTypesLabel')}</p>
                  {eventsCatalog.isPending && (
                    <p className="text-xs text-zinc-500">{t('packsInstall.catalogLoading')}</p>
                  )}
                  {eventsCatalog.isError && (
                    <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                      {t('packsInstall.catalogFailed')}
                    </p>
                  )}
                  {eventsCatalog.data && eventsCatalog.data.length === 0 && (
                    <p className="text-xs text-zinc-500">{t('packsInstall.catalogEmpty')}</p>
                  )}
                  {eventsCatalog.data && eventsCatalog.data.length > 0 && (
                    <div className="flex flex-wrap gap-x-4 gap-y-1.5">
                      {eventsCatalog.data.map((ev) => (
                        <label
                          key={ev.type}
                          className="flex cursor-pointer items-center gap-1.5 text-xs"
                          data-testid="pack-install-event-type"
                        >
                          <input
                            type="checkbox"
                            checked={row.types.includes(ev.type)}
                            onChange={(e) =>
                              setEvent(i, {
                                types: e.target.checked
                                  ? [...row.types, ev.type]
                                  : row.types.filter((ty) => ty !== ev.type),
                              })
                            }
                            className="size-3 accent-zinc-700"
                          />
                          <code className="font-mono">{ev.type}</code>
                        </label>
                      ))}
                    </div>
                  )}
                  {row.types.length > 0 && (
                    <p className="mt-1 flex flex-wrap gap-1">
                      {row.types.map((ty) => (
                        <Badge key={ty} variant="neutral" className="font-mono">
                          {ty}
                        </Badge>
                      ))}
                    </p>
                  )}
                </div>
                <div className="flex justify-end">
                  <Button
                    type="button"
                    size="xs"
                    variant="ghost"
                    className="text-red-600 dark:text-red-400"
                    aria-label={t('packsInstall.removeEvent')}
                    data-testid="pack-install-event-remove"
                    onClick={() => setEventRows((rows) => rows.filter((_, idx) => idx !== i))}
                  >
                    <Trash2 className="size-3" aria-hidden="true" />
                    {t('packsInstall.removeEvent')}
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        </section>

        {/* 界面进驻：开关展开 */}
        <section
          className="space-y-3 rounded-lg border border-zinc-200 p-4 dark:border-zinc-800"
          aria-label={t('packsInstall.uiToggle')}
        >
          <label className="flex cursor-pointer items-center gap-1.5 text-sm font-semibold">
            <input
              type="checkbox"
              checked={uiOpen}
              onChange={(e) => setUiOpen(e.target.checked)}
              className="size-3.5 accent-zinc-700"
              data-testid="pack-install-ui-toggle"
            />
            {t('packsInstall.uiToggle')}
          </label>
          {uiOpen && (
            <div className="space-y-3">
              <div className="grid gap-3 sm:grid-cols-2">
                <div>
                  <label htmlFor="pack-install-ui-label" className={fieldLabel}>
                    {t('packsInstall.uiLabel')}
                  </label>
                  <Input
                    id="pack-install-ui-label"
                    value={ui.label}
                    onChange={(e) => setUiField('label', e.target.value)}
                    placeholder="控制台"
                  />
                </div>
                <div>
                  <label htmlFor="pack-install-ui-url" className={fieldLabel}>
                    {t('packsInstall.uiUrlTemplateLabel')}
                  </label>
                  <Input
                    id="pack-install-ui-url"
                    value={ui.url_template}
                    onChange={(e) => setUiField('url_template', e.target.value)}
                    placeholder="https://…?ws={workspaceId}&t={uiToken}"
                  />
                </div>
              </div>
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <p className="text-xs font-medium text-zinc-500 dark:text-zinc-400">
                    {t('packsInstall.uiSlotsLabel')}
                  </p>
                  <Button
                    type="button"
                    size="xs"
                    variant="outline"
                    data-testid="pack-install-slot-add"
                    onClick={() => setSlotRows((rows) => [...rows, newSlotRow()])}
                  >
                    <Plus className="size-3" aria-hidden="true" />
                    {t('packsInstall.slotAdd')}
                  </Button>
                </div>
                <ul className="space-y-2">
                  {slotRows.map((row, i) => (
                    <li
                      key={i}
                      className="grid items-end gap-2 rounded-md border border-zinc-200 p-3 sm:grid-cols-[auto_1fr_2fr_100px_auto] dark:border-zinc-800"
                      data-testid="pack-install-slot-row"
                    >
                      <div>
                        <label htmlFor={`pack-install-slot-kind-${i}`} className={fieldLabel}>
                          {t('packsInstall.slotLabel')}
                        </label>
                        <select
                          id={`pack-install-slot-kind-${i}`}
                          value={row.slot}
                          onChange={(e) => setSlot(i, { slot: e.target.value })}
                          className={selectCls}
                        >
                          {SLOT_KINDS.map((sk) => (
                            <option key={sk} value={sk}>
                              {sk}
                            </option>
                          ))}
                        </select>
                      </div>
                      <div>
                        <label htmlFor={`pack-install-slot-label-${i}`} className={fieldLabel}>
                          {t('packsInstall.slotLabelField')}
                        </label>
                        <Input
                          id={`pack-install-slot-label-${i}`}
                          value={row.label}
                          onChange={(e) => setSlot(i, { label: e.target.value })}
                          placeholder="预览"
                          data-testid="pack-install-slot-label"
                        />
                      </div>
                      <div>
                        <label htmlFor={`pack-install-slot-url-${i}`} className={fieldLabel}>
                          {t('packsInstall.slotUrlLabel')}
                        </label>
                        <Input
                          id={`pack-install-slot-url-${i}`}
                          value={row.url_template}
                          onChange={(e) => setSlot(i, { url_template: e.target.value })}
                          placeholder="https://…?item={workItemId}&t={uiToken}"
                          data-testid="pack-install-slot-url"
                        />
                      </div>
                      <div>
                        <label htmlFor={`pack-install-slot-height-${i}`} className={fieldLabel}>
                          {t('packsInstall.slotHeightLabel')}
                        </label>
                        <Input
                          id={`pack-install-slot-height-${i}`}
                          type="number"
                          min={120}
                          value={row.height}
                          onChange={(e) => setSlot(i, { height: e.target.value })}
                          data-testid="pack-install-slot-height"
                        />
                      </div>
                      <Button
                        type="button"
                        size="icon-sm"
                        variant="ghost"
                        className="text-red-600 dark:text-red-400"
                        aria-label={t('packsInstall.removeSlot')}
                        data-testid="pack-install-slot-remove"
                        onClick={() => setSlotRows((rows) => rows.filter((_, idx) => idx !== i))}
                      >
                        <Trash2 className="size-3.5" aria-hidden="true" />
                      </Button>
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          )}
        </section>

        {/* 提交行：校验/请求错误 + 按钮 */}
        <div className="space-y-2">
          {validation && (
            <p role="alert" className="text-sm text-red-600 dark:text-red-400">
              {validation}
            </p>
          )}
          {save.error && (
            <p role="alert" className="text-sm text-red-600 dark:text-red-400">
              {save.error.message}
            </p>
          )}
          <Button
            type="submit"
            disabled={!form.name.trim() || save.isPending}
            data-testid="pack-install-submit"
          >
            {save.isPending ? t('packsInstall.submitting') : t('packsInstall.submit')}
          </Button>
        </div>
      </form>

      {/* 安装成功：订阅密钥一次性展示（若 subscriptions 里有非空 secret）。
          交互照 pack-detail 轮换密钥 Dialog：警示文案 + 等宽密钥 + 复制按钮。 */}
      <Dialog
        open={secrets !== null}
        onOpenChange={(next) => (!next ? closeSecrets() : undefined)}
      >
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t('packsInstall.secretsTitle')}</DialogTitle>
            <DialogDescription>{t('packsInstall.secretsWarning')}</DialogDescription>
          </DialogHeader>
          <ul className="space-y-2">
            {(secrets ?? []).map((s) => (
              <li
                key={s.subscription_id}
                className="flex items-center gap-2"
                data-testid="pack-install-secret-row"
              >
                <code className="min-w-0 flex-1 overflow-x-auto rounded-md border border-zinc-200 bg-zinc-50 px-3 py-2 font-mono text-xs whitespace-nowrap dark:border-zinc-700 dark:bg-zinc-900">
                  {s.url}
                </code>
                <code
                  className="min-w-0 flex-1 overflow-x-auto rounded-md border border-amber-300 bg-amber-50 px-3 py-2 font-mono text-xs whitespace-nowrap dark:border-amber-700/60 dark:bg-amber-900/20"
                  data-testid="pack-install-secret-value"
                >
                  {s.secret}
                </code>
                <Button
                  size="icon-sm"
                  variant="outline"
                  aria-label={t('common.copy')}
                  onClick={() => void navigator.clipboard.writeText(s.secret ?? '')}
                >
                  <Copy className="size-3.5" aria-hidden="true" />
                </Button>
              </li>
            ))}
          </ul>
          <p className="flex items-center gap-1.5 text-xs text-amber-700 dark:text-amber-400">
            <Radio className="size-3.5 shrink-0" aria-hidden="true" />
            {t('packsInstall.secretsWarning')}
          </p>
          <div className="flex justify-end">
            <Button data-testid="pack-install-secrets-done" onClick={closeSecrets}>
              {t('packsInstall.secretsDone')}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}

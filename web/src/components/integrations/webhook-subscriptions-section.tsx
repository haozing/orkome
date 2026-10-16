import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { isApiError } from '@ploykit/client'
import {
  Check,
  Copy,
  HeartPulse,
  History,
  Pause,
  Play,
  Plus,
  Radio,
  RotateCw,
  Trash2,
} from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  createWebhookSubscription,
  deleteWebhookSubscription,
  listWebhookDeliveries,
  listWebhookEvents,
  listWebhookSubscriptions,
  pingWebhookSubscription,
  redeliverWebhookDelivery,
  rotateWebhookSecret,
  setWebhookSubscriptionActive,
  type WebhookDelivery,
  type WebhookSubscription,
} from '@/api/webhooks'
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogTitle } from '@/components/ui/alert-dialog'
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

// Webhook 订阅区块（2026-10-15 自 settings 迁入集成页段②，docs/integrations-page-design-2026-10-15.md §2.2）。
// 原 settings 域设计 §3.6 的形态：订阅列表 + 行操作（Ping/暂停恢复/
// 轮换密钥/删除）+ 创建 Dialog（事件目录多选，交互照集成向导时期的分区表单）+
// 投递记录：标题旁按钮开弹窗（2026-10-15 二次裁决）。
// 读=member（webhooks:read），写=admin（webhooks:manage）；行操作错误行内标注。
// 一次性 secret 只存组件 useState（设计 §4.4），Dialog 文案警示离开不可再取。
// 视觉：每条订阅一张内嵌卡（头排 URL+状态/事件数 Badge+操作钮，下接描述/
// 事件码/创建时间），与 workflow-editor StageCard 同一家族。

export interface WebhookSubscriptionsSectionProps {
  workspaceId: string
  /** 当前用户在该工作区的 role；owner/admin 可见写控件（创建/删除/轮换/暂停）。 */
  role: string | undefined
}

const LABEL_CLS = 'mb-1 block text-xs text-zinc-500 dark:text-zinc-400'

function errMsg(err: unknown, fallback: string): string {
  return isApiError(err) ? err.message : fallback
}

export function WebhookSubscriptionsSection({
  workspaceId,
  role,
}: WebhookSubscriptionsSectionProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const canWrite = role === 'owner' || role === 'admin'
  const subscriptions = useQuery({
    queryKey: ['ws-subscriptions', workspaceId] as const,
    queryFn: () => listWebhookSubscriptions(workspaceId),
  })
  const [createOpen, setCreateOpen] = useState(false)
  const [deleting, setDeleting] = useState<WebhookSubscription | null>(null)

  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ['ws-subscriptions', workspaceId] })
  const del = useMutation({
    mutationFn: (id: string) => deleteWebhookSubscription(workspaceId, id),
    onSuccess: () => {
      setDeleting(null)
      invalidate()
    },
  })

  return (
    <section
      id="webhooks"
      className="space-y-3 rounded-lg border border-zinc-200 p-4 dark:border-zinc-800"
      aria-label={t('tools.webhooksTitle')}
      data-testid="ws-webhooks-section"
    >
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">{t('tools.webhooksTitle')}</h2>
        {canWrite && (
          <Button size="sm" variant="outline" data-testid="ws-webhook-create-open" onClick={() => setCreateOpen(true)}>
            <Plus className="size-3.5" aria-hidden="true" />
            {t('tools.webhookCreate')}
          </Button>
        )}
      </div>
      {!canWrite && (
        <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('tools.webhookReadonlyHint')}</p>
      )}

      {subscriptions.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
      {subscriptions.isError && (
        <p role="alert" className="text-sm text-red-600 dark:text-red-400">
          {errMsg(subscriptions.error, t('tools.loadFailed'))}
        </p>
      )}
      {subscriptions.data && subscriptions.data.length === 0 && (
        <p className="py-2 text-sm text-zinc-400">{t('tools.webhookEmpty')}</p>
      )}
      {subscriptions.data && subscriptions.data.length > 0 && (
        <div className="divide-y divide-zinc-100 dark:divide-zinc-800">
          {subscriptions.data.map((sub) => (
            <SubscriptionRow
              key={sub.id}
              sub={sub}
              workspaceId={workspaceId}
              canWrite={canWrite}
              onRequestDelete={() => setDeleting(sub)}
              onChanged={invalidate}
            />
          ))}
        </div>
      )}
      {createOpen && (
        <CreateSubscriptionDialog
          workspaceId={workspaceId}
          onClose={() => setCreateOpen(false)}
          onCreated={invalidate}
        />
      )}

      {/* 删除二次确认 */}
      <AlertDialog open={deleting !== null} onOpenChange={(next) => (!next ? setDeleting(null) : undefined)}>
        <AlertDialogContent>
          <AlertDialogTitle>{t('tools.webhookDeleteTitle')}</AlertDialogTitle>
          <AlertDialogDescription>
            {t('tools.webhookDeleteDesc', { url: deleting?.url ?? '' })}
          </AlertDialogDescription>
          {del.error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {errMsg(del.error, t('tools.actionFailed'))}
            </p>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-red-600 text-white hover:bg-red-500 focus-visible:ring-red-600/40"
              disabled={del.isPending}
              data-testid="ws-webhook-delete-confirm"
              onClick={() => deleting && del.mutate(deleting.id)}
            >
              {del.isPending ? t('common.loading') : t('common.delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

// ---- 订阅行（Ping 三态 / 轮换一次性明文 / 暂停恢复 / 删除入口）----

type PingState = 'idle' | 'running' | 'ok' | 'fail'

function SubscriptionRow({
  sub,
  workspaceId,
  canWrite,
  onRequestDelete,
  onChanged,
}: {
  sub: WebhookSubscription
  workspaceId: string
  canWrite: boolean
  onRequestDelete: () => void
  onChanged: () => void
}) {
  const { t, i18n } = useTranslation()
  const [ping, setPing] = useState<PingState>('idle')
  const [actionErr, setActionErr] = useState<string | null>(null)
  const [rotateOpen, setRotateOpen] = useState(false)
  const [rotatedSecret, setRotatedSecret] = useState<string | null>(null)
  const [deliveriesOpen, setDeliveriesOpen] = useState(false)

  const doPing = async () => {
    if (ping === 'running') return
    setPing('running')
    try {
      await pingWebhookSubscription(workspaceId, sub.id)
      setPing('ok')
    } catch {
      setPing('fail')
    } finally {
      setTimeout(() => setPing('idle'), 2000)
    }
  }
  const rotate = useMutation({
    mutationFn: () => rotateWebhookSecret(workspaceId, sub.id),
    onSuccess: (res) => {
      setRotatedSecret(res.secret)
      setActionErr(null)
    },
    onError: (err) => setActionErr(errMsg(err, t('tools.actionFailed'))),
  })
  const toggle = useMutation({
    mutationFn: () => setWebhookSubscriptionActive(workspaceId, sub.id, !sub.is_active),
    onSuccess: () => {
      setActionErr(null)
      onChanged()
    },
    onError: (err) => setActionErr(errMsg(err, t('tools.actionFailed'))),
  })
  const fmtDate = (iso: string): string =>
    new Date(iso).toLocaleDateString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })

  return (
    <div className="py-3 first:pt-1 last:pb-1" data-testid="ws-webhook-row" data-subscription-id={sub.id}>
      <div className="space-y-2 rounded-md border border-zinc-200 p-3 dark:border-zinc-800">
        {/* 头排：URL + 状态/事件数 Badge 右对齐（照 StageCard 卡头布局） */}
        <div className="flex items-center gap-2">
          <code className="min-w-0 flex-1 truncate font-mono text-xs" title={sub.url}>
            {sub.url}
          </code>
          <Button
            size="xs"
            variant="ghost"
            className="shrink-0 text-zinc-500 dark:text-zinc-400"
            data-testid="ws-webhook-deliveries-open"
            onClick={() => setDeliveriesOpen(true)}
          >
            <History className="size-3" aria-hidden="true" />
            {t('tools.deliveriesTitle')}
          </Button>
          <Badge variant={sub.is_active ? 'success' : 'warning'} className="shrink-0">
            {t(sub.is_active ? 'tools.webhookActive' : 'tools.webhookInactive')}
          </Badge>
          <Badge variant="secondary" className="shrink-0">
            {t('tools.webhookEventsCount', { count: sub.event_types.length })}
          </Badge>
        </div>
        {sub.description && (
          <p className="text-xs text-muted-foreground">{sub.description}</p>
        )}
        {sub.event_types.length > 0 && (
          <p className="flex flex-wrap gap-1">
            {sub.event_types.map((et) => (
              <Badge key={et} variant="outline" className="font-mono">
                {et}
              </Badge>
            ))}
          </p>
        )}
        <div className="flex flex-wrap items-center gap-1.5">
          <Button size="xs" variant="outline" disabled={ping === 'running'} data-testid="ws-webhook-ping" onClick={() => void doPing()}>
            <HeartPulse className="size-3" aria-hidden="true" />
            {ping === 'running'
              ? t('tools.pingRunning')
              : ping === 'ok'
                ? t('tools.pingOk')
                : ping === 'fail'
                  ? t('tools.pingFail')
                  : t('tools.ping')}
          </Button>
          {canWrite && (
            <>
              <Button
                size="xs"
                variant="outline"
                disabled={rotate.isPending}
                data-testid="ws-webhook-rotate"
                onClick={() => {
                  setRotatedSecret(null)
                  setRotateOpen(true)
                  rotate.mutate()
                }}
              >
                <RotateCw className="size-3" aria-hidden="true" />
                {t('tools.rotate')}
              </Button>
              <Button
                size="xs"
                variant="outline"
                disabled={toggle.isPending}
                data-testid="ws-webhook-toggle"
                onClick={() => toggle.mutate()}
              >
                {sub.is_active ? (
                  <>
                    <Pause className="size-3" aria-hidden="true" />
                    {t('tools.pause')}
                  </>
                ) : (
                  <>
                    <Play className="size-3" aria-hidden="true" />
                    {t('tools.resume')}
                  </>
                )}
              </Button>
              <Button
                size="xs"
                variant="ghost"
                className="text-red-600 hover:text-red-600 dark:text-red-400 dark:hover:text-red-400"
                data-testid="ws-webhook-delete"
                onClick={onRequestDelete}
              >
                <Trash2 className="size-3" aria-hidden="true" />
                {t('common.delete')}
              </Button>
            </>
          )}
          {actionErr && (
            <span role="alert" className="text-xs text-red-600 dark:text-red-400">
              {actionErr}
            </span>
          )}
        </div>
        <p className="text-xs text-zinc-400">{t('tools.webhookCreatedAt', { date: fmtDate(sub.created_at) })}</p>
      </div>

      {/* 投递记录弹窗（标题旁按钮触发；投递是订阅子资源但列表撑高卡片，
          弹窗独立承载——2026-10-15 二次裁决） */}
      <SubscriptionDeliveriesDialog
        workspaceId={workspaceId}
        subId={sub.id}
        url={sub.url}
        open={deliveriesOpen}
        onOpenChange={setDeliveriesOpen}
      />

      {/* 轮换密钥：两段式（确认警示 → 一次性明文），照 pack-detail 同名交互 */}
      <Dialog open={rotateOpen} onOpenChange={(next) => (!next ? setRotateOpen(false) : undefined)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('tools.rotateTitle')}</DialogTitle>
            <DialogDescription>
              {rotatedSecret ? t('tools.rotateOnceDesc') : t('tools.rotateConfirmDesc')}
            </DialogDescription>
          </DialogHeader>
          {rotatedSecret ? (
            <div
              className="flex items-center gap-2 rounded-md bg-zinc-50 p-2.5 dark:bg-zinc-900"
              data-testid="ws-webhook-secret"
            >
              <code className="min-w-0 flex-1 font-mono text-sm break-all">{rotatedSecret}</code>
              <Button
                size="icon-sm"
                variant="outline"
                className="shrink-0"
                aria-label={t('common.copy')}
                onClick={() => void navigator.clipboard.writeText(rotatedSecret)}
              >
                <Copy className="size-3.5" aria-hidden="true" />
              </Button>
            </div>
          ) : (
            <p className="flex items-center gap-1.5 text-xs text-amber-700 dark:text-amber-400">
              <Radio className="size-3.5 shrink-0" aria-hidden="true" />
              {rotate.isPending ? t('tools.rotateRunning') : t('tools.rotateWarning')}
            </p>
          )}
          <div className="flex justify-end">
            <Button variant="outline" onClick={() => setRotateOpen(false)}>
              {t('common.close')}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}

// ---- 创建 Dialog（URL + description + 事件目录多选）----

function CreateSubscriptionDialog({
  workspaceId,
  onClose,
  onCreated,
}: {
  workspaceId: string
  onClose: () => void
  onCreated: () => void
}) {
  const { t } = useTranslation()
  const [url, setUrl] = useState('')
  const [description, setDescription] = useState('')
  const [events, setEvents] = useState<string[]>([])
  const [validation, setValidation] = useState<string | null>(null)
  // 创建成功的一次性 secret：仅存本组件 state，关 Dialog 即不可再取。
  const [createdSecret, setCreatedSecret] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  // 事件目录（member 可读）拉来做多选组——Dialog 生命周期内取一次。
  const catalog = useQuery({
    queryKey: ['webhook-events', workspaceId],
    queryFn: () => listWebhookEvents(workspaceId),
  })

  const create = useMutation({
    mutationFn: () =>
      createWebhookSubscription(workspaceId, url.trim(), events, description.trim()),
    onSuccess: (res) => {
      onCreated()
      setCreatedSecret(res.secret)
    },
    onError: (err) => setValidation(errMsg(err, t('tools.actionFailed'))),
  })
  const submit = () => {
    if (!url.trim()) {
      setValidation(t('tools.webhookUrlRequired'))
      return
    }
    if (events.length === 0) {
      setValidation(t('tools.webhookEventsRequired'))
      return
    }
    setValidation(null)
    create.mutate()
  }
  const close = () => {
    setCreatedSecret(null)
    onClose()
  }
  const copySecret = () => {
    if (!createdSecret) return
    void navigator.clipboard.writeText(createdSecret).then(
      () => {
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      },
      () => {},
    )
  }

  return (
    <Dialog open onOpenChange={(next) => (!next ? close() : undefined)}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg" data-testid="ws-webhook-create-dialog">
        <DialogHeader>
          <DialogTitle>{t('tools.webhookCreateTitle')}</DialogTitle>
          <DialogDescription>{t('tools.webhookCreateDesc')}</DialogDescription>
        </DialogHeader>

        {createdSecret === null ? (
          <div className="space-y-3">
            <div>
              <label htmlFor="ws-webhook-url" className={LABEL_CLS}>
                {t('tools.webhookUrlLabel')}
              </label>
              <Input
                id="ws-webhook-url"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://vendor.example.com/hooks"
                autoFocus
                data-testid="ws-webhook-url"
              />
            </div>
            <div>
              <label htmlFor="ws-webhook-desc" className={LABEL_CLS}>
                {t('tools.webhookDescLabel')}
              </label>
              <Input
                id="ws-webhook-desc"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder={t('tools.webhookDescPlaceholder')}
                data-testid="ws-webhook-desc"
              />
            </div>
            <div>
              <p className={LABEL_CLS}>{t('tools.webhookEventsLabel')}</p>
              {catalog.isPending && <p className="text-xs text-zinc-500">{t('common.loading')}</p>}
              {catalog.isError && (
                <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                  {t('tools.webhookCatalogFailed')}
                </p>
              )}
              {catalog.data && catalog.data.length === 0 && (
                <p className="text-xs text-zinc-500">{t('tools.webhookCatalogEmpty')}</p>
              )}
              {catalog.data && catalog.data.length > 0 && (
                <div className="flex flex-wrap gap-x-4 gap-y-1.5">
                  {catalog.data.map((ev) => (
                    <label key={ev.type} className="flex cursor-pointer items-center gap-1.5 text-xs text-zinc-500 dark:text-zinc-400" data-testid="ws-webhook-event-option" title={ev.description}>
                      <input
                        type="checkbox"
                        checked={events.includes(ev.type)}
                        onChange={(e) =>
                          setEvents((prev) =>
                            e.target.checked
                              ? [...prev, ev.type]
                              : prev.filter((ty) => ty !== ev.type),
                          )
                        }
                        className="size-3 accent-zinc-700 dark:accent-zinc-300"
                      />
                      <code className="font-mono">{ev.type}</code>
                    </label>
                  ))}
                </div>
              )}
            </div>
            {validation && (
              <p role="alert" className="text-xs text-red-600 dark:text-red-400">
                {validation}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={close}>
                {t('common.cancel')}
              </Button>
              <Button disabled={!url.trim() || events.length === 0 || create.isPending} data-testid="ws-webhook-create-submit" onClick={submit}>
                {create.isPending ? t('common.loading') : t('tools.webhookCreate')}
              </Button>
            </div>
          </div>
        ) : (
          <div className="space-y-3" data-testid="ws-webhook-secret-dialog">
            <p className="text-sm">{t('tools.webhookSecretTitle')}</p>
            <div className="rounded-md bg-zinc-50 p-2.5 dark:bg-zinc-900">
              <div className="flex items-center gap-2" data-testid="ws-webhook-secret">
                <code className="min-w-0 flex-1 font-mono text-sm break-all">{createdSecret}</code>
                <Button size="icon-sm" variant="outline" className="shrink-0" aria-label={t('common.copy')} onClick={copySecret}>
                  {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                </Button>
              </div>
            </div>
            <p className="flex items-center gap-1.5 text-xs text-amber-700 dark:text-amber-400">
              <Radio className="size-3.5 shrink-0" aria-hidden="true" />
              {t('tools.webhookSecretWarning')}
            </p>
            <div className="flex justify-end">
              <Button onClick={close}>{t('common.ok')}</Button>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

// ---- 投递记录弹窗（2026-10-15 二次裁决：区块级面板/卡内展开都不合适，
// 订阅卡标题旁按钮开弹窗——投递是订阅的子资源，但列表撑高卡片，弹窗独立承载）----

function deliveryStatusVariant(status: string): 'success' | 'warning' | 'danger' | 'neutral' {
  if (status === 'delivered') return 'success'
  if (status === 'pending') return 'warning'
  if (status === 'dead') return 'danger'
  return 'neutral'
}

export function SubscriptionDeliveriesDialog({
  workspaceId,
  subId,
  url,
  open,
  onOpenChange,
}: {
  workspaceId: string
  subId: string
  url: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const deliveries = useQuery({
    queryKey: ['webhook-deliveries', workspaceId],
    queryFn: () => listWebhookDeliveries(workspaceId),
    enabled: open,
  })
  const redeliver = useMutation({
    mutationFn: (id: string) => redeliverWebhookDelivery(workspaceId, id),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['webhook-deliveries', workspaceId] }),
  })
  const rows = (deliveries.data ?? []).filter((dl) => dl.subscription_id === subId)
  const canRedeliver = (dl: WebhookDelivery) =>
    dl.status === 'dead' || (dl.status === 'pending' && dl.attempts > 1)
  const fmtDate = (iso: string): string =>
    new Date(iso).toLocaleString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t('tools.deliveriesTitle')}</DialogTitle>
          <DialogDescription className="truncate font-mono text-xs">{url}</DialogDescription>
        </DialogHeader>
        <div className="max-h-[60vh] space-y-2 overflow-y-auto">
          {deliveries.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
          {deliveries.isError && (
            <p role="alert" className="text-sm text-red-600 dark:text-red-400">
              {t('tools.deliveriesFailed')}
            </p>
          )}
          {deliveries.data && rows.length === 0 && (
            <p className="py-8 text-center text-xs text-zinc-400">{t('tools.deliveriesEmpty')}</p>
          )}
          {rows.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-xs" data-testid="ws-webhook-deliveries-table">
                <thead>
                  <tr className="border-b border-zinc-200 text-xs text-zinc-500 dark:border-zinc-800">
                    <th className="py-1.5 pr-3 font-medium">{t('tools.deliveriesColEvent')}</th>
                    <th className="py-1.5 pr-3 font-medium">{t('tools.deliveriesColStatus')}</th>
                    <th className="py-1.5 pr-3 font-medium">{t('tools.deliveriesColAttempts')}</th>
                    <th className="py-1.5 pr-3 font-medium">{t('tools.deliveriesColCode')}</th>
                    <th className="py-1.5 pr-3 font-medium">{t('tools.deliveriesColError')}</th>
                    <th className="py-1.5 pr-3 font-medium">{t('tools.deliveriesColTime')}</th>
                    <th className="py-1.5 font-medium" aria-label={t('common.actions')} />
                  </tr>
                </thead>
                <tbody>
                  {rows.map((dl) => (
                    <tr
                      key={dl.id}
                      className="border-b border-zinc-100 align-top last:border-b-0 dark:border-zinc-800/60"
                      data-testid="ws-webhook-delivery-row"
                    >
                      <td className="py-1.5 pr-3 font-mono">{dl.event_type}</td>
                      <td className="py-1.5 pr-3">
                        <Badge variant={deliveryStatusVariant(dl.status)}>{dl.status}</Badge>
                      </td>
                      <td className="py-1.5 pr-3">{dl.attempts}</td>
                      <td className="py-1.5 pr-3 font-mono">{dl.last_status_code ?? '–'}</td>
                      <td
                        className="max-w-40 truncate py-1.5 pr-3 text-muted-foreground"
                        title={dl.last_error ?? undefined}
                      >
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
                            data-testid="ws-webhook-redeliver"
                            onClick={() => redeliver.mutate(dl.id)}
                          >
                            <RotateCw className="size-3" aria-hidden="true" />
                            {t('tools.deliveriesRedeliver')}
                          </Button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

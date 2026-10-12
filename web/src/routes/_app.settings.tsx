import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute, Link } from '@tanstack/react-router'
import { Switch } from '@base-ui/react/switch'
import { ApiError } from '@ploykit/client'
import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import {
  MUTABLE_NOTIFICATION_TYPES,
  getNotificationPreferences,
  patchNotificationPreferences,
} from '@/api/notification'
import { configQueryOptions, meQueryOptions } from '@/api/identity'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import { switchWorkspace } from '@/services/workspace-switch'
import { useWorkspaceStore } from '@/stores/workspace'

export const Route = createFileRoute('/_app/settings')({
  component: SettingsPage,
})

export function SettingsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const config = useQuery(configQueryOptions)
  const me = useQuery(meQueryOptions)
  const currentId = useWorkspaceStore((s) => s.currentId)
  const unauthenticated = me.error instanceof ApiError && me.error.status === 401
  return (
    <div className="space-y-6">
      <section className="rounded-lg border border-zinc-200 p-4 dark:border-zinc-800">
        <h2 className="mb-3 text-sm font-semibold">{t('settings.server')}</h2>
        {config.isPending && <PageLoading label={t('settings.connecting')} className="pt-2" />}
        {config.data && (
          <p className="font-mono text-sm">
            {t('settings.version')}{' '}
            <span className="text-zinc-500">{config.data.version || 'unknown'}</span>
          </p>
        )}
        {config.error && (
          <PageError
            title={t('settings.unreachable')}
            message={t('settings.unreachableDesc')}
            onRetry={() => void config.refetch()}
            className="pt-4"
          />
        )}
      </section>

      <section className="rounded-lg border border-zinc-200 p-4 dark:border-zinc-800">
        <h2 className="mb-3 text-sm font-semibold">{t('settings.session')}</h2>
        {me.isPending && <PageLoading label={t('settings.loadingSession')} className="pt-2" />}
        {me.data && (
          <div className="space-y-2 text-sm">
            <p>
              {me.data.user.display_name || me.data.user.email}
              <span className="ml-2 text-zinc-500">{me.data.user.email}</span>
            </p>
            {me.data.workspaces.length > 0 ? (
              <div className="flex flex-wrap gap-1">
                {me.data.workspaces.map((ws) => (
                  <Button
                    key={ws.id}
                    variant={ws.id === currentId ? 'default' : 'outline'}
                    size="sm"
                    onClick={() => switchWorkspace(queryClient, ws.id)}
                  >
                    {ws.name}
                    <span className="font-mono text-xs opacity-60">/{ws.slug}</span>
                  </Button>
                ))}
              </div>
            ) : (
              <PageEmpty
                title={t('settings.noWorkspace')}
                description={t('settings.noWorkspaceDesc')}
                action={
                  <Link to="/onboarding">
                    <Button size="sm" variant="outline">
                      {t('settings.goCreate')}
                    </Button>
                  </Link>
                }
                className="pt-2"
              />
            )}
          </div>
        )}
        {unauthenticated && (
          <div className="space-y-2 text-sm">
            <p className="text-zinc-500">{t('settings.notLoggedIn')}</p>
            <Link to="/login">
              <Button size="sm">{t('settings.goLogin')}</Button>
            </Link>
          </div>
        )}
      </section>

      {/* 偏好端点要求登录会话（webx.RequireAuth），未登录时整块隐藏避免常驻报错 */}
      {me.data && <NotificationPreferencesSection />}
    </div>
  )
}

// ---- 通知偏好（迁移 1034 的三组静音，工作区级 'mute' 哨兵行） ----

// 服务端 ID 组上限（app/service.go validatePreference：types≤50、ID 组≤200）。
// 类别组经本页最多写入 6 个值，无需上限；ID 组加到 200 时禁用添加。
const PREFERENCE_ID_GROUP_CAP = 200

const MUTABLE_TYPE_DEFS: {
  type: (typeof MUTABLE_NOTIFICATION_TYPES)[number]
  labelKey: string
  descKey: string
}[] = [
  {
    type: 'task.completed',
    labelKey: 'notifications.muteTaskCompleted',
    descKey: 'notifications.muteTaskCompletedDesc',
  },
  {
    type: 'task.failed',
    labelKey: 'notifications.muteTaskFailed',
    descKey: 'notifications.muteTaskFailedDesc',
  },
  {
    type: 'comment.created',
    labelKey: 'notifications.muteCommentCreated',
    descKey: 'notifications.muteCommentCreatedDesc',
  },
  {
    type: 'approval_request',
    labelKey: 'notifications.muteApprovalRequest',
    descKey: 'notifications.muteApprovalRequestDesc',
  },
  {
    type: 'eval_report',
    labelKey: 'notifications.muteEvalReport',
    descKey: 'notifications.muteEvalReportDesc',
  },
]

type PrefsStatus = { kind: 'saved' } | { kind: 'error'; message?: string } | null

function PrefSwitch({
  checked,
  disabled,
  onCheckedChange,
  label,
}: {
  checked: boolean
  disabled?: boolean
  onCheckedChange: (checked: boolean) => void
  label: string
}) {
  return (
    <Switch.Root
      checked={checked}
      disabled={disabled}
      onCheckedChange={onCheckedChange}
      aria-label={label}
      className="relative inline-flex h-[18px] w-[32px] shrink-0 items-center rounded-full border border-transparent transition-all outline-none after:absolute after:-inset-x-3 after:-inset-y-2 focus-visible:ring-3 focus-visible:ring-ring/50 data-checked:bg-primary data-unchecked:bg-input data-disabled:cursor-not-allowed data-disabled:opacity-50 dark:data-unchecked:bg-input/80"
    >
      <Switch.Thumb className="pointer-events-none block size-4 rounded-full bg-background ring-0 transition-transform data-checked:translate-x-[calc(100%-2px)] data-unchecked:translate-x-0 dark:data-checked:bg-primary-foreground dark:data-unchecked:bg-foreground" />
    </Switch.Root>
  )
}

function PrefRow({
  label,
  desc,
  checked,
  disabled,
  onCheckedChange,
}: {
  label: string
  desc: string
  checked: boolean
  disabled?: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <div className="flex items-start justify-between gap-4 py-2">
      <div className="min-w-0">
        <p className="text-sm">{label}</p>
        <p className="text-xs text-zinc-500 dark:text-zinc-400">{desc}</p>
      </div>
      <PrefSwitch
        checked={checked}
        disabled={disabled}
        onCheckedChange={onCheckedChange}
        label={label}
      />
    </div>
  )
}

function MutedIdChips({
  ids,
  disabled,
  onRemove,
  removeAria,
}: {
  ids: string[]
  disabled?: boolean
  onRemove: (id: string) => void
  removeAria: (id: string) => string
}) {
  return (
    <ul className="mt-1 flex flex-wrap gap-1">
      {ids.map((id) => (
        <li
          key={id}
          className="flex items-center gap-1 rounded-md border border-zinc-200 px-1.5 py-0.5 font-mono text-xs dark:border-zinc-700"
        >
          <span className="max-w-48 truncate">{id}</span>
          <button
            type="button"
            aria-label={removeAria(id)}
            disabled={disabled}
            onClick={() => onRemove(id)}
            className="text-zinc-400 hover:text-red-600 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:text-red-400"
          >
            ×
          </button>
        </li>
      ))}
    </ul>
  )
}

function NotificationPreferencesSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  // 读-改-写：GET 全量 → 每次交互只 PATCH 被改动的那个键（服务端按键合并，
  // 返回合并后的完整偏好，直接写回缓存免重取）。
  const prefs = useQuery({
    queryKey: ['notification-preferences'],
    queryFn: getNotificationPreferences,
  })
  const [status, setStatus] = useState<PrefsStatus>(null)
  const [workItemDraft, setWorkItemDraft] = useState('')
  const [conversationDraft, setConversationDraft] = useState('')

  const save = useMutation({
    mutationFn: patchNotificationPreferences,
    onSuccess: (merged) => {
      queryClient.setQueryData(['notification-preferences'], merged)
      setStatus({ kind: 'saved' })
    },
    onError: (err) => {
      setStatus({ kind: 'error', message: err instanceof ApiError ? err.message : undefined })
    },
  })
  const pending = save.isPending

  if (prefs.isPending) {
    return (
      <section className="rounded-lg border border-zinc-200 p-4 dark:border-zinc-800" data-testid="prefs-section">
        <h2 className="mb-3 text-sm font-semibold">{t('notifications.title')}</h2>
        <PageLoading label={t('notifications.loading')} className="pt-2" />
      </section>
    )
  }
  if (prefs.isError || !prefs.data) {
    return (
      <section className="rounded-lg border border-zinc-200 p-4 dark:border-zinc-800" data-testid="prefs-section">
        <h2 className="mb-3 text-sm font-semibold">{t('notifications.title')}</h2>
        <PageError
          title={t('notifications.loadFailed')}
          message={prefs.error instanceof ApiError ? prefs.error.message : String(prefs.error)}
          onRetry={() => void prefs.refetch()}
          className="pt-2"
        />
      </section>
    )
  }

  const types = prefs.data.muted_types
  const workItems = prefs.data.muted_work_item_ids
  const conversations = prefs.data.muted_conversation_ids

  const setTypeMuted = (type: string, muted: boolean) => {
    if (muted) {
      if (!types.includes(type)) save.mutate({ muted_types: [...types, type] })
    } else if (types.includes(type)) {
      save.mutate({ muted_types: types.filter((x) => x !== type) })
    }
  }
  const addWorkItem = () => {
    const id = workItemDraft.trim()
    if (!id || workItems.includes(id) || workItems.length >= PREFERENCE_ID_GROUP_CAP) return
    save.mutate({ muted_work_item_ids: [...workItems, id] })
    setWorkItemDraft('')
  }
  const addConversation = () => {
    const id = conversationDraft.trim()
    if (!id || conversations.includes(id) || conversations.length >= PREFERENCE_ID_GROUP_CAP) return
    save.mutate({ muted_conversation_ids: [...conversations, id] })
    setConversationDraft('')
  }

  return (
    <section className="rounded-lg border border-zinc-200 p-4 dark:border-zinc-800" data-testid="prefs-section">
      <h2 className="text-sm font-semibold">{t('notifications.title')}</h2>
      <p className="mb-1 text-xs text-zinc-500 dark:text-zinc-400">{t('notifications.desc')}</p>

      <div className="mt-2 space-y-4">
        <div>
          <p className="text-sm font-medium">{t('notifications.groupTypes')}</p>
          <div className="divide-y divide-zinc-100 dark:divide-zinc-800">
            <PrefRow
              label={t('notifications.allTypes')}
              desc={t('notifications.allTypesDesc')}
              checked={types.includes('*')}
              disabled={pending}
              onCheckedChange={(c) => setTypeMuted('*', c)}
            />
            {MUTABLE_TYPE_DEFS.map((def) => (
              <PrefRow
                key={def.type}
                label={t(def.labelKey)}
                desc={t(def.descKey)}
                checked={types.includes(def.type)}
                disabled={pending}
                onCheckedChange={(c) => setTypeMuted(def.type, c)}
              />
            ))}
          </div>
        </div>

        <div>
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <p className="text-sm font-medium">{t('notifications.groupWorkItems')}</p>
              <p className="text-xs text-zinc-500 dark:text-zinc-400">
                {t('notifications.groupWorkItemsDesc')}
              </p>
            </div>
            <PrefSwitch
              label={t('notifications.groupWorkItems')}
              checked={workItems.length > 0}
              disabled={pending || workItems.length === 0}
              onCheckedChange={(c) => {
                if (!c && workItems.length > 0) save.mutate({ muted_work_item_ids: [] })
              }}
            />
          </div>
          {workItems.length === 0 ? (
            <p className="mt-1 text-xs text-zinc-400 dark:text-zinc-500">
              {t('notifications.emptyWorkItems')}
            </p>
          ) : (
            <MutedIdChips
              ids={workItems}
              disabled={pending}
              onRemove={(id) => save.mutate({ muted_work_item_ids: workItems.filter((x) => x !== id) })}
              removeAria={(id) => t('notifications.removeAria', { id })}
            />
          )}
          <form
            className="mt-2 flex max-w-md gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              addWorkItem()
            }}
          >
            <Input
              value={workItemDraft}
              onChange={(e) => setWorkItemDraft(e.target.value)}
              placeholder={t('notifications.workItemPlaceholder')}
              aria-label={t('notifications.workItemPlaceholder')}
              maxLength={100}
            />
            <Button
              type="submit"
              size="sm"
              variant="outline"
              aria-label={t('notifications.addWorkItem')}
              disabled={!workItemDraft.trim() || pending || workItems.length >= PREFERENCE_ID_GROUP_CAP}
            >
              {t('notifications.add')}
            </Button>
          </form>
        </div>

        <div>
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <p className="text-sm font-medium">{t('notifications.groupConversations')}</p>
              <p className="text-xs text-zinc-500 dark:text-zinc-400">
                {t('notifications.groupConversationsDesc')}
              </p>
            </div>
            <PrefSwitch
              label={t('notifications.groupConversations')}
              checked={conversations.length > 0}
              disabled={pending || conversations.length === 0}
              onCheckedChange={(c) => {
                if (!c && conversations.length > 0) save.mutate({ muted_conversation_ids: [] })
              }}
            />
          </div>
          {conversations.length === 0 ? (
            <p className="mt-1 text-xs text-zinc-400 dark:text-zinc-500">
              {t('notifications.emptyConversations')}
            </p>
          ) : (
            <MutedIdChips
              ids={conversations}
              disabled={pending}
              onRemove={(id) =>
                save.mutate({ muted_conversation_ids: conversations.filter((x) => x !== id) })
              }
              removeAria={(id) => t('notifications.removeAria', { id })}
            />
          )}
          <form
            className="mt-2 flex max-w-md gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              addConversation()
            }}
          >
            <Input
              value={conversationDraft}
              onChange={(e) => setConversationDraft(e.target.value)}
              placeholder={t('notifications.conversationPlaceholder')}
              aria-label={t('notifications.conversationPlaceholder')}
              maxLength={100}
            />
            <Button
              type="submit"
              size="sm"
              variant="outline"
              aria-label={t('notifications.addConversation')}
              disabled={
                !conversationDraft.trim() || pending || conversations.length >= PREFERENCE_ID_GROUP_CAP
              }
            >
              {t('notifications.add')}
            </Button>
          </form>
        </div>

        <p
          aria-live="polite"
          data-testid="prefs-status"
          className={status?.kind === 'error' ? 'text-xs text-red-600 dark:text-red-400' : 'text-xs text-zinc-500 dark:text-zinc-400'}
        >
          {status?.kind === 'saved' && t('notifications.saved')}
          {status?.kind === 'error' && (
            <>
              {t('notifications.saveFailed')}
              {status.message && <span className="ml-1 break-all">{status.message}</span>}
            </>
          )}
        </p>
      </div>
    </section>
  )
}


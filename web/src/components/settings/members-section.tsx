import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { isApiError } from '@ploykit/client'
import {
  DataTable,
  FormField,
  SecretModal,
  useConfirm,
  type Column,
} from '@ploykit/ui'
import { Link2, Trash2, UserPlus } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { membersQueryOptions, type Member } from '@/api/identity'
import {
  createInvitation,
  createShareLink,
  listInvitations,
  listShareLinks,
  removeMember,
  revokeInvitation,
  revokeShareLink,
  updateMemberRole,
  type WorkspaceInvitation,
  type WorkspaceShareLink,
} from '@/api/workspace-admin'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { PageEmpty, PageError, PageLoading } from '@/components/ui/states'
import {
  INLINE_EMPTY_CLS,
  INLINE_ERROR_CLS,
  INLINE_LOADING_CLS,
} from '@/components/settings/shared'

// 成员与邀请区块（settings 域设计 §3.5）：成员表（改角色/移除）+ 邀请管理 +
// 分享链接三段。member 可读（members:read / invites:read），写控件仅对
// owner/admin 渲染（设计 §4.3：区块按 me.workspaces 的 role 条件渲染）。
// 一次性 share code 只存组件 useState，不进 query 缓存（设计 §4.4）。
// 视觉（ui-sweep）：区块自身无边框（壳层 divide-y 分隔，security 范式）；
// 成员表用 kit DataTable（text-xs 紧凑密度）；角色/邀请/分享一律 ui/select；
// 危险确认统一 useConfirm；一次性码展示统一 SecretModal；
// 危险按钮统一 ghost xs 红字 + Trash2（照 workflow-editor StageCard 删除钮参照）。

export interface MembersSectionProps {
  workspaceId: string
  /** 当前用户在该工作区的 role；owner/admin 可见写控件。 */
  role: string | undefined
}

const ROLES = ['admin', 'member'] as const
type AssignableRole = (typeof ROLES)[number]

// 「移除/吊销」危险按钮统一皮（§危险按钮：ghost xs 红字 + Trash2）。
const DANGER_BTN_CLS = 'text-red-600 hover:text-red-600 dark:text-red-400'

function errMsg(err: unknown, fallback: string): string {
  return isApiError(err) ? err.message : fallback
}

function SubsectionHeader({ icon, title }: { icon: ReactNode; title: string }) {
  return (
    <p className="flex items-center gap-1.5 text-sm font-medium">
      <span className="flex items-center text-zinc-400 dark:text-zinc-500" aria-hidden="true">
        {icon}
      </span>
      {title}
    </p>
  )
}

export function MembersSection({ workspaceId, role }: MembersSectionProps) {
  const { t, i18n } = useTranslation()
  const canWrite = role === 'owner' || role === 'admin'
  return (
    <section className="space-y-3" aria-label={t('settingsWs.membersTitle')} data-testid="ws-members-section">
      <h2 className="text-sm font-semibold">{t('settingsWs.membersTitle')}</h2>
      <div className="divide-y divide-zinc-100 dark:divide-zinc-800">
        <div className="space-y-3 pb-3">
          <MembersTable workspaceId={workspaceId} role={role} />
        </div>
        <div className="space-y-3 py-3">
          <InvitationsBlock workspaceId={workspaceId} canWrite={canWrite} />
        </div>
        <div className="space-y-3 pt-3">
          <ShareLinksBlock workspaceId={workspaceId} canWrite={canWrite} locale={i18n.language} />
        </div>
      </div>
    </section>
  )
}

// ---- 成员表 ----

function MembersTable({ workspaceId, role }: { workspaceId: string; role: string | undefined }) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const confirm = useConfirm()
  const canWrite = role === 'owner' || role === 'admin'
  const members = useQuery(membersQueryOptions(workspaceId))
  const [rowError, setRowError] = useState<string | null>(null)

  const changeRole = useMutation({
    mutationFn: ({ userId, next }: { userId: string; next: AssignableRole }) =>
      updateMemberRole(workspaceId, userId, next),
    onSuccess: () => {
      setRowError(null)
      void queryClient.invalidateQueries({ queryKey: ['workspace', workspaceId, 'members'] })
    },
    onError: (err) => setRowError(errMsg(err, t('settingsWs.actionFailed'))),
  })
  const remove = useMutation({
    mutationFn: (userId: string) => removeMember(workspaceId, userId),
    onSuccess: () => {
      setRowError(null)
      void queryClient.invalidateQueries({ queryKey: ['workspace', workspaceId, 'members'] })
    },
    onError: (err) => setRowError(errMsg(err, t('settingsWs.actionFailed'))),
  })

  const fmtDate = (iso: string): string =>
    new Date(iso).toLocaleDateString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })

  // 移除成员二次确认（useConfirm，不能移除自己/最后 owner 由服务端兜底 403/409）。
  const confirmRemove = async (m: Member) => {
    const ok = await confirm({
      title: t('settingsWs.removeTitle'),
      description: t('settingsWs.removeDesc', { email: m.email }),
      confirmText: t('common.confirm'),
      cancelText: t('common.cancel'),
      danger: true,
    })
    if (ok) remove.mutate(m.user_id)
  }

  // 表格密度对齐 orkome text-xs 紧凑风：列级 className 覆盖 DataTable 的
  // px-4 py-3（kit cn 走 tailwind-merge，py-1.5/pr-3 生效）。
  const columns: Column<Member>[] = [
    {
      key: 'email',
      header: t('settingsWs.colEmail'),
      className: 'max-w-52 truncate py-1.5 pr-3 font-mono text-xs',
      render: (m) => m.email,
    },
    {
      key: 'display_name',
      header: t('settingsWs.colDisplayName'),
      className: 'py-1.5 pr-3 text-xs',
      render: (m) => m.display_name || '–',
    },
    {
      key: 'role',
      header: t('settingsWs.colRole'),
      className: 'py-1.5 pr-3 text-xs',
      render: (m) =>
        canWrite && m.role !== 'owner' ? (
          <Select
            value={m.role}
            aria-label={t('settingsWs.roleAria', { email: m.email })}
            className="w-24"
            data-testid="ws-member-role-select"
            disabled={changeRole.isPending}
            onChange={(e) =>
              changeRole.mutate({ userId: m.user_id, next: e.target.value as AssignableRole })
            }
          >
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {t(`settingsWs.role.${r}`)}
              </option>
            ))}
          </Select>
        ) : (
          <Badge variant="secondary">{t(`settingsWs.role.${m.role}`)}</Badge>
        ),
    },
    {
      key: 'joined',
      header: t('settingsWs.colJoined'),
      className: 'py-1.5 pr-3 whitespace-nowrap text-xs text-zinc-500 dark:text-zinc-400',
      render: (m) => fmtDate(m.created_at),
    },
    {
      key: 'actions',
      header: '',
      className: 'py-1.5 text-right text-xs',
      render: (m) =>
        canWrite ? (
          <Button
            size="xs"
            variant="ghost"
            className={DANGER_BTN_CLS}
            aria-label={t('settingsWs.removeMemberAria', { email: m.email })}
            data-testid="ws-member-remove"
            onClick={() => void confirmRemove(m)}
          >
            <Trash2 className="size-3.5" aria-hidden="true" />
            {t('common.remove')}
          </Button>
        ) : null,
    },
  ]

  return (
    <div className="space-y-3">
      {members.isError ? (
        <PageError
          title={t('settingsWs.loadFailed')}
          message={errMsg(members.error, '')}
          onRetry={() => void members.refetch()}
          className={INLINE_ERROR_CLS}
        />
      ) : (
        <DataTable
          columns={columns}
          rows={members.data ?? []}
          rowKey={(m) => m.user_id}
          loading={members.isPending}
          empty={<PageEmpty title={t('settingsWs.membersEmpty')} className={INLINE_EMPTY_CLS} />}
        />
      )}
      {rowError && (
        <p role="alert" className="text-xs text-destructive">
          {rowError}
        </p>
      )}
    </div>
  )
}

// ---- 邀请管理 ----

function InvitationsBlock({ workspaceId, canWrite }: { workspaceId: string; canWrite: boolean }) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const invitations = useQuery({
    queryKey: ['ws-invitations', workspaceId] as const,
    queryFn: () => listInvitations(workspaceId),
  })
  const [email, setEmail] = useState('')
  const [inviteRole, setInviteRole] = useState<AssignableRole>('member')
  const [formError, setFormError] = useState<string | null>(null)

  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ['ws-invitations', workspaceId] })
  const create = useMutation({
    mutationFn: () => createInvitation(workspaceId, email.trim(), inviteRole),
    onSuccess: () => {
      setEmail('')
      setFormError(null)
      invalidate()
    },
    onError: (err) => setFormError(errMsg(err, t('settingsWs.actionFailed'))),
  })
  const revoke = useMutation({
    mutationFn: (invId: string) => revokeInvitation(workspaceId, invId),
    onSuccess: invalidate,
    onError: (err) => setFormError(errMsg(err, t('settingsWs.actionFailed'))),
  })
  const fmtDate = (iso: string): string =>
    new Date(iso).toLocaleDateString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })

  const submit = () => {
    const trimmed = email.trim()
    if (!trimmed || !trimmed.includes('@')) {
      setFormError(t('settingsWs.inviteEmailInvalid'))
      return
    }
    create.mutate()
  }

  return (
    <div className="space-y-3">
      <SubsectionHeader icon={<UserPlus className="size-3.5" />} title={t('settingsWs.invitesTitle')} />
      {canWrite && (
        <form
          className="flex max-w-md flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <div className="min-w-48 flex-1">
            <FormField label={t('settingsWs.inviteEmailLabel')} htmlFor="ws-invite-email">
              <Input
                id="ws-invite-email"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="name@example.com"
                data-testid="ws-invite-email"
              />
            </FormField>
          </div>
          <div className="w-28">
            <FormField label={t('settingsWs.inviteRoleLabel')} htmlFor="ws-invite-role">
              <Select
                id="ws-invite-role"
                value={inviteRole}
                data-testid="ws-invite-role"
                onChange={(e) => setInviteRole(e.target.value as AssignableRole)}
              >
                {ROLES.map((r) => (
                  <option key={r} value={r}>
                    {t(`settingsWs.role.${r}`)}
                  </option>
                ))}
              </Select>
            </FormField>
          </div>
          <Button type="submit" disabled={!email.trim() || create.isPending} data-testid="ws-invite-submit">
            {create.isPending ? t('common.loading') : t('settingsWs.inviteSubmit')}
          </Button>
        </form>
      )}
      {formError && (
        <p role="alert" className="text-xs text-destructive">
          {formError}
        </p>
      )}
      {invitations.isPending && (
        <PageLoading label={t('common.loading')} className={INLINE_LOADING_CLS} />
      )}
      {invitations.isError && (
        <PageError
          title={t('settingsWs.loadFailed')}
          message={errMsg(invitations.error, '')}
          onRetry={() => void invitations.refetch()}
          className={INLINE_ERROR_CLS}
        />
      )}
      {invitations.data && invitations.data.length === 0 && (
        <p className="text-sm text-zinc-500">{t('settingsWs.invitesEmpty')}</p>
      )}
      {invitations.data && invitations.data.length > 0 && (
        <ul className="divide-y divide-zinc-100 dark:divide-zinc-800" data-testid="ws-invite-list">
          {invitations.data.map((inv: WorkspaceInvitation) => (
            <li key={inv.id} className="flex flex-wrap items-center gap-2 py-2 text-sm">
              <span className="min-w-0 truncate font-mono text-xs">{inv.email}</span>
              <Badge variant="secondary">{t(`settingsWs.role.${inv.role}`)}</Badge>
              <span className="ml-auto text-xs text-zinc-500 dark:text-zinc-400">
                {t('settingsWs.expiresAt', { date: fmtDate(inv.expires_at) })}
              </span>
              {canWrite && (
                <Button
                  size="xs"
                  variant="ghost"
                  className={DANGER_BTN_CLS}
                  disabled={revoke.isPending}
                  aria-label={t('settingsWs.inviteRevokeAria', { email: inv.email })}
                  data-testid="ws-invite-revoke"
                  onClick={() => revoke.mutate(inv.id)}
                >
                  <Trash2 className="size-3.5" aria-hidden="true" />
                  {t('settingsWs.revoke')}
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

// ---- 分享链接 ----

const MAX_USES_MIN = 1
const MAX_USES_MAX = 1000
const TTL_MIN = 1
const TTL_MAX = 720

function ShareLinksBlock({
  workspaceId,
  canWrite,
  locale,
}: {
  workspaceId: string
  canWrite: boolean
  locale: string
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const links = useQuery({
    queryKey: ['ws-share-links', workspaceId] as const,
    queryFn: () => listShareLinks(workspaceId),
  })
  const [linkRole, setLinkRole] = useState<AssignableRole>('member')
  const [maxUses, setMaxUses] = useState('10')
  const [ttlHours, setTtlHours] = useState('168')
  const [formError, setFormError] = useState<string | null>(null)
  // 一次性 code：仅存本组件 state，SecretModal 关闭即不可再取（设计 §4.4）。
  const [createdCode, setCreatedCode] = useState<string | null>(null)

  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ['ws-share-links', workspaceId] })
  const create = useMutation({
    mutationFn: () =>
      createShareLink(workspaceId, linkRole, Number(maxUses), Number(ttlHours)),
    onSuccess: (res) => {
      setCreatedCode(res.code)
      setFormError(null)
      invalidate()
    },
    onError: (err) => setFormError(errMsg(err, t('settingsWs.actionFailed'))),
  })
  const revoke = useMutation({
    mutationFn: (linkId: string) => revokeShareLink(workspaceId, linkId),
    onSuccess: invalidate,
    onError: (err) => setFormError(errMsg(err, t('settingsWs.actionFailed'))),
  })
  const fmtDate = (iso: string): string =>
    new Date(iso).toLocaleDateString(locale === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })

  // 范围校验按任务书：max_uses 1-1000、ttl_hours 1-720（0 不合法），提交前拦。
  const parsedMaxUses = Number(maxUses)
  const parsedTtl = Number(ttlHours)
  const rangeValid =
    Number.isInteger(parsedMaxUses) &&
    parsedMaxUses >= MAX_USES_MIN &&
    parsedMaxUses <= MAX_USES_MAX &&
    Number.isInteger(parsedTtl) &&
    parsedTtl >= TTL_MIN &&
    parsedTtl <= TTL_MAX
  const submit = () => {
    if (!rangeValid) {
      setFormError(t('settingsWs.shareRangeInvalid', { minUses: MAX_USES_MIN, maxUses: MAX_USES_MAX, minH: TTL_MIN, maxH: TTL_MAX }))
      return
    }
    create.mutate()
  }

  return (
    <div className="space-y-3">
      <SubsectionHeader icon={<Link2 className="size-3.5" />} title={t('settingsWs.shareTitle')} />
      {canWrite && (
        <form
          className="flex max-w-md flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <div className="w-28">
            <FormField label={t('settingsWs.shareRoleLabel')} htmlFor="ws-share-role">
              <Select
                id="ws-share-role"
                value={linkRole}
                data-testid="ws-share-role"
                onChange={(e) => setLinkRole(e.target.value as AssignableRole)}
              >
                {ROLES.map((r) => (
                  <option key={r} value={r}>
                    {t(`settingsWs.role.${r}`)}
                  </option>
                ))}
              </Select>
            </FormField>
          </div>
          <div className="w-28">
            <FormField label={t('settingsWs.shareUsesLabel')} htmlFor="ws-share-uses">
              <Input
                id="ws-share-uses"
                type="number"
                min={MAX_USES_MIN}
                max={MAX_USES_MAX}
                value={maxUses}
                onChange={(e) => setMaxUses(e.target.value)}
                data-testid="ws-share-uses"
              />
            </FormField>
          </div>
          <div className="w-28">
            <FormField label={t('settingsWs.shareTtlLabel')} htmlFor="ws-share-ttl">
              <Input
                id="ws-share-ttl"
                type="number"
                min={TTL_MIN}
                max={TTL_MAX}
                value={ttlHours}
                onChange={(e) => setTtlHours(e.target.value)}
                data-testid="ws-share-ttl"
              />
            </FormField>
          </div>
          <Button type="submit" disabled={!rangeValid || create.isPending} data-testid="ws-share-submit">
            {create.isPending ? t('common.loading') : t('settingsWs.shareSubmit')}
          </Button>
        </form>
      )}
      {formError && (
        <p role="alert" className="text-xs text-destructive">
          {formError}
        </p>
      )}
      {links.isPending && (
        <PageLoading label={t('common.loading')} className={INLINE_LOADING_CLS} />
      )}
      {links.isError && (
        <PageError
          title={t('settingsWs.loadFailed')}
          message={errMsg(links.error, '')}
          onRetry={() => void links.refetch()}
          className={INLINE_ERROR_CLS}
        />
      )}
      {links.data && links.data.length === 0 && (
        <p className="text-sm text-zinc-500">{t('settingsWs.shareEmpty')}</p>
      )}
      {links.data && links.data.length > 0 && (
        <ul className="divide-y divide-zinc-100 dark:divide-zinc-800" data-testid="ws-share-list">
          {links.data.map((link: WorkspaceShareLink) => (
            <li key={link.id} className="flex flex-wrap items-center gap-2 py-2 text-sm">
              <code className="font-mono text-xs">{link.code_prefix}…</code>
              <Badge variant="secondary">{t(`settingsWs.role.${link.role}`)}</Badge>
              <span className="text-xs text-zinc-500 dark:text-zinc-400">
                {t('settingsWs.shareUses', { uses: link.uses, max: link.max_uses })}
              </span>
              <span className="ml-auto text-xs text-zinc-500 dark:text-zinc-400">
                {t('settingsWs.expiresAt', { date: fmtDate(link.expires_at) })}
              </span>
              {canWrite && (
                <Button
                  size="xs"
                  variant="ghost"
                  className={DANGER_BTN_CLS}
                  disabled={revoke.isPending}
                  aria-label={t('settingsWs.shareRevokeAria', { prefix: link.code_prefix })}
                  data-testid="ws-share-revoke"
                  onClick={() => revoke.mutate(link.id)}
                >
                  <Trash2 className="size-3.5" aria-hidden="true" />
                  {t('settingsWs.revoke')}
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}

      {/* 一次性 code 展示（ui-sweep 裁决③）：SecretModal 收口，警示内置只说一次 */}
      {createdCode && (
        <SecretModal
          open
          onOpenChange={(o) => {
            if (!o) setCreatedCode(null)
          }}
          secretName={t('settingsWs.shareCodeTitle')}
          secretValue={createdCode}
          title={t('settingsWs.shareCodeTitle')}
        />
      )}
    </div>
  )
}

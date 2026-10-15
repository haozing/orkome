import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { isApiError } from '@ploykit/client'
import { Check, Copy, Link2, Radio, Trash2, UserPlus } from 'lucide-react'
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'

// 成员与邀请区块（settings 域设计 §3.5）：成员表（改角色/移除）+ 邀请管理 +
// 分享链接三段。member 可读（members:read / invites:read），写控件仅对
// owner/admin 渲染（设计 §4.3：区块按 me.workspaces 的 role 条件渲染）。
// 一次性 share code 只存组件 useState，不进 query 缓存（设计 §4.4）。
// 视觉：三个子段 space-y-3 + divide-y 分隔；子段标题 text-sm font-medium，
// 描述 text-xs text-zinc-500；角色/邀请/分享一律 ui/select；危险操作统一
// ghost xs 红字 + Trash2（照 workflow-editor StageCard 删除钮参照）。

export interface MembersSectionProps {
  workspaceId: string
  /** 当前用户在该工作区的 role；owner/admin 可见写控件。 */
  role: string | undefined
}

const ROLES = ['admin', 'member'] as const
type AssignableRole = (typeof ROLES)[number]

const FIELD_LABEL_CLS = 'block text-xs text-zinc-500 dark:text-zinc-400'
// 「移除/吊销」危险按钮统一皮（§危险按钮：ghost xs 红字 + Trash2）。
const DANGER_BTN_CLS = 'text-red-600 hover:text-red-600 dark:text-red-400'

function errMsg(err: unknown, fallback: string): string {
  return isApiError(err) ? err.message : fallback
}

function SubsectionHeader({
  icon,
  title,
  hint,
}: {
  icon: ReactNode
  title: string
  hint: string
}) {
  return (
    <div className="space-y-0.5">
      <p className="flex items-center gap-1.5 text-sm font-medium">
        <span className="flex items-center text-zinc-400 dark:text-zinc-500" aria-hidden="true">
          {icon}
        </span>
        {title}
      </p>
      <p className="text-xs text-zinc-500 dark:text-zinc-400">{hint}</p>
    </div>
  )
}

export function MembersSection({ workspaceId, role }: MembersSectionProps) {
  const { t, i18n } = useTranslation()
  const canWrite = role === 'owner' || role === 'admin'
  return (
    <section
      className="space-y-3 rounded-lg border border-zinc-200 p-4 dark:border-zinc-800"
      aria-label={t('settingsWs.membersTitle')}
      data-testid="ws-members-section"
    >
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
  const canWrite = role === 'owner' || role === 'admin'
  const members = useQuery(membersQueryOptions(workspaceId))
  const [rowError, setRowError] = useState<string | null>(null)
  const [removing, setRemoving] = useState<Member | null>(null)

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
      setRemoving(null)
      setRowError(null)
      void queryClient.invalidateQueries({ queryKey: ['workspace', workspaceId, 'members'] })
    },
    onError: (err) => {
      setRemoving(null)
      setRowError(errMsg(err, t('settingsWs.actionFailed')))
    },
  })

  const fmtDate = (iso: string): string =>
    new Date(iso).toLocaleDateString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })

  return (
    <div className="space-y-3">
      <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('settingsWs.membersHint')}</p>
      {members.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
      {members.isError && (
        <p role="alert" className="text-sm text-destructive">
          {errMsg(members.error, t('settingsWs.loadFailed'))}
        </p>
      )}
      {members.data && members.data.length === 0 && (
        <p className="text-sm text-zinc-500">{t('settingsWs.membersEmpty')}</p>
      )}
      {members.data && members.data.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs" data-testid="ws-members-table">
            <thead>
              <tr className="border-b text-zinc-500 dark:text-zinc-400">
                <th className="py-1.5 pr-3 font-medium">{t('settingsWs.colEmail')}</th>
                <th className="py-1.5 pr-3 font-medium">{t('settingsWs.colDisplayName')}</th>
                <th className="py-1.5 pr-3 font-medium">{t('settingsWs.colRole')}</th>
                <th className="py-1.5 pr-3 font-medium">{t('settingsWs.colJoined')}</th>
                <th className="py-1.5 font-medium" aria-label={t('common.actions')} />
              </tr>
            </thead>
            <tbody>
              {members.data.map((m) => (
                <tr
                  key={m.user_id}
                  className="border-b border-zinc-100 last:border-0 dark:border-zinc-800/60"
                  data-testid="ws-member-row"
                  data-user-id={m.user_id}
                >
                  <td className="max-w-52 truncate py-1.5 pr-3 font-mono">{m.email}</td>
                  <td className="py-1.5 pr-3">{m.display_name || '–'}</td>
                  <td className="py-1.5 pr-3">
                    {canWrite && m.role !== 'owner' ? (
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
                    )}
                  </td>
                  <td className="py-1.5 pr-3 whitespace-nowrap text-zinc-500 dark:text-zinc-400">
                    {fmtDate(m.created_at)}
                  </td>
                  <td className="py-1.5 text-right">
                    {canWrite && (
                      <Button
                        size="xs"
                        variant="ghost"
                        className={DANGER_BTN_CLS}
                        aria-label={t('settingsWs.removeMemberAria', { email: m.email })}
                        data-testid="ws-member-remove"
                        onClick={() => setRemoving(m)}
                      >
                        <Trash2 className="size-3.5" aria-hidden="true" />
                        {t('common.remove')}
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {rowError && (
        <p role="alert" className="text-xs text-destructive">
          {rowError}
        </p>
      )}
      {/* 移除成员二次确认（不能移除自己/最后 owner 由服务端兜底 403/409） */}
      <Dialog
        open={removing !== null}
        onOpenChange={(next) => {
          if (!next) setRemoving(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('settingsWs.removeTitle')}</DialogTitle>
            <DialogDescription>
              {t('settingsWs.removeDesc', { email: removing?.email ?? '' })}
            </DialogDescription>
          </DialogHeader>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setRemoving(null)}>
              {t('common.cancel')}
            </Button>
            <Button
              variant="danger"
              disabled={remove.isPending}
              data-testid="ws-member-remove-confirm"
              onClick={() => removing && remove.mutate(removing.user_id)}
            >
              {remove.isPending ? t('common.loading') : t('common.confirm')}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
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
      <SubsectionHeader
        icon={<UserPlus className="size-3.5" />}
        title={t('settingsWs.invitesTitle')}
        hint={t('settingsWs.invitesHint')}
      />
      {canWrite && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <div className="flex flex-col gap-1">
            <label htmlFor="ws-invite-email" className={FIELD_LABEL_CLS}>
              {t('settingsWs.inviteEmailLabel')}
            </label>
            <Input
              id="ws-invite-email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="name@example.com"
              aria-label={t('settingsWs.inviteEmailLabel')}
              className="min-w-48 flex-1"
              data-testid="ws-invite-email"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label htmlFor="ws-invite-role" className={FIELD_LABEL_CLS}>
              {t('settingsWs.inviteRoleLabel')}
            </label>
            <Select
              id="ws-invite-role"
              value={inviteRole}
              className="w-28"
              data-testid="ws-invite-role"
              onChange={(e) => setInviteRole(e.target.value as AssignableRole)}
            >
              {ROLES.map((r) => (
                <option key={r} value={r}>
                  {t(`settingsWs.role.${r}`)}
                </option>
              ))}
            </Select>
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
      {invitations.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
      {invitations.isError && (
        <p role="alert" className="text-sm text-destructive">
          {errMsg(invitations.error, t('settingsWs.loadFailed'))}
        </p>
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
  // 一次性 code：仅存本组件 state，关 Dialog 即不可再取（设计 §4.4）。
  const [createdCode, setCreatedCode] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)

  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ['ws-share-links', workspaceId] })
  const create = useMutation({
    mutationFn: () =>
      createShareLink(workspaceId, linkRole, Number(maxUses), Number(ttlHours)),
    onSuccess: (res) => {
      setCreatedCode(res.code)
      setCopied(false)
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
  const copyCode = () => {
    if (!createdCode) return
    void navigator.clipboard.writeText(createdCode).then(
      () => {
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      },
      () => {},
    )
  }

  return (
    <div className="space-y-3">
      <SubsectionHeader
        icon={<Link2 className="size-3.5" />}
        title={t('settingsWs.shareTitle')}
        hint={t('settingsWs.shareHint')}
      />
      {canWrite && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <div className="flex flex-col gap-1">
            <label htmlFor="ws-share-role" className={FIELD_LABEL_CLS}>
              {t('settingsWs.shareRoleLabel')}
            </label>
            <Select
              id="ws-share-role"
              value={linkRole}
              className="w-28"
              data-testid="ws-share-role"
              onChange={(e) => setLinkRole(e.target.value as AssignableRole)}
            >
              {ROLES.map((r) => (
                <option key={r} value={r}>
                  {t(`settingsWs.role.${r}`)}
                </option>
              ))}
            </Select>
          </div>
          <div className="flex flex-col gap-1">
            <label htmlFor="ws-share-uses" className={FIELD_LABEL_CLS}>
              {t('settingsWs.shareUsesLabel')}
            </label>
            <Input
              id="ws-share-uses"
              type="number"
              min={MAX_USES_MIN}
              max={MAX_USES_MAX}
              value={maxUses}
              onChange={(e) => setMaxUses(e.target.value)}
              className="w-28"
              data-testid="ws-share-uses"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label htmlFor="ws-share-ttl" className={FIELD_LABEL_CLS}>
              {t('settingsWs.shareTtlLabel')}
            </label>
            <Input
              id="ws-share-ttl"
              type="number"
              min={TTL_MIN}
              max={TTL_MAX}
              value={ttlHours}
              onChange={(e) => setTtlHours(e.target.value)}
              className="w-28"
              data-testid="ws-share-ttl"
            />
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
      {links.isPending && <p className="text-sm text-zinc-500">{t('common.loading')}</p>}
      {links.isError && (
        <p role="alert" className="text-sm text-destructive">
          {errMsg(links.error, t('settingsWs.loadFailed'))}
        </p>
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

      {/* 一次性 code 展示 Dialog：警示关闭后不可再取（照 MFA 备份码/PAT 风格） */}
      <Dialog
        open={createdCode !== null}
        onOpenChange={(next) => {
          if (!next) setCreatedCode(null)
        }}
      >
        <DialogContent data-testid="ws-share-code-dialog">
          <DialogHeader>
            <DialogTitle>{t('settingsWs.shareCodeTitle')}</DialogTitle>
            <DialogDescription>{t('settingsWs.shareCodeDesc')}</DialogDescription>
          </DialogHeader>
          <div className="flex items-center gap-2">
            <code className="min-w-0 flex-1 rounded-md bg-zinc-50 p-2.5 font-mono text-sm break-all dark:bg-zinc-900">
              {createdCode}
            </code>
            <Button size="icon-sm" variant="outline" aria-label={t('common.copy')} onClick={copyCode}>
              {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
            </Button>
          </div>
          <p className="flex items-center gap-1.5 text-xs text-amber-700 dark:text-amber-400">
            <Radio className="size-3.5 shrink-0" aria-hidden="true" />
            {t('settingsWs.shareCodeWarning')}
          </p>
          <div className="flex justify-end">
            <Button onClick={() => setCreatedCode(null)}>{t('common.ok')}</Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}

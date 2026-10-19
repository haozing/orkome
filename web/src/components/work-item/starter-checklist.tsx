import { useMutation, useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { ArrowRight, Bot, Check, UserPlus, FilePlus2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError } from '@ploykit/client'
import { membersQueryOptions } from '@/api/identity'
import { getSetupStatus, type SetupStatus } from '@/api/orchestration'
import { quickCreateWorkItem } from '@/api/work'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useWorkspaceStore } from '@/stores/workspace'

// 起步清单（next-wave-plan W2）：工单空板即第二幕——三张卡把冷启动缝成
// 一条线（建工单 → 配智能体 → 邀队友）。完成态由数据驱动：智能体卡读
// GET /api/setup-status 的 ready 探针（6347945），队友卡读成员数。
// 不做独立 onboarding 页：空态卡片天然只在"还没有任何工单"时出现。

const CARD_CLS =
  'flex flex-col gap-3 rounded-lg border border-zinc-200 p-4 dark:border-zinc-800'
const TITLE_CLS = 'flex items-center gap-2 text-sm font-medium'
const DESC_CLS = 'text-xs leading-relaxed text-zinc-500 dark:text-zinc-400'

function CardBadge({ n, done }: { n: number; done?: boolean }) {
  const { t } = useTranslation()
  return done ? (
    <span
      className="flex size-5 shrink-0 items-center justify-center rounded-full bg-emerald-100 text-emerald-600 dark:bg-emerald-900/50 dark:text-emerald-400"
      aria-label={t('work.start.doneAria')}
    >
      <Check className="size-3" aria-hidden="true" />
    </span>
  ) : (
    <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-zinc-100 text-xs text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400">
      {n}
    </span>
  )
}

function LinkRow({ to, label }: { to: string; label: string }) {
  // kit Button outline（ui-sweep #12：手写链接行归一按钮件）；render 合入
  // router Link 保住 <a> 语义与导航。
  return (
    <Button variant="outline" size="sm" render={<Link to={to} />}>
      {label}
      <ArrowRight className="size-3.5" aria-hidden="true" />
    </Button>
  )
}

export function StarterChecklist() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const workspaceId = useWorkspaceStore((s) => s.currentId) ?? ''
  const [title, setTitle] = useState('')
  const [error, setError] = useState<string | null>(null)

  const setup = useQuery({
    queryKey: ['setup-status', workspaceId],
    queryFn: getSetupStatus,
    enabled: !!workspaceId,
    staleTime: 60_000,
  })
  const members = useQuery(membersQueryOptions(workspaceId))

  const agentReady = (s: SetupStatus | undefined) =>
    !!s?.agents.some((a) => !a.archived && a.ready)
  const memberCount = members.data?.length ?? 0

  const create = useMutation({
    mutationFn: () => quickCreateWorkItem({ title: title.trim() }),
    onSuccess: (wi) => {
      void navigate({ to: '/work-items/$id', params: { id: wi.id } })
    },
    onError: (err) =>
      setError(err instanceof ApiError ? err.message : t('work.start.createFailed')),
  })

  const submit = () => {
    setError(null)
    if (title.trim()) create.mutate()
  }

  return (
    <section aria-label={t('work.start.aria')} className="rounded-lg border border-zinc-200 p-5 dark:border-zinc-800">
      <h2 className="text-sm font-semibold">{t('work.start.title')}</h2>
      <p className={DESC_CLS}>{t('work.start.subtitle')}</p>
      <div className="mt-4 grid gap-3 sm:grid-cols-1 lg:grid-cols-3">
        <div className={CARD_CLS}>
          <div className={TITLE_CLS}>
            <CardBadge n={1} />
            <FilePlus2 className="size-4 text-zinc-400" aria-hidden="true" />
            {t('work.start.itemTitle')}
          </div>
          <p className={DESC_CLS}>{t('work.start.itemDesc')}</p>
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              submit()
            }}
          >
            <Input
              value={title}
              maxLength={200}
              onChange={(e) => setTitle(e.target.value)}
              placeholder={t('work.start.itemPlaceholder')}
              aria-label={t('work.start.itemTitle')}
            />
            <Button size="sm" type="submit" disabled={!title.trim() || create.isPending}>
              {create.isPending ? t('common.saving') : t('work.start.itemGo')}
            </Button>
          </form>
          {error && (
            <p role="alert" className="text-xs text-red-600 dark:text-red-400">
              {error}
            </p>
          )}
        </div>

        <div className={CARD_CLS}>
          <div className={TITLE_CLS}>
            <CardBadge n={2} done={agentReady(setup.data)} />
            <Bot className="size-4 text-zinc-400" aria-hidden="true" />
            {t('work.start.agentTitle')}
          </div>
          <p className={DESC_CLS}>{t('work.start.agentDesc')}</p>
          {agentReady(setup.data) ? (
            <p className="text-xs font-medium text-emerald-600 dark:text-emerald-400">
              {t('work.start.agentDone')}
            </p>
          ) : (
            <div className="flex flex-wrap gap-2">
              <LinkRow to="/access" label={t('work.start.openAccess')} />
              <LinkRow to="/tools" label={t('work.start.openTools')} />
            </div>
          )}
        </div>

        <div className={CARD_CLS}>
          <div className={TITLE_CLS}>
            <CardBadge n={3} done={memberCount > 1} />
            <UserPlus className="size-4 text-zinc-400" aria-hidden="true" />
            {t('work.start.inviteTitle')}
          </div>
          <p className={DESC_CLS}>{t('work.start.inviteDesc')}</p>
          {memberCount > 1 ? (
            <p className="text-xs font-medium text-emerald-600 dark:text-emerald-400">
              {t('work.start.inviteDone', { n: memberCount })}
            </p>
          ) : (
            <LinkRow to="/settings" label={t('work.start.openSettings')} />
          )}
        </div>
      </div>
    </section>
  )
}

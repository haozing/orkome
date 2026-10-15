import { useMutation, useQueryClient } from '@tanstack/react-query'
import { isApiError } from '@ploykit/client'
import { Check } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { renameWorkspace, type WorkspaceAdminRecord } from '@/api/workspace-admin'
import type { Workspace } from '@/api/identity'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

// 工作区概览区块（settings 域设计 §3.4）：name 编辑（admin）+ slug/plan_code/
// created_at 只读；member 整块只读。成功后失效 me query 缓存——WorkspaceSwitcher
// 订阅同一缓存，改名后自动跟着刷（任务书要求）。
// 自包含取数（props 只传 workspaceId）：合并进 settings 壳时无需壳层喂数据。
// 视觉：名称编辑行独立置顶，只读定义行用两列 dl 与编辑行 divide 分隔。

export interface WorkspaceOverviewSectionProps {
  /** 当前工作区 id（X-Workspace-Id 的值；空时不渲染写操作）。 */
  workspaceId: string
  /** 当前工作区数据（从 me.workspaces 里取——role 字段也在其中）。 */
  workspace: Workspace
  /** 当前用户在该工作区的 role（owner/admin 可写，member 只读）。 */
  role: string | undefined
}

export function WorkspaceOverviewSection({
  workspaceId,
  workspace,
  role,
}: WorkspaceOverviewSectionProps) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const canEdit = role === 'owner' || role === 'admin'
  const [name, setName] = useState(workspace.name)
  const [saved, setSaved] = useState(false)
  // 工作区切换/改名成功回填（React 官方「渲染时随 props 调整 state」模式，
  // 不用 effect——react-hooks/set-state-in-effect 禁 effect 里同步 setState）：
  // props 的 name 变了就丢弃草稿，并重置上一次渲染的基线。
  const [lastName, setLastName] = useState(workspace.name)
  if (workspace.name !== lastName) {
    setLastName(workspace.name)
    setName(workspace.name)
    setSaved(false)
  }

  const save = useMutation({
    mutationFn: () => renameWorkspace(workspaceId, name.trim()),
    onSuccess: (updated: WorkspaceAdminRecord) => {
      setSaved(true)
      // 写回 me 缓存里对应工作区的 name（WorkspaceSwitcher 跟着刷）；
      // created_at/plan_code 以后台真值为准，一并回写。
      queryClient.setQueryData<{ user: unknown; workspaces: Workspace[] }>(['me'], (me) =>
        me
          ? {
              ...me,
              workspaces: me.workspaces.map((ws) =>
                ws.id === updated.id
                  ? { ...ws, name: updated.name, plan_code: updated.plan_code }
                  : ws,
              ),
            }
          : me,
      )
      queryClient.invalidateQueries({ queryKey: ['me'] })
    },
  })
  const submit = () => {
    const trimmed = name.trim()
    if (!trimmed || trimmed === workspace.name || save.isPending) return
    save.mutate()
  }
  const fmtDate = (iso: string): string =>
    new Date(iso).toLocaleDateString(i18n.language === 'zh' ? 'zh-CN' : 'en-US', {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })

  return (
    <section
      className="rounded-lg border border-zinc-200 p-4 dark:border-zinc-800"
      aria-label={t('settingsWs.overviewTitle')}
      data-testid="ws-overview-section"
    >
      <h2 className="text-sm font-semibold">{t('settingsWs.overviewTitle')}</h2>
      <div className="mt-3 divide-y divide-zinc-100 dark:divide-zinc-800">
        {/* 名称编辑行独立置顶：label + Input + 保存按钮同行 */}
        <div className="space-y-2 pb-3">
          {canEdit ? (
            <div className="flex flex-wrap items-center gap-2">
              <label htmlFor="ws-overview-name-input" className="text-sm font-medium">
                {t('settingsWs.nameLabel')}
              </label>
              <Input
                id="ws-overview-name-input"
                value={name}
                onChange={(e) => {
                  setName(e.target.value)
                  setSaved(false)
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') submit()
                }}
                maxLength={100}
                aria-label={t('settingsWs.nameLabel')}
                className="max-w-md"
                data-testid="ws-overview-name-input"
              />
              <Button
                size="sm"
                disabled={!name.trim() || name.trim() === workspace.name || save.isPending}
                data-testid="ws-overview-save"
                onClick={submit}
              >
                {save.isPending ? t('common.saving') : t('common.save')}
              </Button>
              {saved && !save.isPending && (
                <span
                  className="flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400"
                  data-testid="ws-overview-saved"
                >
                  <Check className="size-3.5" aria-hidden="true" />
                  {t('settingsWs.renameSaved')}
                </span>
              )}
            </div>
          ) : (
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-sm font-medium">{t('settingsWs.nameLabel')}</span>
              <span className="text-sm">{workspace.name}</span>
            </div>
          )}
          {save.error && (
            <p role="alert" className="text-xs text-destructive">
              {isApiError(save.error) ? save.error.message : t('common.saveFailed')}
            </p>
          )}
          {!canEdit && (
            <p className="text-xs text-zinc-500 dark:text-zinc-400">
              {t('settingsWs.adminOnlyHint')}
            </p>
          )}
        </div>
        {/* 只读定义行：两列 dl，与编辑行 divide 分隔 */}
        <div className="pt-3">
          <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1.5 text-sm">
            <dt className="text-xs leading-6 text-zinc-500 dark:text-zinc-400">
              {t('settingsWs.slugLabel')}
            </dt>
            <dd className="font-mono leading-6">{workspace.slug}</dd>
            <dt className="text-xs leading-6 text-zinc-500 dark:text-zinc-400">
              {t('settingsWs.planLabel')}
            </dt>
            <dd className="leading-6">{workspace.plan_code || '–'}</dd>
            <dt className="text-xs leading-6 text-zinc-500 dark:text-zinc-400">
              {t('settingsWs.createdAtLabel')}
            </dt>
            <dd className="leading-6">{fmtDate(workspace.created_at)}</dd>
          </dl>
        </div>
      </div>
    </section>
  )
}

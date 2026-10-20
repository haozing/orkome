import { useQuery, useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { PageHeader } from '@ploykit/ui'
import { useTranslation } from 'react-i18next'
import { meQueryOptions } from '@/api/identity'
import { AuditSection } from '@/components/settings/audit-section'
import { MembersSection } from '@/components/settings/members-section'
import { WorkspaceOverviewSection } from '@/components/settings/workspace-overview-section'
import { Button } from '@/components/ui/button'
import { switchWorkspace } from '@/services/workspace-switch'
import { useWorkspaceStore } from '@/stores/workspace'

export const Route = createFileRoute('/_app/settings/workspace')({
  component: WorkspaceSettingsPage,
})

// 工作区设置页（自 /settings 迁出，IA 裁决：工作区内容独立成页）。
// 四块：切换列表 + 概览（改名/slug/套餐/创建时间）+ 成员与邀请 + 审计日志。
// 组件原样 import（INLINE_*_CLS 经 settings/shared 提供，import 链不变）。
// 布局沿用 settings 域的块卡 + divide-y 分隔（security 范式）。
const SECTION_CLASS = 'rounded-lg border border-zinc-200 p-4 dark:border-zinc-800'

function WorkspaceSettingsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const me = useQuery(meQueryOptions)
  const currentId = useWorkspaceStore((s) => s.currentId)
  const current = me.data?.workspaces.find((ws) => ws.id === currentId)
  return (
    <div>
      <PageHeader title={t('kitLabels.workspaceSettings')} />
      <div className="min-w-0 space-y-6">
        {/* ---- 【工作区】切换列表（原 settings#workspace 段迁入） ---- */}
        <div className={`${SECTION_CLASS} space-y-3`}>
          <h2 className="text-sm font-semibold">{t('settings.nav.workspace')}</h2>
          {me.data && me.data.workspaces.length > 0 && (
            <div className="space-y-2 text-sm">
              <p className="text-xs text-zinc-500 dark:text-zinc-400">
                {t('settings.workspaceHint')}
              </p>
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
            </div>
          )}
        </div>

        {/* ---- 概览 / 成员与邀请 / 审计（组件原样重排） ---- */}
        {me.data && current && (
          <div className={`${SECTION_CLASS} divide-y divide-zinc-100 dark:divide-zinc-800`}>
            <div className="pb-4">
              <WorkspaceOverviewSection
                workspaceId={current.id}
                workspace={current}
                role={current.role}
              />
            </div>
            <div className="py-4">
              <MembersSection workspaceId={current.id} role={current.role} />
            </div>
            <div className="pt-4">
              <AuditSection workspaceId={current.id} />
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

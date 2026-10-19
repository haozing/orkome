import { useQuery } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'
import { PageHeader } from '@ploykit/ui'
import { useTranslation } from 'react-i18next'
import { meQueryOptions } from '@/api/identity'
import { UiEntriesSection } from '@/components/integrations/ui-entries-section'
import { WebhookSubscriptionsSection } from '@/components/integrations/webhook-subscriptions-section'
import { WorkspaceMcpSection } from '@/components/packs-sections'
import { useWorkspaceStore } from '@/stores/workspace'

// 集成页（docs/integrations-page-design-2026-10-15.md）：工作区出线配置总面，三段——
// ① MCP 服务器（注册表 CRUD）② Webhook 订阅（自 settings 迁来，锚点 #webhooks，
// pack-detail 事件区块链入）③ 页面入口（侧栏深链 + 工单页签；读 member/写 admin）。
// 能力包退幕后为安装台账：装包走 pack-provision CLI（pack.yaml 分发），
// 包详情经服务器/入口行内「来自包」徽标链入（/packs/{id} 五区块保留）。
export const Route = createFileRoute('/_app/tools')({
  component: IntegrationsPage,
})

function IntegrationsPage() {
  const { t } = useTranslation()
  const workspaceId = useWorkspaceStore((s) => s.currentId)
  const me = useQuery(meQueryOptions)
  // 写控件按当前工作区 role 显隐（同 settings 壳做法；端点侧 admin 门槛不变）
  const role = me.data?.workspaces.find((w) => w.id === workspaceId)?.role
  return (
    <div className="space-y-8">
      {/* ui-sweep 修复⑪：h1+intro 手写行 → kit PageHeader；intro 合规文案见 i18n patch */}
      <PageHeader title={t('tools.pageTitle')} description={t('tools.pageIntro')} />

      {/* ① MCP 服务器：区块描述整行删（R2「接线板」比喻 + R5「管理在包详情页」位置参照） */}
      <section className="space-y-3">
        <h2 className="text-sm font-semibold">{t('tools.serversSectionTitle')}</h2>
        <WorkspaceMcpSection workspaceId={workspaceId} />
      </section>

      {/* ② Webhook 订阅：锚点 id="webhooks" 由组件 section 自带（pack-detail → /tools#webhooks）；
          组件自带标题，不再套壳标题 */}
      <div className="border-t border-zinc-200 pt-6 dark:border-zinc-800">
        {workspaceId && <WebhookSubscriptionsSection workspaceId={workspaceId} role={role} />}
      </div>

      {/* ③ 页面入口 */}
      <section className="space-y-3 border-t border-zinc-200 pt-6 dark:border-zinc-800">
        <div>
          <h2 className="text-sm font-semibold">{t('tools.uiEntriesTitle')}</h2>
          <p className="text-xs text-zinc-500 dark:text-zinc-400">{t('tools.uiEntriesIntro')}</p>
        </div>
        <UiEntriesSection workspaceId={workspaceId} role={role} />
      </section>
    </div>
  )
}

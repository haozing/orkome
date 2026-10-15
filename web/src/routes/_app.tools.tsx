import { createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { PacksCardsSection, WorkspaceMcpSection } from '@/components/packs-sections'
import { useWorkspaceStore } from '@/stores/workspace'

// 集成页（原 /tools「MCP 服务」+ /packs「能力包」列表合并，2026-10-15 裁决）：
// 外部能力接入的单一入口——上段「已装能力包」（安装视图，点卡片进五区块
// 详情），下段「MCP 服务器」（接线视图，注册/探针/编辑 CRUD）。
// 两页合一的依据：pack 本体只是归属标签（五区块全是按 pack_id 的投影），
// 用户要的是一个入口；详情页 /packs/{id} 与安装向导 /packs/install 路径
// 保留，/packs 列表重定向到本页。
export const Route = createFileRoute('/_app/tools')({
  component: IntegrationsPage,
})
function IntegrationsPage() {
  const { t } = useTranslation()
  const workspaceId = useWorkspaceStore((s) => s.currentId)
  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-lg font-semibold">{t('tools.pageTitle')}</h1>
        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{t('tools.pageIntro')}</p>
      </div>
      <PacksCardsSection />
      <section className="space-y-3 border-t border-zinc-200 pt-6 dark:border-zinc-800">
        <div>
          <h2 className="text-sm font-semibold">{t('tools.serversSectionTitle')}</h2>
          <p className="text-xs text-zinc-500 dark:text-zinc-400">
            {t('tools.serversSectionIntro')}
          </p>
        </div>
        <WorkspaceMcpSection workspaceId={workspaceId} />
      </section>
    </div>
  )
}

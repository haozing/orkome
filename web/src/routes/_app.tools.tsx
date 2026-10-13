import { createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { PacksInstalledSection, WorkspaceMcpSection } from '@/components/packs-sections'
import { useWorkspaceStore } from '@/stores/workspace'

// 工具集成（AI团队组）：智能体消费工具的配置面。
// Packs 是工具的来源渠道（安装产出工作区级 MCP server），MCP 注册表是工具
// 的接线板；agent 级私有服务器在 agent 详情页 MCP tab 管理，两者并存。
export const Route = createFileRoute('/_app/tools')({
  component: ToolsIntegrationPage,
})
function ToolsIntegrationPage() {
  const { t } = useTranslation()
  const workspaceId = useWorkspaceStore((s) => s.currentId)
  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-lg font-semibold">{t('tools.pageTitle')}</h1>
        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{t('tools.pageIntro')}</p>
      </div>
      {/* 第 1 段：Packs（已安装，只读；安装走 pack-provision CLI，前端不做向导） */}
      <PacksInstalledSection />
      {/* 第 2 段：工作区级 MCP 服务器注册表 */}
      <WorkspaceMcpSection workspaceId={workspaceId} />
    </div>
  )
}

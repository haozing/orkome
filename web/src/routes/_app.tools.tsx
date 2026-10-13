import { createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { WorkspaceMcpSection } from '@/components/packs-sections'
import { useWorkspaceStore } from '@/stores/workspace'

// MCP 服务页（AI团队组）：智能体消费工具的接线板——工作区级 MCP 注册表。
// Pack 来源的行只读（归属由能力包页 /packs 管理，行内「管理→」链接过去）；
// agent 级私有服务器在 agent 详情页 MCP tab 管理，两者并存。
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
      <WorkspaceMcpSection workspaceId={workspaceId} />
    </div>
  )
}

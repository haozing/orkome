import { createFileRoute, Link } from '@tanstack/react-router'
import { Check, Copy } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { TokensSection } from '@/components/settings/tokens-section'
import { Button } from '@/components/ui/button'

// 助理接入页（docs/assistant-access-design-2026-10-14.md §五，v4 纯助理）：
// 外部 AI 客户端（Claude Code / Codex / 任意 MCP 客户端）用「用户自己的 PAT」
// 经 per-workspace 端点 /api/mcp/w/{ws}/me 接入——AI 以用户身份干活，作者=用户。
// 与「工具集成」（/tools，消费方：智能体连外部 MCP server）方向相反、受众不同，
// 故分两页。工人接入段（agent 选择器/铸造 runtime credential/撤销列表）已随 v4
// 工人模型退役整体删除。
export const Route = createFileRoute('/_app/access')({
  component: AccessPage,
})

// 工具清单：助理 MCP 端点注册的 26 个真实工具（设计 v4 §3.2 + 知识库读 +
// 配置面两期），名字与后端注册面一一对应（页面不导入服务端包，改动需双方同步）。
// 分五组展示：读 7 + 写 3 + 任务协议 5 + 恢复 1 + 配置 10；无"即将上线"徽标。
const MCP_TOOLS: {
  name: string
  desc: string
  group: 'read' | 'write' | 'protocol' | 'recover' | 'config'
}[] =
  [
    { name: 'list_work_items', desc: 'mcp.toolListWorkItems', group: 'read' },
    { name: 'get_work_item', desc: 'mcp.toolGetWorkItem', group: 'read' },
    { name: 'list_projects', desc: 'mcp.toolListProjects', group: 'read' },
    { name: 'list_labels', desc: 'mcp.toolListLabels', group: 'read' },
    { name: 'list_statuses', desc: 'mcp.toolListStatuses', group: 'read' },
    { name: 'list_skills', desc: 'mcp.toolListSkills', group: 'read' },
    { name: 'read_skill', desc: 'mcp.toolReadSkill', group: 'read' },
    { name: 'create_work_item', desc: 'mcp.toolCreateWorkItem', group: 'write' },
    { name: 'update_work_item', desc: 'mcp.toolUpdateWorkItem', group: 'write' },
    { name: 'add_comment', desc: 'mcp.toolAddComment', group: 'write' },
    { name: 'list_my_claims', desc: 'mcp.toolListMyClaims', group: 'protocol' },
    { name: 'claim_work_item', desc: 'mcp.toolClaimWorkItem', group: 'protocol' },
    { name: 'report_progress', desc: 'mcp.toolReportProgress', group: 'protocol' },
    { name: 'complete_work_item', desc: 'mcp.toolCompleteWorkItem', group: 'protocol' },
    { name: 'fail_work_item', desc: 'mcp.toolFailWorkItem', group: 'protocol' },
    { name: 'get_my_running_tasks', desc: 'mcp.toolGetMyRunningTasks', group: 'recover' },
    // 配置面第一期（AI 即安装器）：与集成页手动能力同集，写工具 admin 门槛。
    { name: 'list_mcp_servers', desc: 'mcp.toolListMcpServers', group: 'config' },
    { name: 'list_webhook_subscriptions', desc: 'mcp.toolListSubscriptions', group: 'config' },
    { name: 'list_page_entries', desc: 'mcp.toolListPageEntries', group: 'config' },
    { name: 'create_credential', desc: 'mcp.toolCreateCredential', group: 'config' },
    { name: 'create_mcp_server', desc: 'mcp.toolCreateMcpServer', group: 'config' },
    { name: 'create_webhook_subscription', desc: 'mcp.toolCreateSubscription', group: 'config' },
    { name: 'put_page_entry', desc: 'mcp.toolPutPageEntry', group: 'config' },
    // 包级（第二期：AI 即安装器）。
    { name: 'list_packs', desc: 'mcp.toolListPacks', group: 'config' },
    { name: 'register_pack', desc: 'mcp.toolRegisterPack', group: 'config' },
    { name: 'uninstall_pack', desc: 'mcp.toolUninstallPack', group: 'config' },
  ]
const CARD_CLASS = 'rounded-lg border border-zinc-200 p-4 dark:border-zinc-800'
const CARD_TITLE_CLASS = 'mb-1 text-sm font-semibold'
const CARD_DESC_CLASS = 'mb-3 text-xs text-zinc-500 dark:text-zinc-400'
function CopyButton({ text }: { text: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={() => {
        void navigator.clipboard.writeText(text).then(
          () => {
            setCopied(true)
            setTimeout(() => setCopied(false), 1500)
          },
          () => {},
        )
      }}
    >
      {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      {copied ? t('common.copied') : t('common.copy')}
    </Button>
  )
}
function Snippet({ title, file, code }: { title: string; file?: string; code: string }) {
  return (
    <div className={CARD_CLASS}>
      <div className="mb-2 flex items-center justify-between gap-2">
        <p className="text-sm font-medium">
          {title}
          {file && <span className="ml-2 font-mono text-xs text-zinc-400">{file}</span>}
        </p>
        <CopyButton text={code} />
      </div>
      <pre className="overflow-x-auto rounded-md bg-zinc-950 p-3 font-mono text-xs leading-relaxed text-zinc-100">
        {code}
      </pre>
    </div>
  )
}
// 导出供测试直接渲染（同 _app.automation 的 AutomationPage 模式）。
export function AccessPage() {
  const { t } = useTranslation()

  // 全站统一接入文档（2026-10-16 裁决）：固定链接、与工作区无关——
  // 文档里端点用 {workspaceId} 占位，AI 拿到令牌后自行发现工作区（文档内
  // 附 GET /api/workspaces 的发现指令）。README/官网可引同一条链接。
  const skillDocUrl = `${window.location.origin}/api/mcp/skill.md`

  // 工具清单按"读/写/任务协议/恢复"分组（与设计 v4 §3.2 的四组一一对应）。
  const toolGroups: {
    key: 'read' | 'write' | 'protocol' | 'recover' | 'config'
    titleKey: string
  }[] = [
    { key: 'read', titleKey: 'mcp.toolsGroupRead' },
    { key: 'write', titleKey: 'mcp.toolsGroupWrite' },
    { key: 'protocol', titleKey: 'mcp.toolsGroupProtocol' },
    { key: 'recover', titleKey: 'mcp.toolsGroupRecover' },
    { key: 'config', titleKey: 'mcp.toolsGroupConfig' },
  ]

  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-lg font-semibold">{t('access.pageTitle')}</h1>
        <p className="mt-1 text-sm text-zinc-500 dark:text-zinc-400">{t('access.pageIntro')}</p>
      </div>

      {/* 第 1 段：三步接入（v4 §五①）+ 接单一句话 */}
      <section className="space-y-3">
        <h2 className="text-sm font-semibold">{t('mcp.connectTitle')}</h2>
        <ol className="space-y-1 text-sm text-zinc-600 dark:text-zinc-300">
          <li>{t('access.step1')}</li>
          <li>{t('access.step2')}</li>
          <li>{t('access.step3')}</li>
        </ol>
        <p className={CARD_DESC_CLASS}>{t('access.claimHint')}</p>
        {/* 三张客户端配置卡已删（2026-10-15 用户裁决）：接入指引收敛到页尾「一键接入命令」，
            本段只保留风险提示。 */}
        <p className={CARD_DESC_CLASS}>{t('mcp.replaceTokenHint')}</p>
      </section>

      {/* 第 2 段：工具清单（23 个真实注册面，按 读/写/任务协议/恢复/配置 分组） */}
      <section className={CARD_CLASS}>
        <h2 className={CARD_TITLE_CLASS}>{t('mcp.toolsTitle')}</h2>
        <p className={CARD_DESC_CLASS}>{t('mcp.toolsDesc')}</p>
        <div className="space-y-4" data-testid="access-tool-list">
          {toolGroups.map((group) => (
            <div key={group.key}>
              <p className="mb-1.5 text-xs font-semibold text-zinc-400">
                {t(group.titleKey)}（{MCP_TOOLS.filter((tool) => tool.group === group.key).length}）
              </p>
              <ul className="grid gap-x-6 gap-y-2 sm:grid-cols-2">
                {MCP_TOOLS.filter((tool) => tool.group === group.key).map((tool) => (
                  <li key={tool.name} className="flex flex-col gap-0.5">
                    <code className="font-mono text-xs font-medium">{tool.name}</code>
                    <span className="text-xs text-zinc-500 dark:text-zinc-400">
                      {t(tool.desc)}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </section>

      {/* 第 3 段：访问凭证 = 个人访问令牌 PAT（v4 §五③：本页主角）。
          管理面（列表/创建/撤销）已抽为共享组件 tokens-section（design settings-domain
          §3.3：归宿在 settings 安全区块），此处引用同组件保留接入语境；
          完整管理入口链接到 /settings#tokens。 */}
      <section className={CARD_CLASS}>
        <h2 className={CARD_TITLE_CLASS}>{t('access.patTitle')}</h2>
        <p className={CARD_DESC_CLASS}>{t('access.patDesc')}</p>
        <p className="mb-3 text-xs">
          <Link
            to="/settings"
            className="text-primary underline-offset-4 hover:underline"
            data-testid="access-tokens-link"
          >
            {t('access.manageInSettings')}
          </Link>
        </p>
        <TokensSection />
      </section>

      {/* 第 4 段：通用一键接入（2026-10-15 用户裁决）——接入即一份 skill 文档：
          用户把文档地址+令牌发给任意 AI 客户端，AI 读完自行完成 MCP 注册并
          学会全部工具用法（文档由服务端从注册面自动生成，工具目录零漂移）。 */}
      <section className={CARD_CLASS}>
        <h2 className={CARD_TITLE_CLASS}>{t('access.oneCmdTitle')}</h2>
        <p className={CARD_DESC_CLASS}>{t('access.oneCmdDesc')}</p>
        <Snippet title={t('access.skillDocTitle')} file="skill.md" code={skillDocUrl} />
        <p className={CARD_DESC_CLASS}>{t('access.oneCmdHint')}</p>
      </section>
    </div>
  )
}

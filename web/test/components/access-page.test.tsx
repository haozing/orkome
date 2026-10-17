import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import i18n from '@/i18n'
import { AccessPage } from '@/routes/_app.access'
import { useWorkspaceStore } from '@/stores/workspace'
import { server } from '../msw'

// 路由页导入链经 TokensSection → settings/shared.ts 拉入 router-singleton →
// routeTree（全路由循环导入），模块级 createFileRoute 注册在此环境下解析出
// undefined。桩掉单例切断循环——本测试只关心接入页文案与 PAT 区块行为。
vi.mock('@/router-singleton', () => ({ router: { navigate: vi.fn() } }))
// AccessPage 内的 <Link> 需要 RouterProvider 上下文；页面测试裸渲染时降级为
// 静态 <a>（导航行为不属于本测试断言面）。
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    Link: ({ to, children, ...rest }: { to: string; children?: React.ReactNode }) => (
      <a href={to} {...rest}>
        {children}
      </a>
    ),
  }
})

// 助理接入页 v4（docs/assistant-access-design-2026-10-14.md §五）重写后的回归面：
// ①工人接入段（agent 选择器/铸造 runtime credential）整体消失——v4 删除，不再回归；
// ②三步接入文案换成「建令牌→把接入文档发给 AI→对 AI 说『帮我把 X 标完成』」+ 接单一句话；
// ③工具清单 24 个真实工具名（读7+写4+任务协议5+恢复1+配置7，2026-10-17 +list_agents
//   归写组与生成器 skillDocGroupOf 一致；包级 pack 工具随能力包退役拆除），无"即将上线"；
// ④统一文档 skill.md 固定链接 + 本工作区完整端点 /api/mcp/w/{currentId}/me
//   （2026-10-17 端点卡片回归：currentId 在 path，用当前工作区拼真实值）；
// ⑤PAT 段保留为主角。
// setup.ts 固定 i18n=en，断言用英文文案。

const PAT = {
  id: 'tok-1',
  name: 'codex',
  prefix: 'ork_abcd',
  created_at: '2026-10-02T00:00:00Z',
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <AccessPage />
    </QueryClientProvider>,
  )
}

// workspace store 是持久化的全局单例：逐测重置，避免端点卡片断言互相污染。
beforeEach(() => {
  useWorkspaceStore.setState({ currentId: null })
})

describe('AccessPage (assistant-only, v4)', () => {
  it('leads with the setup document; three-step copy retired (2026-10-16 slimming)', async () => {
    server.use(http.get('/api/tokens', () => HttpResponse.json([PAT])))
    renderPage()
    // 文档段置顶：标题/文档地址/引导句都在，且无旧三步与接单文案。
    expect(await screen.findByText('One-click setup')).toBeInTheDocument()
    expect(screen.getByText(/\/api\/mcp\/skill\.md/)).toBeInTheDocument()
    expect(screen.queryByText(/Create a personal access token/)).not.toBeInTheDocument()
    expect(screen.queryByText(/mark X as done/)).not.toBeInTheDocument()
    expect(screen.queryByText(/watch the items assigned to me/)).not.toBeInTheDocument()
  })

  it('lists exactly the 24 real assistant tools with no coming-soon badge', async () => {
    server.use(http.get('/api/tokens', () => HttpResponse.json([PAT])))
    renderPage()
    await screen.findByText('Tool catalog')
    const list = screen.getByTestId('access-tool-list')
    for (const tool of [
      'list_work_items',
      'get_work_item',
      'list_projects',
      'list_labels',
      'list_statuses',
      'list_skills',
      'read_skill',
      'create_work_item',
      'list_agents',
      'update_work_item',
      'add_comment',
      'list_my_claims',
      'claim_work_item',
      'report_progress',
      'complete_work_item',
      'fail_work_item',
      'get_my_running_tasks',
      'list_mcp_servers',
      'list_webhook_subscriptions',
      'list_page_entries',
      'create_credential',
      'create_mcp_server',
      'create_webhook_subscription',
      'put_page_entry',
    ]) {
      expect(list).toHaveTextContent(tool)
    }
    // 分组计数与生成器一致：list_agents 归写组（读7+写4+任务协议5+恢复1+配置7=24）。
    expect(screen.getByText('Read（7）')).toBeInTheDocument()
    expect(screen.getByText('Write（4）')).toBeInTheDocument()
    expect(screen.getByText('Task protocol（5）')).toBeInTheDocument()
    expect(screen.getByText('Recover（1）')).toBeInTheDocument()
    expect(screen.getByText('Configuration (integrations)（7）')).toBeInTheDocument()
    // 新参数入 i18n 描述（assignee_agent_id / probe）。
    expect(screen.getByText(/assignee_agent_id reassigns to an agent/)).toBeInTheDocument()
    expect(screen.getByText(/probe=true dry-runs connectivity/)).toBeInTheDocument()
    expect(screen.getByText(/ready=false means missing model config/)).toBeInTheDocument()
    // 能力包退役：包级三工具不得回流（连同工人面幽灵工具）。
    for (const ghost of [
      'claim_task',
      'complete_task',
      'fail_task',
      'get_task_messages',
      'list_packs',
      'register_pack',
      'uninstall_pack',
    ]) {
      expect(list).not.toHaveTextContent(ghost)
    }
    // v4 无"即将上线"徽标。
    expect(screen.queryByText('Coming soon')).not.toBeInTheDocument()
  })

  it('shows the workspace-bound full endpoint card (current workspace id in path)', async () => {
    server.use(http.get('/api/tokens', () => HttpResponse.json([PAT])))
    useWorkspaceStore.setState({ currentId: 'ws-42' })
    renderPage()
    await screen.findByText('Tool catalog')
    const card = screen.getByTestId('access-ws-endpoint')
    expect(card).toHaveTextContent('Full endpoint for this workspace')
    expect(card).toHaveTextContent(/\/api\/mcp\/w\/ws-42\/me/)
    // 与统一文档并列存在，不互斥。
    expect(screen.getAllByText(/\/api\/mcp\/skill\.md/).length).toBeGreaterThanOrEqual(1)
  })

  it('shows the site-wide universal setup doc link (workspace-independent)', async () => {
    server.use(http.get('/api/tokens', () => HttpResponse.json([PAT])))
    renderPage()
    await screen.findByText('Tool catalog')
    // 固定链接（skill.md 版）：与工作区无关，全站同一条。
    expect(await screen.findAllByText(/\/api\/mcp\/skill\.md/)).toBeTruthy()
    // 端点卡片冷启动（无 currentId）渲染 {workspaceId} 占位，与统一文档口径一致。
    expect(screen.getByText(/\/api\/mcp\/w\/\{workspaceId\}\/me/)).toBeInTheDocument()
    // 旧 /agent 端点形态绝迹。
    expect(screen.queryByText(/\/api\/mcp\/w\/[^"'\s{}]*\/agent/)).not.toBeInTheDocument()
    expect(screen.queryByText(/claude mcp add/)).not.toBeInTheDocument()
  })

  it('keeps the PAT section as the credential protagonist', async () => {
    server.use(
      http.get('/api/tokens', () =>
        HttpResponse.json([
          PAT,
          { ...PAT, id: 'tok-2', name: 'ci', revoked_at: '2026-10-03T00:00:00Z' },
        ]),
      ),
    )
    renderPage()
    expect(await screen.findByText('Access credential (personal access token, PAT)')).toBeInTheDocument()
    // 风险明示文案（v4 §3.3：AI 客户端持令牌=账号完整权限）。replaceTokenHint
    // 里也出现 "full account permissions"（两处），断言用 getAllByText。
    expect(
      screen.getAllByText(/full account permissions/).length,
    ).toBeGreaterThanOrEqual(1)
    await waitFor(() => {
      expect(screen.getByText('codex')).toBeInTheDocument()
      expect(screen.getByText('ci')).toBeInTheDocument()
      expect(screen.getByText('Revoked')).toBeInTheDocument()
    })
  })

  it('has no worker-access section remnants (runtime credential flow removed)', async () => {
    server.use(http.get('/api/tokens', () => HttpResponse.json([PAT])))
    renderPage()
    await screen.findByText('Tool catalog')
    // 旧工人段文案不得回流（中英任一出现都算回归）。
    expect(screen.queryByText(/Mint credential/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Runtime/)).not.toBeInTheDocument()
    // 旧版三步里的"选一个智能体"文案绝迹。
    expect(screen.queryByText(/Pick an agent/)).not.toBeInTheDocument()
    void i18n
  })
})

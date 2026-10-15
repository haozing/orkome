import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
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
// ②三步接入文案换成「建令牌→贴配置→对 AI 说『帮我把 X 标完成』」+ 接单一句话；
// ③工具清单 14 个真实工具名（读5+写3+任务协议5+恢复1），无"即将上线"；
// ④snippet 端点是 /api/mcp/w/{ws}/me（workspace 在 path，用当前工作区拼真实值）；
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

describe('AccessPage (assistant-only, v4)', () => {
  it('renders the three-step copy and the claim hand-off hint', async () => {
    server.use(http.get('/api/tokens', () => HttpResponse.json([PAT])))
    renderPage()
    expect(await screen.findByText(/Create a personal access token/)).toBeInTheDocument()
    expect(screen.getByText(/Paste the config below into your client/)).toBeInTheDocument()
    expect(screen.getByText(/mark X as done/)).toBeInTheDocument()
    // 接单一句话（§五①）
    expect(screen.getByText(/watch the items assigned to me/)).toBeInTheDocument()
  })

  it('lists exactly the 14 real assistant tools with no coming-soon badge', async () => {
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
      'create_work_item',
      'update_work_item',
      'add_comment',
      'list_my_claims',
      'claim_work_item',
      'report_progress',
      'complete_work_item',
      'fail_work_item',
      'get_my_running_tasks',
    ]) {
      expect(list).toHaveTextContent(tool)
    }
    // 旧 5 个虚构工具（二轮审计确认从未存在于服务端）不得回流。
    for (const ghost of ['claim_task', 'complete_task', 'fail_task', 'get_task_messages']) {
      expect(list).not.toHaveTextContent(ghost)
    }
    // v4 无"即将上线"徽标。
    expect(screen.queryByText('Coming soon')).not.toBeInTheDocument()
  })

  it('builds snippet URLs from the current workspace with /me path', async () => {
    server.use(http.get('/api/tokens', () => HttpResponse.json([PAT])))
    useWorkspaceStore.setState({ currentId: 'ws-42' })
    renderPage()
    await screen.findByText('Tool catalog')
    // 旧端点 /agent 必须绝迹；新端点带真实 workspace id。
    expect(await screen.findAllByText(/\/api\/mcp\/w\/ws-42\/me/)).toBeTruthy()
    expect(screen.queryByText(/\/api\/mcp\/w\/[^"'\s/]*\/agent/)).not.toBeInTheDocument()
    // 一键命令同样换端点。
    const cmd = screen.getByText(/claude mcp add/)
    expect(cmd.textContent).toContain('/api/mcp/w/ws-42/me')
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

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AgentsPage } from '@/routes/_app.agents.index'
import { useWorkspaceStore } from '@/stores/workspace'
import { server } from '../msw'

// 智能体列表页 ui-sweep 钉子：筛选条曾用 role=tablist/tab/aria-selected，但没有
// 对应 tabpanel（读屏语义错）；裁决改为 role=group + aria-pressed 按钮组。
// 断言只钉结构与既有键的英文文案，不依赖待主线合入的 i18n patch。
// setup.ts 固定 i18n=en。

vi.mock('@/router-singleton', () => ({ router: { navigate: vi.fn() } }))
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => vi.fn(),
    // 裸渲染无 RouterProvider；列表行 Link 降级为静态 <a>
    Link: ({ to, children, ...rest }: { to: string; children?: React.ReactNode }) => (
      <a href={to} {...rest}>
        {children}
      </a>
    ),
  }
})

const AGENT = {
  id: 'ag-1',
  name: 'Translator',
  visibility: 'public',
  archived: false,
  runtime_config: { provider: 'openai', model: 'gpt-4o-mini' },
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <AgentsPage />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  useWorkspaceStore.setState({ currentId: null })
})

describe('AgentsPage filter semantics (ui-sweep)', () => {
  it('renders the filter as an aria-pressed button group, not a tablist', async () => {
    server.use(
      http.get('/api/agents', () => HttpResponse.json({ agents: [AGENT] })),
      http.get('/api/working-agents', () => HttpResponse.json({ agents: [] })),
    )
    renderPage()
    const group = await screen.findByRole('group', { name: 'Agent filter' })
    expect(group).toBeInTheDocument()
    // 三档筛选都是普通按钮，用 aria-pressed 表达选中态
    const all = screen.getByRole('button', { name: 'All' })
    expect(all).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'Active' })).toHaveAttribute(
      'aria-pressed',
      'false',
    )
    expect(screen.getByRole('button', { name: 'Archived' })).toHaveAttribute(
      'aria-pressed',
      'false',
    )
    // 不再残留 tab 语义（无 tabpanel 的 role=tab 是读屏错误）
    expect(screen.queryAllByRole('tab')).toHaveLength(0)
    expect(screen.queryByRole('tablist')).toBeNull()
  })

  it('lists agents with visibility badge and archived badge', async () => {
    server.use(
      http.get('/api/agents', () =>
        HttpResponse.json({
          agents: [
            AGENT,
            { ...AGENT, id: 'ag-2', name: 'Archived One', archived: true },
          ],
        }),
      ),
      http.get('/api/working-agents', () =>
        HttpResponse.json({ agents: [{ agent_id: 'ag-1', name: 'Translator', active: 2 }] }),
      ),
    )
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('Translator')).toBeInTheDocument()
    })
    // 两行 fixture 都继承 visibility: 'public'，各自渲染一枚 Public 徽章
    expect(screen.getAllByText('Public')).toHaveLength(2)
    expect(screen.getByText('2 active')).toBeInTheDocument()
    // 默认 all 视图也显示已归档行
    expect(screen.getByText('Archived One')).toBeInTheDocument()
  })

  it('shows the empty state when no agents exist', async () => {
    server.use(
      http.get('/api/agents', () => HttpResponse.json({ agents: [] })),
      http.get('/api/working-agents', () => HttpResponse.json({ agents: [] })),
    )
    renderPage()
    expect(await screen.findByText('No agents yet')).toBeInTheDocument()
  })
})

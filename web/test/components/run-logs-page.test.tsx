import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { WorkspaceTaskRow } from '@/api/orchestration'
import { RunLogsPage } from '@/routes/_app.run-logs.index'
import { server } from '../msw'

// run-logs 页回归钉子（ui-sweep 自审）：列表渲染、状态/智能体筛选走 search patch、
// 空态组合件、加载更多。抽屉本体另有 run-logs-drawer.test.tsx 覆盖。
// 路由 hook 打桩：页面消费 Route.useSearch/useNavigate（URL 即筛选状态），
// navigate 打桩收 patch，直接断言 search 合并结果。

const { navigate, useNavigate, useSearch } = vi.hoisted(() => {
  const navigate = vi.fn()
  return {
    navigate,
    useNavigate: vi.fn(() => navigate),
    useSearch: vi.fn((): Record<string, string | undefined> => ({})),
  }
})
vi.mock('@tanstack/react-router', () => ({
  createFileRoute:
    () =>
    () => ({
      useSearch,
      useNavigate,
    }),
  // 行内"查看工作项"链接 + 抽屉复用：渲染成 <a> 即可
  Link: (props: React.ComponentProps<'a'>) => <a {...props} />,
}))

const TASK: WorkspaceTaskRow = {
  id: 't-1',
  workspace_id: 'ws-1',
  agent_id: 'a-1',
  agent_name: 'writer',
  work_item_id: 'wi-1',
  work_item_title: 'Sweep the floor',
  status: 'running',
  priority: 0,
  attempt: 1,
  max_attempts: 3,
  context: {},
  originator_source: 'assignment',
  created_at: '2026-10-12T00:00:00Z',
}
const AGENT = {
  id: 'a-1',
  workspace_id: 'ws-1',
  name: 'writer',
  description: '',
  runtime_config: {},
  max_concurrent_tasks: 1,
  visibility: 'workspace',
  archived: false,
  created_at: '2026-10-11T08:00:00Z',
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <RunLogsPage />
    </QueryClientProvider>,
  )
}

describe('run-logs page', () => {
  it('renders task rows with agent, work item and a load-more affordance', async () => {
    server.use(
      http.get('/api/agents', () => HttpResponse.json({ agents: [AGENT] })),
      http.get('/api/tasks', () =>
        HttpResponse.json({ tasks: [TASK], next_cursor: 'cursor-1' }),
      ),
    )
    renderPage()
    expect(await screen.findByText('Sweep the floor')).toBeInTheDocument()
    // 智能体名同时出现在行内与筛选项里，按角色各断一处
    expect(screen.getByRole('option', { name: 'writer' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Load more' })).toBeInTheDocument()
    // 打开转写提示走 Tooltip 触发器（原 title 属性），行本身仍是按钮
    expect(screen.getByRole('button', { name: /Sweep the floor/ })).toBeInTheDocument()
  })

  it('agent filter and status tabs patch the search state', async () => {
    server.use(
      http.get('/api/agents', () => HttpResponse.json({ agents: [AGENT] })),
      http.get('/api/tasks', () => HttpResponse.json({ tasks: [], next_cursor: null })),
    )
    const user = userEvent.setup()
    renderPage()
    // 等智能体选项就绪（select 的 option 异步渲染）
    expect(await screen.findByText('writer')).toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText('Filter by agent'), 'a-1')
    expect(navigate).toHaveBeenCalledTimes(1)
    const agentPatch = navigate.mock.calls[0]![0] as {
      search: (prev: unknown) => unknown
      replace: boolean
    }
    expect(agentPatch.replace).toBe(true)
    expect(agentPatch.search({})).toEqual({ agent: 'a-1' })

    await user.click(screen.getByRole('tab', { name: 'Failed' }))
    const statusPatch = navigate.mock.calls[1]![0] as {
      search: (prev: unknown) => unknown
    }
    expect(statusPatch.search({})).toEqual({ status: 'failed' })

    // 回到默认 tab：active 落为 undefined（URL 保持干净）
    await user.click(screen.getByRole('tab', { name: 'Active' }))
    const activePatch = navigate.mock.calls[2]![0] as {
      search: (prev: unknown) => unknown
    }
    expect(activePatch.search({})).toEqual({ status: undefined })
  })

  it('empty default tab: shows the onboarding empty state, not the filtered one', async () => {
    server.use(
      http.get('/api/agents', () => HttpResponse.json({ agents: [] })),
      http.get('/api/tasks', () => HttpResponse.json({ tasks: [], next_cursor: null })),
    )
    renderPage()
    expect(await screen.findByText('No active tasks right now')).toBeInTheDocument()
    expect(
      screen.getByText(
        'Assign an agent, @ it in a comment, or set up a wakeup rule—runs will appear here.',
      ),
    ).toBeInTheDocument()
  })
})

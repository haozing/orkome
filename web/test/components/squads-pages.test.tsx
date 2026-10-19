import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { ConfirmProvider } from '@ploykit/ui'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { SquadDetailPage } from '@/routes/_app.squads.$squadId'
import { SquadsPage } from '@/routes/_app.squads.index'
import { server } from '../msw'

// 小队域 ui-sweep 钉子：删除/移除走 kit useConfirm（后果说明只在弹窗 description
// 说一次，页内两段式与常驻 hint 行不回流）；弹窗=本地 ui/dialog+Button；状态=
// StatusBadge 文字单轨（色点+原生 title 不回流）；角色 chip=Badge secondary+Tooltip；
// 成员区三态=Skeleton 行占位/PageEmpty。setup.ts 固定 i18n=en，断言用英文文案
// （含 squads.* 与 squadsList.* 的 patch 后新文案，i18n 主线落地前本文件红）。

const st = vi.hoisted(() => ({
  params: { squadId: 'sq-1' },
  navigate: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (options: Record<string, unknown>) => ({
      options,
      useParams: () => st.params,
    }),
    useNavigate: () => st.navigate,
    // 渲染成 <a>；params 是路由参数对象，剥掉避免 React 未知属性告警
    Link: ({ params, ...props }: { params?: unknown } & React.ComponentProps<'a'>) => {
      void params
      return <a {...props} />
    },
  }
})

const AGENT = {
  id: 'agent-1',
  workspace_id: 'ws-1',
  name: 'alpha',
  description: '',
  runtime_config: {},
  max_concurrent_tasks: 1,
  visibility: 'workspace',
  archived: false,
  created_at: '2026-10-11T08:00:00Z',
}
const AGENT_BETA = {
  ...AGENT,
  id: 'agent-2',
  name: 'beta',
}
const SQUAD = {
  id: 'sq-1',
  name: 'Morning squad',
  instructions: '',
  leader_agent_id: '',
  members: ['agent-1'],
}
const MEMBER = { agent_id: 'agent-1', name: 'alpha', role: 'member', archived: false }
const STATUS_IDLE = {
  agent_id: 'agent-1',
  name: 'alpha',
  role: 'member',
  status: 'idle',
  active_tasks: null,
}

function defaultHandlers() {
  return [
    http.get('/api/agents', () => HttpResponse.json({ agents: [AGENT, AGENT_BETA] })),
    http.get('/api/squads', () => HttpResponse.json({ squads: [SQUAD] })),
    http.get('/api/squads/sq-1', () => HttpResponse.json(SQUAD)),
    http.get('/api/squads/sq-1/members', () => HttpResponse.json({ members: [MEMBER] })),
    http.get('/api/squads/sq-1/members/status', () =>
      HttpResponse.json({ members: [STATUS_IDLE] }),
    ),
  ]
}

function renderWithProviders(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <ConfirmProvider>{ui}</ConfirmProvider>
    </QueryClientProvider>,
  )
}

function renderDetailRaw() {
  return renderWithProviders(<SquadDetailPage />)
}

// handlers 顺序敏感：msw 3 同路径 handler 先注册者先匹配（首响应即胜），
// 同路径 override 必须排在 defaultHandlers 之前，否则被默认桩遮蔽。
function renderDetail(handlers: Parameters<typeof server.use> = defaultHandlers()) {
  server.use(...handlers)
  return renderDetailRaw()
}

beforeEach(() => {
  st.navigate.mockClear()
})

describe('squad detail (status single-track)', () => {
  it('member row shows StatusBadge text and role Badge; label copy patched', async () => {
    renderDetail()
    expect(await screen.findByText('Morning squad')).toBeInTheDocument()
    // 状态=文字徽章（原色点+原生 title 不回流）
    expect(await screen.findByText('Idle')).toBeInTheDocument()
    // 角色 chip=Badge secondary（原手写 pill）
    expect(screen.getByRole('button', { name: 'Role' })).toHaveTextContent('member')
    // 表单行走 kit FormField（label 文案=patch 后）
    expect(screen.getByText('Leader (dispatches work)')).toBeInTheDocument()
    expect(screen.getByLabelText('Leader (dispatches work)')).toBeInstanceOf(
      HTMLSelectElement,
    )
  })

  it('role badge click opens inline editor and PATCHes role', async () => {
    const patches: unknown[] = []
    server.use(
      http.patch('/api/squads/sq-1/members/agent-1', async ({ request }) => {
        patches.push(await request.json())
        return HttpResponse.json({ result: 'ok' })
      }),
    )
    const user = userEvent.setup()
    renderDetail()
    await user.click(await screen.findByRole('button', { name: 'Role' }))
    const input = screen.getByLabelText('Role')
    await user.clear(input)
    await user.type(input, 'scout{Enter}')
    await waitFor(() => expect(patches).toEqual([{ role: 'scout' }]))
  })
})

describe('squad delete (useConfirm single form)', () => {
  it('cancel in confirm dialog fires no DELETE; consequence text only in dialog', async () => {
    const deletes: string[] = []
    server.use(
      http.delete('/api/squads/sq-1', ({ request }) => {
        deletes.push(request.url)
        return HttpResponse.json({ result: 'ok' })
      }),
    )
    const user = userEvent.setup()
    renderDetail()
    await user.click(await screen.findByRole('button', { name: 'Delete squad' }))
    const dialog = await screen.findByRole('alertdialog')
    // 后果说明只在弹窗里说一次（页内常驻 hint 行已删）
    expect(within(dialog).getAllByText(/no active tasks/)).toHaveLength(1)
    expect(screen.getAllByText(/no active tasks/)).toHaveLength(1)
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(deletes).toHaveLength(0)
  })

  it('confirm deletes the squad, toasts and navigates back to list', async () => {
    const deletes: string[] = []
    server.use(
      http.delete('/api/squads/sq-1', ({ request }) => {
        deletes.push(request.url)
        return HttpResponse.json({ result: 'ok' })
      }),
    )
    const user = userEvent.setup()
    renderDetail()
    await user.click(await screen.findByRole('button', { name: 'Delete squad' }))
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Confirm delete' }))
    await waitFor(() => expect(deletes).toHaveLength(1))
    expect(st.navigate).toHaveBeenCalledWith({ to: '/squads' })
  })
})

describe('member remove (useConfirm single form)', () => {
  it('inline two-step is gone; dialog states the target then DELETE fires', async () => {
    const deletes: string[] = []
    server.use(
      http.delete('/api/squads/sq-1/members/agent-1', ({ request }) => {
        deletes.push(request.url)
        return HttpResponse.json({ result: 'ok' })
      }),
    )
    const user = userEvent.setup()
    renderDetail()
    await user.click(await screen.findByRole('button', { name: 'Remove' }))
    // 行内不再出现「Confirm remove」文字链，确认只发生在弹窗
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/This will remove "alpha" from the squad/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(deletes).toHaveLength(1))
  })
})

describe('members area three states', () => {
  it('pending: Skeleton row placeholders, no handwritten loading copy', async () => {
    renderDetail([
      http.get('/api/squads/sq-1/members', () => new Promise(() => {})),
      ...defaultHandlers(),
    ])
    expect(await screen.findByText('Morning squad')).toBeInTheDocument()
    const skeletons = document.querySelectorAll('[data-slot="skeleton"]')
    expect(skeletons.length).toBeGreaterThanOrEqual(3)
    expect(screen.queryByText('Loading members…')).toBeNull()
  })

  it('empty: PageEmpty with patched short copy (no flow teaching)', async () => {
    renderDetail([
      http.get('/api/squads/sq-1/members', () => HttpResponse.json({ members: [] })),
      ...defaultHandlers(),
    ])
    expect(await screen.findByText('No members yet')).toBeInTheDocument()
    expect(screen.queryByText(/Add agents and let the leader/)).toBeNull()
  })
})

describe('add member dialog (local ui/dialog)', () => {
  it('posts {agent_id, role} contract and closes', async () => {
    const bodies: unknown[] = []
    server.use(
      http.post('/api/squads/sq-1/members', async ({ request }) => {
        bodies.push(await request.json())
        return HttpResponse.json({ result: 'ok' })
      }),
    )
    const user = userEvent.setup()
    renderDetail()
    await user.click(await screen.findByRole('button', { name: 'Add member' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('Agent')).toBeInstanceOf(HTMLSelectElement)
    await user.selectOptions(within(dialog).getByLabelText('Agent'), 'agent-2')
    await user.type(within(dialog).getByLabelText('Role (optional)'), 'scout')
    await user.click(within(dialog).getByRole('button', { name: 'Add' }))
    await waitFor(() =>
      expect(bodies).toEqual([{ agent_id: 'agent-2', role: 'scout' }]),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('no candidates: description carries the hint once', async () => {
    // 只有 alpha（已在队）可选 → 候选为空，hint 走 FormField description 单轨
    // （同路径 override 排在 defaults 前，msw 3 首注册先匹配）
    server.use(
      http.get('/api/agents', () => HttpResponse.json({ agents: [AGENT] })),
      ...defaultHandlers(),
    )
    const user = userEvent.setup()
    renderDetailRaw()
    await user.click(await screen.findByRole('button', { name: 'Add member' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getAllByText('No agents available to add')).toHaveLength(1)
    expect(within(dialog).getByRole('button', { name: 'Add' })).toBeDisabled()
  })
})

describe('squads index (create dialog)', () => {
  it('createHint removed; create posts and navigates to detail', async () => {
    const bodies: unknown[] = []
    server.use(
      ...defaultHandlers(),
      http.post('/api/squads', async ({ request }) => {
        bodies.push(await request.json())
        return HttpResponse.json(
          { ...SQUAD, id: 'sq-9', name: 'Evening squad', members: [] },
          { status: 201 },
        )
      }),
    )
    const user = userEvent.setup()
    renderWithProviders(<SquadsPage />)
    expect(await screen.findByText('Morning squad')).toBeInTheDocument()
    // 失实文案整段删（onSuccess 已自动跳详情）
    expect(
      screen.queryByText('After creating, add the leader and members on the detail page.'),
    ).toBeNull()
    await user.click(screen.getByRole('button', { name: 'New squad' }))
    const dialog = screen.getByRole('dialog')
    await user.type(within(dialog).getByLabelText('Name (required)'), 'Evening squad')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(bodies).toEqual([{ name: 'Evening squad', instructions: '' }]))
    expect(st.navigate).toHaveBeenCalledWith({
      to: '/squads/$squadId',
      params: { squadId: 'sq-9' },
    })
  })

  it('empty list uses patched emptyDesc', async () => {
    // 同路径 override 排在 defaults 前（msw 3 首注册先匹配）
    server.use(
      http.get('/api/squads', () => HttpResponse.json({ squads: [] })),
      ...defaultHandlers(),
    )
    renderWithProviders(<SquadsPage />)
    expect(await screen.findByText('No squads yet')).toBeInTheDocument()
    expect(
      screen.getByText('Group agents into a squad, led by a leader who dispatches the work.'),
    ).toBeInTheDocument()
  })
})

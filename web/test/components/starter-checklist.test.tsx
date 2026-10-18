import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { StarterChecklist } from '@/components/work-item/starter-checklist'
import { useWorkspaceStore } from '@/stores/workspace'
import { server } from '../msw'

// 起步清单（next-wave-plan W2）：空板三卡——单字段建工单直达详情；
// 智能体卡读 setup-status ready 探针出完成态；队友卡按成员数出完成态。
// setup.ts 固定 i18n=en，断言用英文文案。

const navigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => navigate, Link: (p: { to: string; children?: React.ReactNode }) => <a href={p.to}>{p.children}</a> }
})

function renderChecklist() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <StarterChecklist />
    </QueryClientProvider>,
  )
}

function defaultHandlers(over: { agents?: unknown[]; members?: unknown[] } = {}) {
  return [
    http.get('/api/setup-status', () =>
      HttpResponse.json({ credential_count: 0, agents: over.agents ?? [] }),
    ),
    http.get('/api/workspaces/ws-1/members', () =>
      HttpResponse.json(over.members ?? [{ user_id: 'u-1' }]),
    ),
  ]
}

beforeEach(() => {
  useWorkspaceStore.setState({ currentId: 'ws-1' })
  navigate.mockReset()
  server.use(...defaultHandlers())
})
afterEach(() => {
  useWorkspaceStore.setState({ currentId: null })
  server.resetHandlers()
})

describe('starter checklist (work-items empty state)', () => {
  it('renders the three cards with links', async () => {
    renderChecklist()
    expect(await screen.findByText('Create your first work item')).toBeInTheDocument()
    expect(screen.getByText('Set up an agent')).toBeInTheDocument()
    expect(screen.getByText('Invite teammates')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Open assistant access/ })).toHaveAttribute(
      'href',
      '/access',
    )
  })

  it('quick create posts {title} and navigates to the new item', async () => {
    let body: { title?: string } | undefined
    server.use(
      http.post('/api/work-items/quick-create', async ({ request }) => {
        body = (await request.json()) as { title: string }
        return HttpResponse.json({ id: 'wi-new', workspace_id: 'ws-1' }, { status: 201 })
      }),
    )
    renderChecklist()
    await screen.findByText('Create your first work item')
    await userEvent.type(screen.getByRole('textbox'), 'First task')
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(body).toEqual({ title: 'First task' }))
    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith({ to: '/work-items/$id', params: { id: 'wi-new' } }),
    )
  })

  it('a ready agent marks card 2 done; a lone member leaves card 3 open', async () => {
    server.use(
      ...defaultHandlers({
        agents: [{ id: 'a-1', name: 'ready-one', archived: false, ready: true }],
      }),
    )
    renderChecklist()
    expect(await screen.findByText('An agent is ready')).toBeInTheDocument()
    expect(screen.queryByText('Open assistant access')).toBeNull()
    expect(screen.getAllByLabelText('Done')).toHaveLength(1)
  })

  it('an archived-but-configured agent does not count as ready', async () => {
    server.use(
      ...defaultHandlers({
        agents: [{ id: 'a-2', name: 'archived-one', archived: true, ready: true }],
      }),
    )
    renderChecklist()
    expect(await screen.findByRole('link', { name: /Open assistant access/ })).toBeInTheDocument()
  })
})

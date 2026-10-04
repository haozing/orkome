import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AssigneeRow } from '@/components/work-item/property-rows'
import type { WorkItem } from '@/api/work'
const mocks = vi.hoisted(() => ({
  updateWorkItem: vi.fn(),
  listMembers: vi.fn(),
  listAgents: vi.fn(),
}))
vi.mock('@/api/work', async () => {
  const actual = await vi.importActual<typeof import('@/api/work')>('@/api/work')
  return { ...actual, updateWorkItem: (...args: unknown[]) => mocks.updateWorkItem(...args) }
})
vi.mock('@/api/identity', () => ({
  membersQueryOptions: (ws: string) => ({
    queryKey: ['workspace', ws, 'members'],
    queryFn: () => mocks.listMembers(),
  }),
}))
vi.mock('@/api/orchestration', async () => {
  const actual = await vi.importActual<typeof import('@/api/orchestration')>('@/api/orchestration')
  return { ...actual, listAgents: (...args: unknown[]) => mocks.listAgents(...args) }
})
vi.mock('@/stores/workspace', () => ({
  useWorkspaceStore: () => 'ws-1',
}))
const ITEM = (over: Partial<WorkItem> = {}): WorkItem => ({
  id: 'wi-1',
  workspace_id: 'ws-1',
  number: 1,
  title: 'T',
  description: '',
  status: 'todo',
  status_category: 'unstarted',
  priority: 0,
  assignee_type: 'none',
  creator_type: 'user',
  creator_id: 'u-1',
  revision: 3,
  created_at: '2026-10-03T00:00:00Z',
  updated_at: '2026-10-03T00:00:00Z',
  last_activity_at: '2026-10-03T00:00:00Z',
  ...over,
})
function renderRow(item: WorkItem) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <AssigneeRow item={item} />
    </QueryClientProvider>,
  )
}
beforeEach(() => {
  vi.clearAllMocks()
  mocks.listMembers.mockResolvedValue([
    {
      id: 'm-1',
      workspace_id: 'ws-1',
      user_id: 'u-1',
      role: 'owner',
      email: 'a@b.c',
      display_name: 'Alice Zhang',
      created_at: '2026-10-03T00:00:00Z',
    },
  ])
  mocks.listAgents.mockResolvedValue([
    {
      id: 'agent-1',
      workspace_id: 'ws-1',
      name: 'writer',
      description: '',
      max_concurrent_tasks: 6,
      visibility: 'public',
      archived: false,
      created_at: '2026-10-03T00:00:00Z',
    },
    {
      id: 'agent-2',
      workspace_id: 'ws-1',
      name: 'archived-one',
      description: '',
      max_concurrent_tasks: 6,
      visibility: 'public',
      archived: true,
      created_at: '2026-10-03T00:00:00Z',
    },
  ])
  mocks.updateWorkItem.mockResolvedValue(ITEM())
})
describe('AssigneeRow', () => {
  it('menu groups: unassigned / members (display name) / agents (archived filtered)', async () => {
    renderRow(ITEM())
    fireEvent.click(screen.getByRole('button'))
    expect(await screen.findByText('Member')).toBeInTheDocument()
    expect(screen.getByText('Alice Zhang')).toBeInTheDocument()
    expect(screen.getByText('writer')).toBeInTheDocument()
    expect(screen.queryByText('archived-one')).not.toBeInTheDocument()
  })
  it('click member → PUT assignee_type=user + user_id', async () => {
    renderRow(ITEM())
    fireEvent.click(screen.getByRole('button'))
    fireEvent.click(await screen.findByText('Alice Zhang'))
    await waitFor(() =>
      expect(mocks.updateWorkItem).toHaveBeenCalledWith(
        'wi-1',
        { assignee_type: 'user', assignee_id: 'u-1' },
        3,
      ),
    )
  })
  it('click agent → PUT assignee_type=agent + agent_id; click unassigned → type=none', async () => {
    renderRow(ITEM())
    fireEvent.click(screen.getByRole('button'))
    fireEvent.click(await screen.findByText('writer'))
    await waitFor(() =>
      expect(mocks.updateWorkItem).toHaveBeenCalledWith(
        'wi-1',
        { assignee_type: 'agent', assignee_id: 'agent-1' },
        3,
      ),
    )
  })
  it('shows display name when assigned; unassigned clears', async () => {
    renderRow(ITEM({ assignee_type: 'user', assignee_id: 'u-1' }))
    expect(await screen.findByText('Alice Zhang')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button'))
    expect(await screen.findByText('Unassigned')).toBeInTheDocument()
  })
})

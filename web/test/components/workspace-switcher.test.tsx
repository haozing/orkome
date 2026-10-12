import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { WorkspaceSwitcher } from '@/components/workspace-switcher'
import { useWorkspaceStore } from '@/stores/workspace'
import { server } from '../msw'
const WS_A = {
  id: 'ws-1',
  name: 'Team A',
  slug: 'team-a',
  plan_code: 'free',
  role: 'owner',
  created_at: '2026-10-02T00:00:00Z',
}
const WS_B = {
  id: 'ws-2',
  name: 'Team B',
  slug: 'team-b',
  plan_code: 'free',
  role: 'member',
  created_at: '2026-10-02T00:00:00Z',
}
function renderSwitcher() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <WorkspaceSwitcher />
    </QueryClientProvider>,
  )
}
describe('WorkspaceSwitcher', () => {
  it('opening the menu lists workspaces without crashing (Menu.GroupLabel must live inside Menu.Group)', async () => {
    // Regression (batch1 root cause B / FP-02 P0-1): before the fix the
    // popup threw synchronously on render ("MenuGroupContext is missing"),
    // crashing any page that opened the switcher.
    server.use(
      http.get('/auth/me', () =>
        HttpResponse.json({ id: 'u-1', email: 'a@b.c', display_name: 'A' }),
      ),
      http.get('/api/workspaces', () => HttpResponse.json([WS_A, WS_B])),
    )
    useWorkspaceStore.setState({ currentId: 'ws-1' })
    renderSwitcher()
    expect(await screen.findByText('Team A')).toBeInTheDocument() // trigger label
    fireEvent.click(screen.getByRole('button'))
    // "Team B" exists only as a menu item, so finding it proves the popup
    // rendered instead of throwing.
    expect(await screen.findByText('Team B')).toBeInTheDocument()
    expect(screen.getByText('Workspaces')).toBeInTheDocument() // group label
  })
  it('shows the fallback label when the me query has no workspaces yet', async () => {
    server.use(
      http.get('/auth/me', () =>
        HttpResponse.json({ id: 'u-1', email: 'a@b.c', display_name: 'A' }),
      ),
      http.get('/api/workspaces', () => HttpResponse.json([])),
    )
    renderSwitcher()
    expect(await screen.findByText('Select workspace')).toBeInTheDocument()
  })
})

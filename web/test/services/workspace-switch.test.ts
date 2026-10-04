import { QueryClient } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { switchWorkspace } from '@/services/workspace-switch'
import { useWorkspaceStore } from '@/stores/workspace'
const wsMock = vi.hoisted(() => ({ reconnect: vi.fn() }))
vi.mock('@/realtime/client', () => ({ wsClient: wsMock }))
beforeEach(() => {
  wsMock.reconnect.mockClear()
  useWorkspaceStore.setState({ currentId: 'ws-1' })
})
describe('switchWorkspace (the only switch entry)', () => {
  it('updates store, clears workspace caches, keeps me/config, triggers WS reconnect', () => {
    const qc = new QueryClient()
    qc.setQueryData(['me'], { user: {} })
    qc.setQueryData(['config'], { version: 'v' })
    qc.setQueryData(['work-items'], { items: [] })
    qc.setQueryData(['inbox', 'unread-count'], 3)
    switchWorkspace(qc, 'ws-2')
    expect(useWorkspaceStore.getState().currentId).toBe('ws-2')
    expect(qc.getQueryData(['me'])).toEqual({ user: {} })
    expect(qc.getQueryData(['config'])).toEqual({ version: 'v' })
    expect(qc.getQueryData(['work-items'])).toBeUndefined()
    expect(qc.getQueryData(['inbox', 'unread-count'])).toBeUndefined()
    expect(wsMock.reconnect).toHaveBeenCalledTimes(1)
  })
  it('switching to current workspace is a no-op', () => {
    const qc = new QueryClient()
    qc.setQueryData(['work-items'], { items: [] })
    switchWorkspace(qc, 'ws-1')
    expect(qc.getQueryData(['work-items'])).toEqual({ items: [] })
    expect(wsMock.reconnect).not.toHaveBeenCalled()
  })
})
describe('queryKey prefix convention', () => {
  it('meQueryOptions matches the preserved list', async () => {
    const { meQueryOptions } = await import('@/api/identity')
    expect(meQueryOptions.queryKey[0]).toBe('me')
  })
})

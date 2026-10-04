import { http, HttpResponse } from 'msw'
import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { createWorkspace, joinByShareCode, meQueryOptions } from '@/api/identity'
import { server } from '../msw'
const WS: import('@/api/identity').Workspace = {
  id: 'ws-1',
  name: 'Team A',
  slug: 'team-a',
  work_item_counter: 3,
  created_at: '2026-10-02T00:00:00Z',
}
describe('identity API (workspace)', () => {
  it('createWorkspace: POST /api/workspaces with name+slug, returns Workspace', async () => {
    server.use(
      http.post('/api/workspaces', async ({ request }) => {
        expect(await request.json()).toEqual({ name: 'Team A', slug: 'team-a' })
        return HttpResponse.json(WS, { status: 201 })
      }),
    )
    await expect(createWorkspace('Team A', 'team-a')).resolves.toEqual(WS)
  })
  it('joinByShareCode: POST /api/share-links/join with code, returns Member', async () => {
    server.use(
      http.post('/api/share-links/join', async ({ request }) => {
        expect(await request.json()).toEqual({ code: 'share-code-1' })
        return HttpResponse.json({
          id: 'm-1',
          workspace_id: 'ws-1',
          user_id: 'u-1',
          role: 'member',
          created_at: '2026-10-02T00:00:00Z',
        })
      }),
    )
    const member = await joinByShareCode('share-code-1')
    expect(member.workspace_id).toBe('ws-1')
    expect(member.role).toBe('member')
  })
  it('meQueryOptions.queryFn: GET /api/me returns user+workspaces', async () => {
    server.use(
      http.get('/api/me', () =>
        HttpResponse.json({
          user: { id: 'u-1', email: 'a@b.c', display_name: 'A' },
          workspaces: [WS],
        }),
      ),
    )
    const me = await meQueryOptions.queryFn!({
      client: new QueryClient(),
      queryKey: meQueryOptions.queryKey,
      signal: new AbortController().signal,
      meta: undefined,
    })
    expect(me.user.email).toBe('a@b.c')
    expect(me.workspaces).toHaveLength(1)
  })
})

import { http, HttpResponse } from 'msw'
import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { createWorkspace, joinByShareCode, meQueryOptions } from '@/api/identity'
import { server } from '../msw'
const WS: import('@/api/identity').Workspace = {
  id: 'ws-1',
  name: 'Team A',
  slug: 'team-a',
  plan_code: 'free',
  role: 'owner',
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
  it('meQueryOptions.queryFn: GET /auth/me + GET /api/workspaces (split on the ploykit rebase)', async () => {
    const calls: string[] = []
    server.use(
      http.get('/auth/me', () => {
        calls.push('GET /auth/me')
        return HttpResponse.json({ id: 'u-1', email: 'a@b.c', display_name: 'A' })
      }),
      http.get('/api/workspaces', () => {
        calls.push('GET /api/workspaces')
        return HttpResponse.json([WS])
      }),
    )
    const queryFn = meQueryOptions.queryFn
    if (typeof queryFn !== 'function') throw new Error('meQueryOptions.queryFn missing')
    const me = await queryFn({
      client: new QueryClient(),
      queryKey: meQueryOptions.queryKey,
      signal: new AbortController().signal,
      meta: undefined,
    })
    expect(me.user.email).toBe('a@b.c')
    expect(me.workspaces).toHaveLength(1)
    expect(calls).toEqual(['GET /auth/me', 'GET /api/workspaces'])
  })
})

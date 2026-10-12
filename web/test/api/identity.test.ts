import { http, HttpResponse } from 'msw'
import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { createToken, createWorkspace, joinByShareCode, listMembers, meQueryOptions } from '@/api/identity'
import { server } from '../msw'
const WS: import('@/api/identity').Workspace = {
  id: 'ws-1',
  name: 'Team A',
  slug: 'team-a',
  plan_code: 'free',
  role: 'owner',
  created_at: '2026-10-02T00:00:00Z',
}
// Real GET /api/workspaces/{id}/members capture: ploykit app.Page envelope,
// rows have user_id/email/display_name but NO id/workspace_id (audit FP-02 §2.2).
const MEMBER_PAGE: import('@/api/identity').Member[] = [
  {
    user_id: 'u-1',
    role: 'owner',
    email: 'a@b.c',
    display_name: 'Alice Zhang',
    created_at: '2026-10-02T00:00:00Z',
  },
  {
    user_id: 'u-2',
    role: 'member',
    email: 'bob@c.d',
    display_name: 'Bob',
    created_at: '2026-10-03T00:00:00Z',
  },
]
// Real POST /api/share-links/redeem capture: returns a bare Workspace —
// no workspace_id, no role (audit FP-02 §2.3 / FP-17 T23).
const REDEEMED_WS = {
  id: 'ws-1',
  slug: 'team-a',
  name: 'Team A',
  plan_code: 'free',
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
  it('joinByShareCode: POST /api/share-links/redeem with code, returns Workspace', async () => {
    // Regression (batch1 root cause C / FP-02 P0-2+P0-3): the old client hit
    // the nonexistent /api/share-links/join and read member.workspace_id
    // off a Workspace response (undefined).
    server.use(
      http.post('/api/share-links/redeem', async ({ request }) => {
        expect(await request.json()).toEqual({ code: 'share-code-1' })
        return HttpResponse.json(REDEEMED_WS)
      }),
    )
    const ws = await joinByShareCode('share-code-1')
    expect(ws.id).toBe('ws-1')
    expect(ws.slug).toBe('team-a')
  })
  it('listMembers: unwraps the {items,total} page envelope, returns Member[]', async () => {
    // Regression (batch1 root cause A): the old client treated the envelope
    // as a bare array, so consumers calling .find/.map on it crashed or
    // silently rendered nothing.
    server.use(
      http.get('/api/workspaces/ws-1/members', () =>
        HttpResponse.json({ items: MEMBER_PAGE, total: MEMBER_PAGE.length }),
      ),
    )
    const members = await listMembers('ws-1')
    expect(members).toEqual(MEMBER_PAGE)
    expect(members[0]).toMatchObject({ user_id: 'u-1', display_name: 'Alice Zhang' })
  })
  it('listMembers: {items:null} page resolves to []', async () => {
    server.use(
      http.get('/api/workspaces/ws-1/members', () => HttpResponse.json({ items: null, total: 0 })),
    )
    await expect(listMembers('ws-1')).resolves.toEqual([])
  })
  it('createToken: sends ttl_hours (not ttl_seconds) and unwraps the nested {pat,token} response', async () => {
    // Regression (batch1 root cause E / FP-09 P0-1): ttl_seconds was rejected
    // with 400 (DisallowUnknownFields); the {pat:{...},token} response was
    // read flat, rendering an empty-name success banner.
    server.use(
      http.post('/api/tokens', async ({ request }) => {
        expect(await request.json()).toEqual({ name: 'fp09-pat', ttl_hours: 2 })
        return HttpResponse.json({
          pat: {
            id: 'pat-1',
            name: 'fp09-pat',
            prefix: 'ork_',
            expires_at: null,
            created_at: '2026-10-02T00:00:00Z',
          },
          token: 'ork_secret-value',
        })
      }),
    )
    const secret = await createToken('fp09-pat', 7200)
    expect(secret.token).toBe('ork_secret-value')
    expect(secret.id).toBe('pat-1')
    expect(secret.name).toBe('fp09-pat')
    expect(secret.prefix).toBe('ork_')
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

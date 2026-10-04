import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { api, ApiError, __resetApiForTests, setWorkspaceIdProvider } from '@/lib/api'
import { useWorkspaceStore } from '@/stores/workspace'
import { server } from '../msw'
describe('api client', () => {
  beforeEach(() => {
    __resetApiForTests()
    useWorkspaceStore.setState({ currentId: null })
    setWorkspaceIdProvider(() => useWorkspaceStore.getState().currentId)
  })
  afterEach(() => {
    useWorkspaceStore.setState({ currentId: null })
  })
  it('GET does not trigger CSRF bootstrap, no CSRF header', async () => {
    let captured: Request | undefined
    server.use(
      http.get('/api/things', ({ request }) => {
        captured = request.clone()
        return HttpResponse.json({ ok: true })
      }),
    )
    await expect(api.get('/api/things')).resolves.toEqual({ ok: true })
    expect(captured?.headers.get('x-csrf-token')).toBeNull()
  })
  it('POST auto bootstrap: GET /config for masked token first, then X-CSRF-Token', async () => {
    const calls: string[] = []
    server.use(
      http.get('/config', () => {
        calls.push('GET /config')
        return HttpResponse.json({ version: 'test', csrf_token: 'tok-1' })
      }),
      http.post('/api/things', ({ request }) => {
        calls.push('POST /api/things')
        expect(request.headers.get('x-csrf-token')).toBe('tok-1')
        return HttpResponse.json({ created: true }, { status: 201 })
      }),
    )
    await expect(api.post('/api/things', { a: 1 })).resolves.toEqual({ created: true })
    expect(calls).toEqual(['GET /config', 'POST /api/things'])
  })
  it('CSRF bootstrap happens once (shared across concurrent)', async () => {
    let configCalls = 0
    server.use(
      http.get('/config', () => {
        configCalls += 1
        return HttpResponse.json({ version: 'test', csrf_token: 'tok-1' })
      }),
      http.post('/api/a', () => HttpResponse.json({})),
      http.post('/api/b', () => HttpResponse.json({})),
    )
    await Promise.all([api.post('/api/a'), api.post('/api/b')])
    expect(configCalls).toBe(1)
  })
  it('login response with csrf_token is captured (later requests use new token)', async () => {
    server.use(
      http.post('/auth/verify-code', () =>
        HttpResponse.json({ user: { id: 'u1' }, csrf_token: 'tok-rotated' }),
      ),
      http.post('/api/after-login', ({ request }) => {
        expect(request.headers.get('x-csrf-token')).toBe('tok-rotated')
        return HttpResponse.json({})
      }),
    )
    await api.post('/auth/verify-code', { email: 'a@b.c', code: '123456' })
    await api.post('/api/after-login')
  })
  it('unified error envelope → ApiError (status/code/message/details)', async () => {
    server.use(
      http.post('/api/things', () =>
        HttpResponse.json(
          {
            error: {
              code: 'E_REVISION_CONFLICT',
              message: 'revision conflict',
              details: { expected: 3 },
            },
          },
          { status: 409 },
        ),
      ),
    )
    const err = await api.post('/api/things').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({
      status: 409,
      code: 'E_REVISION_CONFLICT',
      message: 'revision conflict',
      details: { expected: 3 },
    })
  })
  it('204 no content → undefined', async () => {
    server.use(http.delete('/api/things/1', () => new HttpResponse(null, { status: 204 })))
    await expect(api.delete('/api/things/1')).resolves.toBeUndefined()
  })
  it('injects X-Workspace-Id into /api requests after workspace set', async () => {
    useWorkspaceStore.setState({ currentId: 'ws-123' })
    server.use(
      http.get('/api/work-items', ({ request }) => {
        expect(request.headers.get('x-workspace-id')).toBe('ws-123')
        return HttpResponse.json({ items: [] })
      }),
    )
    await expect(api.get('/api/work-items')).resolves.toEqual({ items: [] })
  })
  it('non-/api paths get no workspace header (root endpoints lack this contract)', async () => {
    useWorkspaceStore.setState({ currentId: 'ws-123' })
    server.use(
      http.get('/config', ({ request }) => {
        expect(request.headers.get('x-workspace-id')).toBeNull()
        return HttpResponse.json({ version: 'x', csrf_token: 't' })
      }),
    )
    await expect(api.get('/config')).resolves.toBeTruthy()
  })
})

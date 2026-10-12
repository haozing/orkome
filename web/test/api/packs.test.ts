import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import {
  createWorkspaceMcp,
  deleteWorkspaceMcp,
  listPackEntries,
  listWorkspaceMcp,
  updateWorkspaceMcp,
} from '@/api/packs'
import { server } from '../msw'

const ENTRY = (over: Partial<import('@/api/packs').PackUIEntry> = {}) => ({
  id: 'pe-1',
  workspace_id: 'ws-1',
  pack: 'agentblog',
  label: '内容工厂',
  url_template: 'https://agentblog.internal/console?ws={workspaceId}&t={uiToken}',
  installed_at: '2026-10-11T08:00:00Z',
  ...over,
})

const SERVER = (over: Partial<import('@/api/packs').WorkspaceMCPServer> = {}) => ({
  id: 'srv-1',
  workspace_id: 'ws-1',
  name: 'web-search',
  transport: 'http',
  url: 'https://mcp.example.com/mcp',
  has_credentials: false,
  created_at: '2026-10-11T08:00:00Z',
  ...over,
})

describe('packs API: GET /api/ui-entries', () => {
  it('unwraps { items } and falls back to [] on null', async () => {
    server.use(
      http.get('/api/ui-entries', () => HttpResponse.json({ items: [ENTRY()] })),
    )
    await expect(listPackEntries()).resolves.toHaveLength(1)
    server.use(http.get('/api/ui-entries', () => HttpResponse.json({ items: null })))
    await expect(listPackEntries()).resolves.toEqual([])
  })
})

describe('packs API: workspace MCP registry', () => {
  it('listWorkspaceMcp targets /api/workspaces/{id}/mcp-servers and unwraps { servers }', async () => {
    server.use(
      http.get('/api/workspaces/ws-1/mcp-servers', () =>
        HttpResponse.json({ servers: [SERVER()] }),
      ),
    )
    const list = await listWorkspaceMcp('ws-1')
    expect(list).toHaveLength(1)
    expect(list[0]!.name).toBe('web-search')
    // workspace id must be in the URL path (memberPathMW reads PathValue), not a header
    server.use(
      http.get('/api/workspaces/ws-1/mcp-servers', () => HttpResponse.json({ servers: null })),
    )
    await expect(listWorkspaceMcp('ws-1')).resolves.toEqual([])
  })

  it('createWorkspaceMcp POSTs wire shape { name, transport, url, credentials }', async () => {
    server.use(
      http.post('/api/workspaces/ws-1/mcp-servers', async ({ request }) => {
        expect(await request.json()).toEqual({
          name: 'web-search',
          transport: 'http',
          url: 'https://mcp.example.com/mcp',
          credentials: { headers: { Authorization: 'Bearer sk-1' } },
        })
        return HttpResponse.json(SERVER({ has_credentials: true }), { status: 201 })
      }),
    )
    const created = await createWorkspaceMcp('ws-1', {
      name: 'web-search',
      transport: 'http',
      url: 'https://mcp.example.com/mcp',
      credentials: { headers: { Authorization: 'Bearer sk-1' } },
    })
    expect(created.has_credentials).toBe(true)
  })

  it('updateWorkspaceMcp PUTs to /{serverId} with three-state credentials wire', async () => {
    // W2-3 / FP-09 P1-4 三态：缺键=保留，null/{}=清空，值=覆盖。
    const bodies: unknown[] = []
    server.use(
      http.put('/api/workspaces/ws-1/mcp-servers/srv-1', async ({ request }) => {
        bodies.push(await request.json())
        return HttpResponse.json(SERVER({ name: 'renamed', transport: 'sse' }))
      }),
    )
    await updateWorkspaceMcp('ws-1', 'srv-1', {
      name: 'renamed',
      transport: 'sse',
      url: 'https://mcp.example.com/sse',
      credentials: { headers: { Authorization: 'Bearer sk-new' } },
    })
    await updateWorkspaceMcp('ws-1', 'srv-1', {
      name: 'renamed',
      transport: 'sse',
      url: 'https://mcp.example.com/sse',
      credentials: null,
    })
    await updateWorkspaceMcp('ws-1', 'srv-1', {
      name: 'renamed',
      transport: 'sse',
      url: 'https://mcp.example.com/sse',
    })
    expect(bodies).toEqual([
      {
        name: 'renamed',
        transport: 'sse',
        url: 'https://mcp.example.com/sse',
        credentials: { headers: { Authorization: 'Bearer sk-new' } },
      },
      {
        name: 'renamed',
        transport: 'sse',
        url: 'https://mcp.example.com/sse',
        credentials: null,
      },
      // 不传 credentials：键整体不存在（不是 undefined 占位、更不是 {}）。
      { name: 'renamed', transport: 'sse', url: 'https://mcp.example.com/sse' },
    ])
  })

  it('deleteWorkspaceMcp DELETEs /{serverId} and returns { result }', async () => {
    server.use(
      http.delete('/api/workspaces/ws-1/mcp-servers/srv-1', () =>
        HttpResponse.json({ result: 'deleted' }),
      ),
    )
    await expect(deleteWorkspaceMcp('ws-1', 'srv-1')).resolves.toEqual({ result: 'deleted' })
  })
})

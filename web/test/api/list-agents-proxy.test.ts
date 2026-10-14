import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { listAgents, type Agent } from '@/api/orchestration'
import { server } from '../msw'

// 助理代理行过滤（docs/assistant-access-design-2026-10-14.md §4.1）：
// GET /api/agents 会返回 runtime_kind='assistant_proxy' 的代理行（"张三(AI 代)"），
// listAgents 默认把它过滤掉（一处收口），需要渲染代理名的消费方
// （useAgentNameMap 等）显式传 includeAssistantProxy: true。

const ENGINE: Agent = {
  id: 'ag-1',
  workspace_id: 'ws-1',
  name: 'alpha',
  description: '',
  runtime_config: {},
  max_concurrent_tasks: 1,
  visibility: 'workspace',
  archived: false,
  created_at: '2026-10-11T08:00:00Z',
  runtime_kind: 'engine',
}
const PROXY: Agent = {
  ...ENGINE,
  id: 'ag-proxy',
  name: 'assistant-proxy-zhangsan',
  runtime_kind: 'assistant_proxy',
}
const EXTERNAL: Agent = {
  ...ENGINE,
  id: 'ag-2',
  name: 'worker-1',
  runtime_kind: 'external',
}
// 旧后端可能不发 runtime_kind——缺省按非 proxy 处理，不得误删。
const LEGACY: Agent = { ...ENGINE, id: 'ag-3', name: 'legacy', runtime_kind: undefined }

describe('listAgents assistant_proxy filtering', () => {
  it('default filters assistant_proxy rows, keeps engine/external/legacy', async () => {
    server.use(
      http.get('/api/agents', () => HttpResponse.json({ agents: [ENGINE, PROXY, EXTERNAL, LEGACY] })),
    )
    const rows = await listAgents()
    expect(rows.map((r) => r.id)).toEqual(['ag-1', 'ag-2', 'ag-3'])
  })

  it('includeAssistantProxy:true keeps the proxy row for name rendering', async () => {
    server.use(
      http.get('/api/agents', () => HttpResponse.json({ agents: [ENGINE, PROXY] })),
    )
    const rows = await listAgents({ includeAssistantProxy: true })
    expect(rows.map((r) => r.id)).toEqual(['ag-1', 'ag-proxy'])
  })

  it('handles null agents payload as empty list', async () => {
    server.use(http.get('/api/agents', () => HttpResponse.json({ agents: null })))
    await expect(listAgents()).resolves.toEqual([])
  })
})

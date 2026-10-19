import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { ConfirmProvider } from '@ploykit/ui'
import type { PackUIEntry, WorkspaceMCPServer } from '@/api/packs'
import { PacksInstalledSection, WorkspaceMcpSection } from '@/components/packs-sections'
import { server } from '../msw'

// setup.ts pins i18n to 'en'; components render through i18next so
// assertions target the English copy from packs.*.

const ENTRY = (over: Partial<PackUIEntry> = {}): PackUIEntry => ({
  id: 'pe-1',
  workspace_id: 'ws-1',
  pack: 'agentblog',
  label: 'Content Factory',
  url_template: 'https://agentblog.internal/console',
  installed_at: '2026-10-11T08:00:00Z',
  ...over,
})

const SERVER = (over: Partial<WorkspaceMCPServer> = {}): WorkspaceMCPServer => ({
  id: 'srv-1',
  workspace_id: 'ws-1',
  name: 'web-search',
  transport: 'http',
  url: 'https://mcp.example.com/mcp',
  has_credentials: false,
  created_at: '2026-10-11T08:00:00Z',
  ...over,
})

function render_ui(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  // ConfirmProvider：删除两段式收口 useConfirm（ui-sweep 裁决①）后，
  // WorkspaceMcpSection 的删除流程需要确认弹窗宿主。
  return render(
    <QueryClientProvider client={qc}>
      <ConfirmProvider>{ui}</ConfirmProvider>
    </QueryClientProvider>,
  )
}

describe('PacksInstalledSection (three states)', () => {
  it('empty state: shows onboarding copy when zero packs', async () => {
    server.use(http.get('/api/ui-entries', () => HttpResponse.json({ items: [] })))
    render_ui(<PacksInstalledSection />)
    expect(await screen.findByTestId('packs-empty')).toBeInTheDocument()
    expect(screen.getByText('No packs installed yet')).toBeInTheDocument()
    expect(screen.queryByTestId('packs-row')).toBeNull()
  })

  it('single pack: renders pack name + label + installed date', async () => {
    server.use(http.get('/api/ui-entries', () => HttpResponse.json({ items: [ENTRY()] })))
    render_ui(<PacksInstalledSection />)
    expect(await screen.findByTestId('packs-list')).toBeInTheDocument()
    const rows = screen.getAllByTestId('packs-row')
    expect(rows).toHaveLength(1)
    expect(screen.getByText('agentblog')).toBeInTheDocument()
    expect(screen.getByText('Content Factory')).toBeInTheDocument()
    expect(screen.getByText(/Installed /)).toBeInTheDocument()
  })

  it('multiple packs: one row per pack, no health/version/tool columns', async () => {
    server.use(
      http.get('/api/ui-entries', () =>
        HttpResponse.json({ items: [ENTRY(), ENTRY({ id: 'pe-2', pack: 'dify', label: 'Console' })] }),
      ),
    )
    render_ui(<PacksInstalledSection />)
    await screen.findByTestId('packs-list')
    expect(screen.getAllByTestId('packs-row')).toHaveLength(2)
    expect(screen.getByText('dify')).toBeInTheDocument()
    expect(screen.getByText('Console')).toBeInTheDocument()
  })
})

describe('WorkspaceMcpSection (registry CRUD)', () => {
  it('empty state when no servers registered', async () => {
    server.use(
      http.get('/api/workspaces/ws-1/mcp-servers', () => HttpResponse.json({ servers: [] })),
    )
    render_ui(<WorkspaceMcpSection workspaceId="ws-1" />)
    expect(await screen.findByTestId('ws-mcp-empty')).toBeInTheDocument()
  })

  it('lists servers with name / transport / url', async () => {
    server.use(
      http.get('/api/workspaces/ws-1/mcp-servers', () => HttpResponse.json({ servers: [SERVER()] })),
    )
    render_ui(<WorkspaceMcpSection workspaceId="ws-1" />)
    expect(await screen.findByTestId('ws-mcp-row')).toBeInTheDocument()
    expect(screen.getByText('web-search')).toBeInTheDocument()
    expect(screen.getByText('https://mcp.example.com/mcp')).toBeInTheDocument()
  })

  it('add server: dialog form submits POST and the list refetches', async () => {
    const posts: unknown[] = []
    let listCalls = 0
    server.use(
      http.get('/api/workspaces/ws-1/mcp-servers', () => {
        listCalls++
        return HttpResponse.json({ servers: listCalls > 1 ? [SERVER({ name: 'fresh' })] : [] })
      }),
      http.post('/api/workspaces/ws-1/mcp-servers', async ({ request }) => {
        posts.push(await request.json())
        return HttpResponse.json(SERVER({ name: 'fresh' }), { status: 201 })
      }),
    )
    render_ui(<WorkspaceMcpSection workspaceId="ws-1" />)
    fireEvent.click(await screen.findByRole('button', { name: /Add server/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'fresh' } })
    fireEvent.change(screen.getByLabelText('URL'), {
      target: { value: 'https://mcp.example.com/mcp' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.getByTestId('ws-mcp-row')).toBeInTheDocument())
    // W2-3 三态：创建无凭证时不发 credentials 键（缺省即空凭证，后端密封层无需触碰）。
    // transport 缺省随 2026-10-17 统一裁决变为 streamable_http（合法值仅
    // streamable_http | sse）。
    expect(posts).toEqual([
      { name: 'fresh', url: 'https://mcp.example.com/mcp', transport: 'streamable_http' },
    ])
    expect(screen.getByText('fresh')).toBeInTheDocument()
  })

  it('delete server: confirm flow issues DELETE and removes the row', async () => {
    const deletes: string[] = []
    let listCalls = 0
    server.use(
      http.get('/api/workspaces/ws-1/mcp-servers', () => {
        listCalls++
        return HttpResponse.json({ servers: listCalls > 1 ? [] : [SERVER()] })
      }),
      http.delete('/api/workspaces/ws-1/mcp-servers/:id', ({ params }) => {
        deletes.push(String(params.id))
        return HttpResponse.json({ result: 'deleted' })
      }),
    )
    render_ui(<WorkspaceMcpSection workspaceId="ws-1" />)
    fireEvent.click(
      await screen.findByRole('button', { name: 'Delete server web-search' }),
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Confirm delete' }))
    await waitFor(() => expect(screen.queryByTestId('ws-mcp-row')).toBeNull())
    expect(deletes).toEqual(['srv-1'])
  })

  it('edit server: pre-fills the form and PUTs to /{serverId}', async () => {
    const puts: { url: string; body: unknown }[] = []
    server.use(
      http.get('/api/workspaces/ws-1/mcp-servers', () => HttpResponse.json({ servers: [SERVER()] })),
      http.put('/api/workspaces/ws-1/mcp-servers/:id', async ({ request, params }) => {
        puts.push({ url: String(params.id), body: await request.json() })
        return HttpResponse.json(SERVER({ name: 'renamed' }))
      }),
    )
    render_ui(<WorkspaceMcpSection workspaceId="ws-1" />)
    fireEvent.click(await screen.findByRole('button', { name: /Edit/ }))
    const nameInput = screen.getByLabelText('Name') as HTMLInputElement
    expect(nameInput.value).toBe('web-search')
    fireEvent.change(nameInput, { target: { value: 'renamed' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0]!.url).toBe('srv-1')
    // SERVER() 夹具仍是旧值 'http'（存量行）：编辑表单归一到 streamable_http
    // 再提交（旧值不在下拉合法值内会渲染成空选中）。
    expect(puts[0]!.body).toMatchObject({ name: 'renamed', transport: 'streamable_http' })
  })

  // W2-3 / FP-09 P1-4 三态凭证（编辑已存凭证的服务器），三步各自独立渲染
  //（保存成功即关对话框，无法在同一渲染里连发三次 PUT）：
  //   留空保存 = 不发 credentials 键（保留）；勾选清除 = null（清空）；
  //   输入新值 = 覆盖。琥珀警示只跟随"清除"勾选出现。
  async function openEditWithStoredCreds() {
    server.use(
      http.get('/api/workspaces/ws-1/mcp-servers', () =>
        HttpResponse.json({ servers: [SERVER({ has_credentials: true })] }),
      ),
    )
    render_ui(<WorkspaceMcpSection workspaceId="ws-1" />)
    fireEvent.click(await screen.findByRole('button', { name: /Edit/ }))
  }

  it('edit server: leaving the key blank keeps stored credentials (no credentials key sent)', async () => {
    const puts: unknown[] = []
    server.use(
      http.put('/api/workspaces/ws-1/mcp-servers/:id', async ({ request }) => {
        puts.push(await request.json())
        return HttpResponse.json(SERVER({ has_credentials: true }))
      }),
    )
    await openEditWithStoredCreds()
    expect(
      screen.queryByText('Saving will clear the credentials stored for this server.'),
    ).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0]).not.toHaveProperty('credentials')
  })

  it('edit server: the clear checkbox sends credentials null after confirmation copy', async () => {
    const puts: unknown[] = []
    server.use(
      http.put('/api/workspaces/ws-1/mcp-servers/:id', async ({ request }) => {
        puts.push(await request.json())
        return HttpResponse.json(SERVER({ has_credentials: false }))
      }),
    )
    await openEditWithStoredCreds()
    fireEvent.click(screen.getByRole('checkbox'))
    expect(
      screen.getByText('Saving will clear the credentials stored for this server.'),
    ).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0]).toMatchObject({ credentials: null })
  })

  it('edit server: entering a new key overwrites (Bearer headers wire)', async () => {
    const puts: unknown[] = []
    server.use(
      http.put('/api/workspaces/ws-1/mcp-servers/:id', async ({ request }) => {
        puts.push(await request.json())
        return HttpResponse.json(SERVER({ has_credentials: true }))
      }),
    )
    await openEditWithStoredCreds()
    fireEvent.change(
      screen.getByLabelText(
        'API Key (leave blank to keep stored credentials; entering a new one overwrites)',
      ),
      { target: { value: 'sk-2' } },
    )
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(puts).toHaveLength(1))
    expect(puts[0]).toMatchObject({
      credentials: { headers: { Authorization: 'Bearer sk-2' } },
    })
  })
})

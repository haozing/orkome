import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { server } from '../msw'
import type { PackUIEntry } from '@/api/packs'
import { aggregateHealth, PackDetailView } from '@/components/pack-detail'
import { UiEntriesSection } from '@/components/integrations/ui-entries-section'
import { useWorkspaceStore } from '@/stores/workspace'

// Pack 一等公民化 P1 页面钉子（pack-first-class-design-2026-10-13.md §5 验收）：
// ① 集成页段③「页面入口」读写面（/packs 列表并入 /tools 的包卡片区已随
//    integrations-page-design-2026-10-15 §1.3 删除，由本段的来源徽标用例接替）；
// ② /packs/$packId 五区块渲染 + 探活三态聚合（aggregateHealth 纯函数）+
//    卸载 P1 只弹 CLI 提示（不打接口）。
// setup.ts 固定 i18n=en，断言用英文 tools.* / packs2.* 文案。
// 路由桩沿 work-item-detail-tabs 的做法：createFileRoute 返回带 useParams
// 的轻对象，Link 渲染成 <a>。

const st = vi.hoisted(() => ({ params: { packId: 'pk-1' } }))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (options: Record<string, unknown>) => ({
      options,
      useParams: () => st.params,
    }),
    // 渲染成 <a>；params 是路由参数对象，剥掉避免 React 未知属性告警
    Link: ({ params, ...props }: { params?: unknown } & React.ComponentProps<'a'>) => {
      void params
      return <a {...props} />
    },
  }
})

const ENTRY_FROM_PACK: PackUIEntry = {
  id: 'ue-1',
  workspace_id: 'ws-1',
  pack: 'agentblog',
  pack_id: 'pk-1',
  label: 'Content Factory',
  url_template: 'https://agentblog.internal/console',
  slots: [],
  installed_at: '2026-10-15T08:00:00Z',
}

// 手动入口（pack_id 缺省 = 非包来源）：wire 上 pack_id 为 omitempty，缺键即手动
const ENTRY_MANUAL: PackUIEntry = {
  id: 'ue-2',
  workspace_id: 'ws-1',
  pack: 'custom-internal-console',
  label: 'Internal console',
  url_template: 'https://intranet.example.com/console',
  slots: [],
  installed_at: '2026-10-15T08:00:00Z',
}

const DETAIL = (over: Record<string, unknown> = {}) => ({
  pack: { id: 'pk-1', name: 'agentblog', version: '0.1.0', installed_at: '2026-10-13T08:00:00Z' },
  servers: [
    {
      name: 'agentblog-mcp',
      transport: 'http',
      url: 'https://mcp.example.com/mcp',
      has_credentials: true,
    },
  ],
  tool_schemas: [
    {
      server: 'agentblog-mcp',
      tool_name: 'publish_post',
      source: 'protocol',
      installed_at: '2026-10-13T08:00:00Z',
    },
    {
      server: 'agentblog-mcp',
      tool_name: 'list_drafts',
      source: 'pack_yaml',
      installed_at: '2026-10-13T08:00:00Z',
    },
  ],
  ui_entry: {
    label: 'Content Factory',
    url_template: 'https://agentblog.internal/console?ws={workspaceId}&t={uiToken}',
    slots: [{ slot: 'workitem.detail.tab', label: 'Blog drafts', height: 420 }],
  },
  subscriptions: [
    {
      subscription_id: 'sub-1',
      url: 'https://agentblog.internal/hooks/orkome',
      event_types: ['workitem.completed'],
      is_active: true,
    },
  ],
  agents: [
    { id: 'a-1', name: 'blogger', archived: false },
    { id: 'a-2', name: 'editor', archived: true },
  ],
  skills: [{ id: 'sk-1', name: 'seo-style' }],
  ...over,
})

function render_ui(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
}

describe('aggregateHealth (probe three-state rollup)', () => {
  it('all ok → healthy; mixed → partial; none ok → down; zero servers → healthy', () => {
    expect(aggregateHealth(3, 3)).toBe('healthy')
    expect(aggregateHealth(3, 1)).toBe('partial')
    expect(aggregateHealth(2, 0)).toBe('down')
    expect(aggregateHealth(0, 0)).toBe('healthy')
  })
})

describe('/tools ui-entries section', () => {
  it('empty: renders the add control', async () => {
    server.use(http.get('/api/ui-entries', () => HttpResponse.json({ items: [] })))
    render_ui(<UiEntriesSection workspaceId="ws-1" role="admin" />)
    expect(await screen.findByText('No page entries yet.')).toBeInTheDocument()
    expect(screen.getByTestId('ui-entry-add')).toBeInTheDocument()
    expect(screen.queryByTestId('ui-entry-row')).toBeNull()
  })

  it('rows: pack_id renders the from-pack badge; missing pack_id renders the manual badge', async () => {
    server.use(
      http.get('/api/ui-entries', () =>
        HttpResponse.json({ items: [ENTRY_FROM_PACK, ENTRY_MANUAL] }),
      ),
    )
    render_ui(<UiEntriesSection workspaceId="ws-1" role="admin" />)
    expect(await screen.findAllByTestId('ui-entry-row')).toHaveLength(2)
    // 来自包：徽标即 /packs/{pack_id} 详情链
    const fromPack = screen.getByTestId('ui-entry-from-pack')
    expect(fromPack.textContent).toContain('From pack')
    expect(fromPack.getAttribute('to')).toBe('/packs/$packId')
    // 手动：无 pack_id → 只读徽标
    expect(screen.getByTestId('ui-entry-manual').textContent).toContain('Manual')
  })
})

describe('/packs/$packId detail (five sections)', () => {
  beforeEach(() => {
    server.use(
      http.get('/api/packs/pk-1', () => HttpResponse.json(DETAIL())),
      http.get('/api/workspaces/ws-1/mcp-servers', () =>
        HttpResponse.json({
          servers: [
            {
              id: 'srv-1',
              workspace_id: 'ws-1',
              name: 'agentblog-mcp',
              transport: 'http',
              url: 'https://mcp.example.com/mcp',
              has_credentials: true,
              created_at: '2026-10-13T08:00:00Z',
            },
          ],
        }),
      ),
    )
  })

  async function renderDetail() {
    useWorkspaceStore.setState({ currentId: 'ws-1' })
    render_ui(<PackDetailView packId="pk-1" />)
    await screen.findByText('Overview')
    // 探活按钮在注册表（名→id 映射）就绪后才启用
    await waitFor(() => expect(screen.getByRole('button', { name: 'Probe' })).toBeEnabled())
    return screen
  }

  it('renders all five sections with archive tools, slots and subscriptions', async () => {
    await renderDetail()
    for (const title of ['Overview', 'Tools', 'Events', 'UI', 'Assets']) {
      expect(screen.getByRole('region', { name: title })).toBeInTheDocument()
    }
    // ② 存档清单 + source 徽标
    expect(screen.getByText('publish_post')).toBeInTheDocument()
    expect(screen.getByText('list_drafts')).toBeInTheDocument()
    // ④ slots 行（slot 标识 + 高度）
    expect(screen.getByTestId('pack-slot-row').textContent).toContain('workitem.detail.tab')
    expect(screen.getByTestId('pack-slot-row').textContent).toContain('420px')
    // ③ 订阅 URL + types + active（方向说明替代了旧的"投递统计即将上线"占位）
    expect(screen.getByTestId('pack-sub-row').textContent).toContain('workitem.completed')
    expect(screen.getByTestId('pack-sub-row').textContent).toContain('Active')
    expect(screen.getByTestId('pack-sub-ping')).toBeInTheDocument()
    expect(screen.getByTestId('pack-sub-rotate')).toBeInTheDocument()
    expect(screen.getByTestId('pack-sub-toggle')).toBeInTheDocument()
    expect(screen.getByTestId('pack-sub-deliveries')).toBeInTheDocument()
    expect(screen.getByTestId('pack-events-catalog')).toBeInTheDocument()
    expect(screen.getByText(/workspace Webhook management/)).toBeInTheDocument()
    // ⑤ agents（archived 灰显行带后缀）与 skills
    const agentRows = screen.getAllByTestId('pack-agent-row')
    expect(agentRows.some((r) => r.textContent!.includes('blogger'))).toBe(true)
    const archivedRow = agentRows.find((r) => r.textContent!.includes('editor'))
    expect(archivedRow!.textContent).toContain('editor')
    expect(archivedRow!.className).toContain('text-zinc-400')
    expect(screen.getByTestId('pack-skill-row').textContent).toContain('seo-style')
    // 健康态初始未知
    expect(screen.getByTestId('pack-overview-health').textContent).toContain('Not probed yet')
  })

  it('probe button: rolls all server probes up into the three-state badge', async () => {
    server.use(
      http.post('/api/workspaces/ws-1/mcp-servers/srv-1/probe-tools', () =>
        HttpResponse.json({ tools: [{ name: 'publish_post' }] }),
      ),
    )
    await renderDetail()
    fireEvent.click(screen.getByRole('button', { name: 'Probe' }))
    await waitFor(() => expect(screen.getByTestId('pack-health-badge')).toBeInTheDocument())
    expect(screen.getByTestId('pack-health-badge').textContent).toContain('Healthy')
    expect(screen.getByTestId('pack-health-detail')!.textContent).toContain('1/1')
  })

  it('probe button: any failing server flips the badge to Partially healthy', async () => {
    server.use(
      http.post('/api/workspaces/ws-1/mcp-servers/srv-1/probe-tools', () =>
        HttpResponse.json({ error: 'E_UPSTREAM', message: 'refused' }, { status: 502 }),
      ),
    )
    await renderDetail()
    fireEvent.click(screen.getByRole('button', { name: 'Probe' }))
    await waitFor(() => expect(screen.getByTestId('pack-health-badge')).toBeInTheDocument())
    expect(screen.getByTestId('pack-health-badge').textContent).toContain('Unreachable')
  })

  it('uninstall: P1 only opens the CLI hint dialog, no DELETE hits the wire', async () => {
    const deletes: string[] = []
    server.use(
      http.delete('/api/packs/pk-1', () => {
        deletes.push('hit')
        return HttpResponse.json({})
      }),
    )
    await renderDetail()
    fireEvent.click(screen.getByRole('button', { name: 'Uninstall' }))
    expect(await screen.findByTestId('pack-uninstall-cmd')).toBeInTheDocument()
    expect(screen.getByTestId('pack-uninstall-cmd').textContent).toContain(
      'pack-provision uninstall',
    )
    expect(screen.getByTestId('pack-uninstall-cmd').textContent).toContain('ws-1')
    // 交互链之外无卸载请求
    expect(deletes).toEqual([])
  })
})

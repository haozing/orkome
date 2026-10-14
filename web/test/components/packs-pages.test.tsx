import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { server } from '../msw'
import { aggregateHealth, PackDetailView } from '@/components/pack-detail'
import { PacksPageView } from '@/components/packs-page'
import { useWorkspaceStore } from '@/stores/workspace'

// Pack 一等公民化 P1 页面钉子（pack-first-class-design-2026-10-13.md §5 验收）：
// ① /packs 列表三态（含 CLI 引导空态、行=名/版本/日期/三计数）；
// ② /packs/$packId 五区块渲染 + 探活三态聚合（aggregateHealth 纯函数）+
//    卸载 P1 只弹 CLI 提示（不打接口）。
// setup.ts 固定 i18n=en，断言用英文 packs2.* 文案。
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
    Link: (props: React.ComponentProps<'a'>) => <a {...props} />,
  }
})

const PACK = (over: Record<string, unknown> = {}) => ({
  id: 'pk-1',
  name: 'agentblog',
  version: '0.1.0',
  installed_at: '2026-10-13T08:00:00Z',
  server_count: 2,
  agent_count: 1,
  skill_count: 3,
  ...over,
})

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

describe('/packs list (three states)', () => {
  it('empty: onboarding copy + pack-provision install command block', async () => {
    server.use(http.get('/api/packs', () => HttpResponse.json({ items: [] })))
    render_ui(<PacksPageView />)
    expect(await screen.findByText('No packs installed yet')).toBeInTheDocument()
    const cmd = screen.getByTestId('packs-install-cmd')
    expect(cmd.textContent).toContain('pack-provision install')
    // 平台化 P2：头部安装入口（向导页）
    expect(screen.getByTestId('packs-install-link')).toBeInTheDocument()
    expect(screen.getByTestId('packs-install-link').getAttribute('to')).toBe('/packs/install')
  })

  it('rows: name, version, asset counts; click-through to detail', async () => {
    server.use(http.get('/api/packs', () => HttpResponse.json({ items: [PACK()] })))
    render_ui(<PacksPageView />)
    const row = await screen.findByTestId('packs2-row')
    expect(row.textContent).toContain('agentblog')
    expect(row.textContent).toContain('v0.1.0')
    // Link 桩透传 to（不解析 params），断言模板形状即可
    expect(row.getAttribute('to')).toBe('/packs/$packId')
    // 三资产计数（服务器/智能体以图标+数字呈现，技能为文本）
    expect(row.textContent).toContain('2')
    expect(row.textContent).toContain('1')
    expect(row.textContent).toContain('Skills 3')
  })

  it('error state: PageError with retry', async () => {
    server.use(
      http.get('/api/packs', () =>
        HttpResponse.json({ error: 'E_DB', message: 'boom' }, { status: 500 }),
      ),
    )
    render_ui(<PacksPageView />)
    expect(await screen.findByRole('alert')).toBeInTheDocument()
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

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { server } from '../msw'
import { PackInstallWizardView } from '@/components/pack-install-wizard'
import { PackDetailView } from '@/components/pack-detail'
import { useWorkspaceStore } from '@/stores/workspace'

// 能力包平台化 P2 页面钉子：
// ① /packs/install 向导——分区表单渲染 + 动态行增删 + 提交 body 形状 +
//    一次性密钥 Dialog（有 secret 进 Dialog，无 secret 直跳详情）；
// ② /packs/$packId 详情——「使用文档」按钮/Dialog（复制/下载/存为技能链）+
//    资产「挂载」picker（过滤已挂 + 确认逐个 POST）+ 已挂行「卸下」空串提交。
// mock 惯例照 packs-pages.test.tsx：msw handler + 路由桩（Link 渲染 <a>）+
// QueryClientProvider 包裹；i18n 固定 en，断言英文文案。

const st = vi.hoisted(() => ({
  params: { packId: 'pk-1' },
  navigate: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (options: Record<string, unknown>) => ({
      options,
      useParams: () => st.params,
    }),
    Link: (props: React.ComponentProps<'a'>) => <a {...props} />,
    useNavigate: () => st.navigate,
  }
})

const DETAIL = () => ({
  pack: { id: 'pk-1', name: 'agentblog', version: '0.1.0', installed_at: '2026-10-13T08:00:00Z' },
  servers: [],
  tool_schemas: [],
  ui_entry: null,
  subscriptions: [],
  agents: [{ id: 'a-1', name: 'blogger', archived: false }],
  skills: [{ id: 'sk-1', name: 'seo-style' }],
})

function render_ui(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>)
}

describe('/packs/install wizard', () => {
  beforeEach(() => {
    st.navigate.mockReset()
    useWorkspaceStore.setState({ currentId: 'ws-1' })
  })

  it('renders sectioned form: basics + empty dynamic sections + submit disabled without name', async () => {
    render_ui(<PackInstallWizardView />)
    // 三分区 + 提交按钮
    expect(screen.getByRole('region', { name: 'Basics' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'MCP servers' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Event subscriptions' })).toBeInTheDocument()
    // name 必填：version 默认 0.1.0；未填 name 时提交禁用
    expect(screen.getByTestId('pack-install-version')).toHaveValue('0.1.0')
    expect(screen.getByTestId('pack-install-submit')).toBeDisabled()
    // 动态段空态
    expect(screen.getByTestId('pack-install-mcp-empty')).toBeInTheDocument()
    expect(screen.getByTestId('pack-install-event-empty')).toBeInTheDocument()
    // 界面进驻默认收起
    expect(screen.getByTestId('pack-install-ui-toggle')).not.toBeChecked()
  })

  it('adds/removes mcp rows and event rows; ui toggle expands slot form', async () => {
    server.use(
      http.get('/api/workspaces/ws-1/webhooks/events', () =>
        HttpResponse.json({ items: [{ type: 'work_item.updated', description: 'd' }] }),
      ),
    )
    render_ui(<PackInstallWizardView />)
    fireEvent.click(screen.getByTestId('pack-install-mcp-add'))
    expect(screen.getByTestId('pack-install-mcp-row')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('pack-install-mcp-remove'))
    expect(screen.queryByTestId('pack-install-mcp-row')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('pack-install-event-add'))
    expect(screen.getByTestId('pack-install-event-row')).toBeInTheDocument()
    // 事件目录按需拉取（有行才 enabled）
    expect(await screen.findByTestId('pack-install-event-type')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('pack-install-event-remove'))
    expect(screen.queryByTestId('pack-install-event-row')).not.toBeInTheDocument()
    // 界面进驻开关展开 slot 表单
    fireEvent.click(screen.getByTestId('pack-install-ui-toggle'))
    expect(screen.getByTestId('pack-install-slot-add')).toBeInTheDocument()
  })

  it('submits install body and jumps to detail when no secrets echoed', async () => {
    const bodies: unknown[] = []
    server.use(
      http.post('/api/packs/install', async ({ request }) => {
        bodies.push(await request.json())
        return HttpResponse.json({
          pack: {
            id: 'pk-new',
            workspace_id: 'ws-1',
            name: 'x',
            version: '0.1.0',
            installed_at: '2026-10-14T08:00:00Z',
          },
          subscriptions: [{ url: 'https://v/hooks', subscription_id: 'sub-9', secret: '' }],
        })
      }),
      http.get('/api/workspaces/ws-1/webhooks/events', () =>
        HttpResponse.json({ items: [{ type: 'work_item.updated', description: 'd' }] }),
      ),
    )
    render_ui(<PackInstallWizardView />)
    fireEvent.change(screen.getByTestId('pack-install-name'), { target: { value: 'x' } })
    // 加一条 MCP 行（带凭证开关）
    fireEvent.click(screen.getByTestId('pack-install-mcp-add'))
    fireEvent.change(screen.getByTestId('pack-install-mcp-name'), { target: { value: 'srv' } })
    fireEvent.change(screen.getByTestId('pack-install-mcp-url'), { target: { value: 'https://mcp' } })
    fireEvent.click(screen.getByTestId('pack-install-mcp-cred-toggle'))
    fireEvent.change(screen.getByTestId('pack-install-mcp-provider'), {
      target: { value: 'TOKEN_KEY' },
    })
    fireEvent.change(screen.getByTestId('pack-install-mcp-secret'), {
      target: { value: 'sk-1' },
    })
    // 加一条事件订阅并勾类型
    fireEvent.click(screen.getByTestId('pack-install-event-add'))
    await screen.findByTestId('pack-install-event-type')
    fireEvent.change(screen.getByTestId('pack-install-event-url'), {
      target: { value: 'https://v/hooks' },
    })
    fireEvent.click(screen.getByTestId('pack-install-event-type'))
    fireEvent.click(screen.getByTestId('pack-install-submit'))
    await waitFor(() => expect(st.navigate).toHaveBeenCalled())
    expect(bodies[0]).toEqual({
      name: 'x',
      version: '0.1.0',
      mcp: [
        {
          name: 'srv',
          transport: 'http',
          url: 'https://mcp',
          credential: { provider: 'TOKEN_KEY', secret: 'sk-1' },
        },
      ],
      events: [{ url: 'https://v/hooks', types: ['work_item.updated'], description: undefined }],
    })
    // secret 为空 → 不弹密钥 Dialog，直接跳详情
    expect(st.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ to: '/packs/$packId', params: { packId: 'pk-new' } }),
    )
  })

  it('shows one-time secrets dialog when install echoes non-empty secrets', async () => {
    server.use(
      http.post('/api/packs/install', () =>
        HttpResponse.json({
          pack: {
            id: 'pk-new',
            workspace_id: 'ws-1',
            name: 'x',
            version: '0.1.0',
            installed_at: '2026-10-14T08:00:00Z',
          },
          subscriptions: [
            { url: 'https://v/hooks', subscription_id: 'sub-9', secret: 'whk_abc' },
          ],
        }),
      ),
      http.get('/api/workspaces/ws-1/webhooks/events', () => HttpResponse.json({ items: [] })),
    )
    render_ui(<PackInstallWizardView />)
    fireEvent.change(screen.getByTestId('pack-install-name'), { target: { value: 'x' } })
    fireEvent.click(screen.getByTestId('pack-install-submit'))
    // 一次性密钥展示：警示 + 等宽密钥 + 复制按钮
    const secret = await screen.findByTestId('pack-install-secret-value')
    expect(secret.textContent).toBe('whk_abc')
    expect(screen.getByTestId('pack-install-secrets-done')).toBeInTheDocument()
    // 关闭后跳详情
    fireEvent.click(screen.getByTestId('pack-install-secrets-done'))
    await waitFor(() =>
      expect(st.navigate).toHaveBeenCalledWith(
        expect.objectContaining({ to: '/packs/$packId', params: { packId: 'pk-new' } }),
      ),
    )
  })
})

describe('/packs/$packId platform features (usage doc + asset mounting)', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({ currentId: 'ws-1' })
    server.use(
      http.get('/api/packs/pk-1', () => HttpResponse.json(DETAIL())),
      http.get('/api/workspaces/ws-1/mcp-servers', () =>
        HttpResponse.json({ servers: [] }),
      ),
    )
  })

  async function renderDetail() {
    render_ui(<PackDetailView packId="pk-1" />)
    await screen.findByText('Overview')
  }

  it('header exposes usage-guide button; dialog shows markdown raw text with copy/download/save-as-skill', async () => {
    server.use(
      http.get('/api/packs/pk-1/usage-document', () =>
        HttpResponse.json({ markdown: '# agentblog guide' }),
      ),
      http.post('/api/skills', () =>
        HttpResponse.json({
          id: 'sk-new',
          workspace_id: 'ws-1',
          name: 'agentblog 使用指南',
          description: 'd',
          origin: 'manual',
          created_at: '2026-10-14T08:00:00Z',
        }),
      ),
      http.put('/api/skills/sk-new/files', () =>
        HttpResponse.json({
          file: {
            id: 'f-1',
            path: 'SKILL.md',
            content: '# agentblog guide',
            sha256: 'x',
            size_bytes: 18,
          },
          manifest_sha256: 'y',
        }),
      ),
    )
    await renderDetail()
    fireEvent.click(screen.getByTestId('pack-usage-open'))
    const pre = await screen.findByTestId('pack-usage-content')
    expect(pre.textContent).toBe('# agentblog guide')
    expect(screen.getByTestId('pack-usage-copy')).toBeInTheDocument()
    expect(screen.getByTestId('pack-usage-download')).toBeInTheDocument()
    // 存为技能：默认名 + 链式 createSkill → putSkillFile → 行内提示 + 链接
    expect(screen.getByTestId('pack-usage-skill-name')).toHaveValue('agentblog 使用指南')
    fireEvent.click(screen.getByTestId('pack-usage-save-skill'))
    expect(await screen.findByTestId('pack-usage-skill-saved')).toBeInTheDocument()
  })

  it('attach picker filters already-mounted assets and posts setAgentPack per selection', async () => {
    const posted: unknown[] = []
    server.use(
      http.get('/api/agents', () =>
        HttpResponse.json({
          agents: [
            { id: 'a-1', name: 'blogger' },
            { id: 'a-2', name: 'reviewer' },
          ],
        }),
      ),
      http.post('/api/agents/a-2/pack', async ({ request }) => {
        posted.push(await request.json())
        return HttpResponse.json(null, { status: 204 })
      }),
    )
    await renderDetail()
    fireEvent.click(screen.getByTestId('pack-agent-attach'))
    // a-1 已挂 → 只剩 a-2 可选
    const boxes = await screen.findAllByTestId('pack-attach-checkbox')
    expect(boxes).toHaveLength(1)
    fireEvent.click(boxes[0]!)
    fireEvent.click(screen.getByTestId('pack-attach-confirm'))
    await waitFor(() => expect(posted).toEqual([{ pack_id: 'pk-1' }]))
    // 全部成功 → Dialog 关闭 + 详情刷新
    await waitFor(() =>
      expect(screen.queryByTestId('pack-attach-confirm')).not.toBeInTheDocument(),
    )
  })

  it('detach posts empty pack_id (unmount semantics)', async () => {
    const posted: unknown[] = []
    server.use(
      http.post('/api/agents/a-1/pack', async ({ request }) => {
        posted.push(await request.json())
        return HttpResponse.json(null, { status: 204 })
      }),
    )
    await renderDetail()
    fireEvent.click(screen.getByTestId('pack-agent-detach'))
    await waitFor(() => expect(posted).toEqual([{ pack_id: '' }]))
  })
})

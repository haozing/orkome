import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { server } from '../msw'
import { PackDetailView } from '@/components/pack-detail'
import { useWorkspaceStore } from '@/stores/workspace'

// pack 详情页钉子：「使用文档」按钮/Dialog（复制/下载/存为技能链）。
// （装包向导测试已随向导退役删除——2026-10-15 集成页终态裁决。）
// mock 惯例照 pack-detail 相关测试：msw handler + 路由桩（Link 渲染 <a>）+
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

describe('/packs/$packId platform features (usage doc)', () => {
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
})

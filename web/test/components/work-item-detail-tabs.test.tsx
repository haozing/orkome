import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PackUIEntry } from '@/api/packs'
import { server } from '../msw'

// work-item 详情页动态 slot tab 测试（C2/F3，前端设计 §1.3 审计修订四触点）：
// ① zod search schema 放行 /^pack:/，garbage 丢弃
// ② DetailTab 类型 + ③ patchSearch 强转 + ④ 内容条件渲染分支——通过渲染整页验证：
// 有 slots → 出现 pack: tab 且选中时挂 PackSlotFrame；无 slots → Tabs 与现状一致。

const st = vi.hoisted(() => ({
  params: { id: 'wi-1' },
  search: {} as Record<string, unknown>,
  navigate: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute:
      () =>
      (options: Record<string, unknown>) => ({
        options,
        useParams: () => st.params,
        useSearch: () => st.search,
        useNavigate: () => st.navigate,
      }),
  }
})

vi.mock('@/api/work', () => ({
  getWorkItem: () =>
    Promise.resolve({
      id: 'wi-1',
      workspace_id: 'ws-1',
      number: 7,
      title: '写首篇 agentblog 文章',
      description: '',
      status: 'in_progress',
      status_category: 'started',
      priority: 2,
      assignee_type: 'none',
      creator_type: 'user',
      creator_id: 'u-1',
      revision: 1,
      created_at: '2026-10-11T08:00:00Z',
      updated_at: '2026-10-11T08:00:00Z',
      last_activity_at: '2026-10-11T08:00:00Z',
    }),
}))

// 重子组件轻桩：本测试只关心 Tabs 拼接与 slot tab 的条件渲染。
vi.mock('@/components/work-item/description-editor', () => ({
  DescriptionEditor: () => <div data-testid="stub-detail" />,
}))
vi.mock('@/components/work-item/label-chips', () => ({ LabelsBlock: () => null }))
vi.mock('@/components/work-item/info-panel', () => ({ InfoPanel: () => null }))
vi.mock('@/components/work-item/more-menu', () => ({ MoreMenu: () => null }))
vi.mock('@/components/work-item/property-rows', () => ({ StatusToggle: () => null }))
vi.mock('@/components/work-item/comment-thread', () => ({ CommentThread: () => null }))
vi.mock('@/components/work-item/sub-work-items', () => ({
  SubWorkItemsList: () => <div data-testid="stub-sub" />,
}))

import { Route, WorkItemDetailPage } from '@/routes/_app.work-items.$id'
import { useWorkspaceStore } from '@/stores/workspace'

const ENTRY_WITH_SLOTS = (over: Partial<PackUIEntry> = {}): PackUIEntry => ({
  id: 'pe-1',
  workspace_id: 'ws-1',
  pack: 'agentblog',
  label: 'Content Factory',
  url_template: 'https://agentblog.internal/console?ws={workspaceId}&t={uiToken}',
  slots: [
    {
      slot: 'workitem.detail.tab',
      label: 'Article preview',
      url_template: 'https://agentblog.internal/slot/preview?item={workItemId}&t={uiToken}',
      height: 480,
    },
  ],
  installed_at: '2026-10-11T08:00:00Z',
  ...over,
})

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <WorkItemDetailPage />
    </QueryClientProvider>,
  )
}

// 点击 tab → patchSearch → navigate：模拟 URL search 更新后重渲染（路由单向数据流）。
async function clickTabAndSync(label: string) {
  fireEvent.click(screen.getByRole('tab', { name: label }))
  await waitFor(() => {
    expect(st.navigate).toHaveBeenCalled()
    const call = st.navigate.mock.calls[st.navigate.mock.calls.length - 1]!
    const updater = (call[0] as { search: (prev: Record<string, unknown>) => Record<string, unknown> })
      .search
    Object.assign(st.search, updater(st.search))
  })
}

beforeEach(() => {
  useWorkspaceStore.setState({ currentId: 'ws-1' })
  st.params = { id: 'wi-1' }
  st.search = {}
  st.navigate.mockReset()
  server.use(http.post('/api/ui-tokens', () => HttpResponse.json({ token: 'tok-slot' })))
})
afterEach(() => {
  useWorkspaceStore.setState({ currentId: null })
  server.resetHandlers()
})

describe('work-item detail tabs: dynamic slot tabs (four touchpoints)', () => {
  it('zod search schema keeps static values, admits pack:* and drops garbage', () => {
    const validate = Route.options.validateSearch as (s: Record<string, unknown>) => unknown
    expect(validate({ tab: 'detail' })).toEqual({ tab: 'detail' })
    expect(validate({ tab: 'pack:agentblog' })).toEqual({ tab: 'pack:agentblog' })
    expect(validate({ tab: 'nonsense' })).toEqual({})
    expect(validate({})).toEqual({})
  })

  it('no slots → exactly the four static tabs (zero regression)', async () => {
    server.use(http.get('/api/ui-entries', () => HttpResponse.json({ items: [ENTRY_WITH_SLOTS({ slots: [] })] })))
    renderPage()
    expect(await screen.findByRole('tab', { name: 'Details' })).toBeInTheDocument()
    const tabs = screen.getAllByRole('tab')
    expect(tabs.map((t) => t.textContent)).toEqual(['Details', 'Sub items', 'Tasks', 'Timeline'])
    expect(screen.queryByTestId('pack-slot-frame')).toBeNull()
  })

  it('empty ui-entries response → four static tabs', async () => {
    server.use(http.get('/api/ui-entries', () => HttpResponse.json({ items: [] })))
    renderPage()
    await screen.findByRole('tab', { name: 'Timeline' })
    expect(screen.getAllByRole('tab')).toHaveLength(4)
  })

  it('slots present → pack tab appears and renders PackSlotFrame when selected', async () => {
    server.use(http.get('/api/ui-entries', () => HttpResponse.json({ items: [ENTRY_WITH_SLOTS()] })))
    const { rerender } = renderPage()

    // 静态 4 + 动态 1，slot label 用 pack 提供文案。
    // Base UI Tab 不把 value 落 DOM——tab 身份经点击行为断言（patchSearch 强转 pack:*）。
    await screen.findByRole('tab', { name: 'Article preview' })
    expect(screen.getAllByRole('tab')).toHaveLength(5)
    expect(screen.queryByTestId('pack-slot-frame')).toBeNull()

    await clickTabAndSync('Article preview')
    expect(st.search.tab).toBe('pack:agentblog')
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    rerender(
      <QueryClientProvider client={qc}>
        <WorkItemDetailPage />
      </QueryClientProvider>,
    )
    const frame = await screen.findByTestId('pack-slot-frame')
    const iframe = frame.querySelector('iframe')
    expect(iframe!.getAttribute('src')).toContain('item=wi-1')
    expect(iframe!.getAttribute('src')).toContain('t=tok-slot')
    // 切回 detail：PackSlotFrame 卸载、原内容回归
    await clickTabAndSync('Details')
    st.search = { ...st.search }
    rerender(
      <QueryClientProvider client={qc}>
        <WorkItemDetailPage />
      </QueryClientProvider>,
    )
    await waitFor(() => expect(screen.getByTestId('stub-detail')).toBeInTheDocument())
    expect(screen.queryByTestId('pack-slot-frame')).toBeNull()
  })

  it('stale pack:* tab in URL (pack since removed) → falls back to detail content', async () => {
    server.use(http.get('/api/ui-entries', () => HttpResponse.json({ items: [] })))
    st.search = { tab: 'pack:ghost' }
    renderPage()
    await waitFor(() => expect(screen.getByTestId('stub-detail')).toBeInTheDocument())
    expect(screen.queryByTestId('pack-slot-frame')).toBeNull()
  })
})

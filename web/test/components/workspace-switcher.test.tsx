import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SidebarProvider } from '@ploykit/ui'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { WorkspaceSwitcher } from '@/components/workspace-switcher'
import { switchWorkspace } from '@/services/workspace-switch'
import { useWorkspaceStore } from '@/stores/workspace'
import { server } from '../msw'

// 切换副作用 mock 掉：switchWorkspace 会 queryClient.removeQueries + ws 重连，
// 这里只验证"切换器把选择交给了 orkome 服务"，重连/清缓存不在本测试域。
vi.mock('@/services/workspace-switch', () => ({ switchWorkspace: vi.fn() }))

const WS_A = {
  id: 'ws-1',
  name: 'Team A',
  slug: 'team-a',
  plan_code: 'free',
  role: 'owner',
  created_at: '2026-10-02T00:00:00Z',
}
const WS_B = {
  id: 'ws-2',
  name: 'Team B',
  slug: 'team-b',
  plan_code: 'free',
  role: 'member',
  created_at: '2026-10-02T00:00:00Z',
}

// kit 组件栈（SidebarMenuButton → useSidebar）要求 SidebarProvider；文案未挂
// KitLabelsProvider 时落 kit 内置中文默认值（本组件族不直连 i18n——i18n 映射
// 在 _app.tsx 的 KitLabelsProvider 完成）。
function renderSwitcher() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <SidebarProvider>
        <WorkspaceSwitcher />
      </SidebarProvider>
    </QueryClientProvider>,
  )
}
function mockMe(workspaces: unknown[]) {
  server.use(
    http.get('/auth/me', () =>
      HttpResponse.json({ id: 'u-1', email: 'a@b.c', display_name: 'A' }),
    ),
    http.get('/api/workspaces', () => HttpResponse.json(workspaces)),
  )
}

describe('WorkspaceSwitcher (kit adapter)', () => {
  it('trigger shows the zustand-selected workspace and the menu lists the rest (kit canonical card)', async () => {
    // 数据链回归：current 来自 zustand currentId + orkome me 缓存（data 注入缝），
    // list 来自 meQueryOptions —— kit WorkspaceLayer 的内部 current 不参与。
    mockMe([WS_A, WS_B])
    useWorkspaceStore.setState({ currentId: 'ws-1' })
    renderSwitcher()
    expect(await screen.findByText('Team A')).toBeInTheDocument() // trigger 卡片
    fireEvent.click(screen.getByRole('button', { name: '切换工作区' })) // kit 默认 aria
    // "Team B" exists only as a menu item, so finding it proves the popup
    // rendered instead of throwing.
    expect(await screen.findByText('Team B')).toBeInTheDocument()
    expect(screen.getByText('工作区')).toBeInTheDocument() // group label（kit 默认文案）
  })

  it('shows the fallback label when the me query has no workspaces yet', async () => {
    mockMe([])
    renderSwitcher()
    expect(await screen.findByText('选择工作区')).toBeInTheDocument()
  })

  it('create flow posts name+slug via orkome createWorkspace and hands switching to the orkome service', async () => {
    mockMe([WS_A])
    useWorkspaceStore.setState({ currentId: 'ws-1' })
    let createBody: unknown
    server.use(
      http.post('/api/workspaces', async ({ request }) => {
        createBody = await request.json()
        return HttpResponse.json({
          id: 'ws-3',
          name: 'Team C',
          slug: 'team-c',
          role: 'owner',
          created_at: '2026-10-20T00:00:00Z',
        })
      }),
    )
    renderSwitcher()
    fireEvent.click(await screen.findByRole('button', { name: '切换工作区' }))
    fireEvent.click(await screen.findByText('新建工作区')) // kit 内置菜单项
    const input = await screen.findByRole('textbox', { name: '新工作区名称' })
    fireEvent.change(input, { target: { value: 'Team C' } })
    fireEvent.click(screen.getByRole('button', { name: '创建' }))
    // 注入 create 契约：POST /api/workspaces {name, slug}（slug 约定在 orkome 侧）
    await waitFor(() => {
      expect(createBody).toEqual({ name: 'Team C', slug: 'team-c' })
    })
    // 成功后 orkome 切换语义接管（zustand 置新 + 清缓存 + ws 重连都在服务里）
    await waitFor(() => {
      expect(vi.mocked(switchWorkspace)).toHaveBeenCalled()
    })
    // 面板收起（resolve 即收起；reject 时保持打开——错误展示归消费方）
    await waitFor(() => {
      expect(screen.queryByRole('textbox', { name: '新工作区名称' })).not.toBeInTheDocument()
    })
  })
})

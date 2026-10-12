import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import i18n from '@/i18n'
import { SettingsPage } from '@/routes/_app.settings'
import { server } from '../msw'

// settings 通知偏好区块（wave3 P2）：三组静音的读-改-写——渲染当前值
// （空组用后端默认值）、开关切换只 PATCH 被改键、成功/失败反馈。
// setup.ts 固定 i18n=en，断言用英文文案。

const USER = {
  id: 'u-1',
  email: 'tester@orkome.dev',
  display_name: 'Tester',
  created_at: '2026-10-11T08:00:00Z',
}
const WS = {
  id: 'ws-1',
  name: 'Inventory',
  slug: 'inventory',
  created_at: '2026-10-11T08:00:00Z',
}

// 服务端 'mute' 哨兵行的内存镜像：GET 原样返回（空组保持 null，复现
// pgrepo splitMutedTypes 的真实 envelope）。
let row: {
  muted_types: string[] | null
  muted_work_item_ids: string[] | null
  muted_conversation_ids: string[] | null
}

// 注意：msw 先注册者先匹配——记录 PATCH body 的测试必须用自己的
// PATCH handler 覆盖（server.use 里放前面），不能依赖这里的全量默认集。
function defaultHandlers() {
  return [
    http.get('/auth/me', () => HttpResponse.json(USER)),
    http.get('/api/workspaces', () => HttpResponse.json([WS])),
    http.get('/api/notification-preferences', () => HttpResponse.json(row)),
  ]
}

// 模拟服务端按键合并的 PATCH：记录请求体并把 patch 合入 row 后回传合并结果
function recordingPatchHandler(bodies: unknown[]) {
  return http.patch('/api/notification-preferences', async ({ request }) => {
    bodies.push(await request.json())
    const patch = bodies[bodies.length - 1] as Record<string, string[] | undefined>
    row = {
      muted_types: patch.muted_types ?? row.muted_types,
      muted_work_item_ids: patch.muted_work_item_ids ?? row.muted_work_item_ids,
      muted_conversation_ids: patch.muted_conversation_ids ?? row.muted_conversation_ids,
    }
    return HttpResponse.json(row)
  })
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <SettingsPage />
    </QueryClientProvider>,
  )
}

describe('notification preferences section', () => {
  it('renders current values; unset groups fall back to backend defaults', async () => {
    row = { muted_types: ['*', 'comment.created'], muted_work_item_ids: ['wi-1'], muted_conversation_ids: null }
    server.use(...defaultHandlers())
    renderPage()

    expect(await screen.findByRole('switch', { name: 'All categories' })).toBeChecked()
    expect(screen.getByRole('switch', { name: 'New comment' })).toBeChecked()
    expect(screen.getByRole('switch', { name: 'Task completed' })).not.toBeChecked()
    expect(screen.getByRole('switch', { name: 'Eval report' })).not.toBeChecked()

    const wiGroup = screen.getByTestId('prefs-section')
    expect(within(wiGroup).getByText('wi-1')).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Muted work items' })).toBeChecked()
    // muted_conversation_ids=null → 默认「未静音」渲染；空组开关禁用
    // （base-ui 渲染 span role=switch，禁用态是 aria-disabled 而非原生 disabled）
    expect(screen.getByText('No muted conversations')).toBeInTheDocument()
    const convSwitch = screen.getByRole('switch', { name: 'Muted conversations' })
    expect(convSwitch).not.toBeChecked()
    expect(convSwitch).toHaveAttribute('aria-disabled', 'true')
  })

  it('toggling a category on PATCHes only muted_types and shows saved feedback', async () => {
    row = { muted_types: null, muted_work_item_ids: [], muted_conversation_ids: [] }
    const bodies: unknown[] = []
    server.use(recordingPatchHandler(bodies), ...defaultHandlers())
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('switch', { name: 'Task failed' }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    // 只发改动键；null 加载态按空数组起笔
    expect(bodies[0]).toEqual({ muted_types: ['task.failed'] })
    expect(await screen.findByText('Preferences saved')).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Task failed' })).toBeChecked()
  })

  it('turning all-categories off clears the wildcard while keeping explicit types', async () => {
    row = { muted_types: ['*', 'eval_report'], muted_work_item_ids: [], muted_conversation_ids: [] }
    const bodies: unknown[] = []
    server.use(recordingPatchHandler(bodies), ...defaultHandlers())
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('switch', { name: 'All categories' }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0]).toEqual({ muted_types: ['eval_report'] })
    expect(await screen.findByRole('switch', { name: 'All categories' })).not.toBeChecked()
    expect(screen.getByRole('switch', { name: 'Eval report' })).toBeChecked()
  })

  it('adding and removing a work item PATCHes the full id list of that group', async () => {
    row = { muted_types: [], muted_work_item_ids: ['wi-1'], muted_conversation_ids: [] }
    const bodies: unknown[] = []
    server.use(recordingPatchHandler(bodies), ...defaultHandlers())
    const user = userEvent.setup()
    renderPage()
    const input = await screen.findByLabelText('Enter a work item ID')
    await user.type(input, 'wi-2')
    await user.click(screen.getByRole('button', { name: 'Add muted work item' }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0]).toEqual({ muted_work_item_ids: ['wi-1', 'wi-2'] })
    expect(await screen.findByText('wi-2')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Remove wi-1' }))
    await waitFor(() => expect(bodies).toHaveLength(2))
    expect(bodies[1]).toEqual({ muted_work_item_ids: ['wi-2'] })
    await waitFor(() => expect(screen.queryByText('wi-1')).toBeNull())
  })

  it('conversation mutes are exposed and writable', async () => {
    row = { muted_types: [], muted_work_item_ids: [], muted_conversation_ids: ['sess-1'] }
    const bodies: unknown[] = []
    server.use(recordingPatchHandler(bodies), ...defaultHandlers())
    const user = userEvent.setup()
    renderPage()
    expect(
      await screen.findByRole('switch', { name: 'Muted conversations' }),
    ).toBeChecked()
    await user.type(screen.getByLabelText('Enter a session ID'), 'sess-2')
    await user.click(screen.getByRole('button', { name: 'Add muted conversation' }))
    await waitFor(() => expect(bodies).toHaveLength(1))
    expect(bodies[0]).toEqual({ muted_conversation_ids: ['sess-1', 'sess-2'] })
    expect(await screen.findByText('sess-2')).toBeInTheDocument()
  })

  it('save failure keeps local state and surfaces the server message', async () => {
    row = { muted_types: ['*'], muted_work_item_ids: [], muted_conversation_ids: [] }
    let patches = 0
    server.use(
      ...defaultHandlers(),
      http.patch('/api/notification-preferences', () => {
        patches += 1
        return HttpResponse.json(
          { error: 'E_VALIDATION', message: 'muted type reserves session: prefix' },
          { status: 400 },
        )
      }),
    )
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('switch', { name: 'All categories' }))
    expect(await screen.findByText('Failed to save preferences')).toBeInTheDocument()
    expect(screen.getByText('muted type reserves session: prefix')).toBeInTheDocument()
    // 失败即回滚感知：开关保持原状（受控渲染不落缓存）
    expect(screen.getByRole('switch', { name: 'All categories' })).toBeChecked()
    expect(patches).toBe(1)
  })

  it('load failure shows the section error with a working retry', async () => {
    row = { muted_types: null, muted_work_item_ids: [], muted_conversation_ids: [] }
    let failing = true
    server.use(
      http.get('/auth/me', () => HttpResponse.json(USER)),
      http.get('/api/workspaces', () => HttpResponse.json([WS])),
      http.get('/api/notification-preferences', () => {
        if (failing) {
          return HttpResponse.json({ error: 'E_INTERNAL', message: 'db down' }, { status: 500 })
        }
        return HttpResponse.json(row)
      }),
    )
    renderPage()
    expect(await screen.findByText('Failed to load notification preferences')).toBeInTheDocument()
    expect(screen.getByText('db down')).toBeInTheDocument()
    failing = false
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('switch', { name: 'All categories' })).toBeInTheDocument()
  })
})

describe('notification preferences i18n keys exist in both locales', () => {
  const KEYS = [
    'notifications.title',
    'notifications.desc',
    'notifications.loading',
    'notifications.loadFailed',
    'notifications.groupTypes',
    'notifications.allTypes',
    'notifications.allTypesDesc',
    'notifications.muteTaskCompleted',
    'notifications.muteTaskCompletedDesc',
    'notifications.muteTaskFailed',
    'notifications.muteTaskFailedDesc',
    'notifications.muteCommentCreated',
    'notifications.muteCommentCreatedDesc',
    'notifications.muteApprovalRequest',
    'notifications.muteApprovalRequestDesc',
    'notifications.muteEvalReport',
    'notifications.muteEvalReportDesc',
    'notifications.groupWorkItems',
    'notifications.groupWorkItemsDesc',
    'notifications.workItemPlaceholder',
    'notifications.emptyWorkItems',
    'notifications.groupConversations',
    'notifications.groupConversationsDesc',
    'notifications.conversationPlaceholder',
    'notifications.emptyConversations',
    'notifications.add',
    'notifications.addWorkItem',
    'notifications.addConversation',
    'notifications.removeAria',
    'notifications.saved',
    'notifications.saveFailed',
  ] as const
  it.each(KEYS)('%s exists in both locales', (key) => {
    // 缺键时 i18n.t 回退返回键名本身——不等于键名即存在
    expect(i18n.t(key, { lng: 'en' })).not.toBe(key)
    expect(i18n.t(key, { lng: 'zh' })).not.toBe(key)
  })
})

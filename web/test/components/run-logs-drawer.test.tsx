import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import i18n from '@/i18n'
import type { WorkspaceTaskRow } from '@/api/orchestration'
import { TaskTranscriptDrawer } from '@/components/run-logs/task-transcript-drawer'
import { server } from '../msw'

// FP-12 P2-1 + FP-05 P1-1 抽屉侧回归钉子：
// ① 抽屉挂载即订阅 task scope（死回放修复）；
// ② 转写首拉为最新 200 形状（order=desc wire），按 seq 升序渲染，且可向上
//    翻更早历史（next_before_seq 游标），耗尽后按钮消失。
// wave3 P1（FP-12 P2-5）审批入口钉子：
// ③ awaiting_input 渲染批准/驳回并打到 approve 端点 + 失效链刷新；
//    非 awaiting_input 不渲染；失败走 i18n 文案；taskMsg.* 双语键存在。
// （抽屉独立成组件文件：router 插件的 autoCodeSplitting 会拆走路由模块的
//   非 Route 导出，无法从 _app.run-logs.index 直接引用组件做测试。）

const unsubscribe = vi.hoisted(() => vi.fn())
const subscribe = vi.hoisted(() =>
  vi.fn((prefix: string, id: string) => {
    void prefix
    void id
    return unsubscribe
  }),
)
vi.mock('@/realtime/client', () => ({
  wsClient: { subscribe: (prefix: string, id: string) => subscribe(prefix, id) },
}))
// 抽屉只用 <Link>（渲染成 <a> 即可，to/params 非法 DOM 属性无碍）。
vi.mock('@tanstack/react-router', () => ({
  Link: (props: React.ComponentProps<'a'>) => <a {...props} />,
}))

const TASK: WorkspaceTaskRow = {
  id: 't-page',
  workspace_id: 'ws-1',
  agent_id: 'a-1',
  agent_name: 'writer',
  work_item_id: 'wi-1',
  work_item_title: '盘一盘',
  status: 'running',
  priority: 0,
  attempt: 1,
  max_attempts: 3,
  context: {},
  originator_source: 'assignment',
  created_at: '2026-10-12T00:00:00Z',
}
const AWAITING_TASK: WorkspaceTaskRow = { ...TASK, status: 'awaiting_input' }
const msg = (seq: number) => ({
  id: `m-${seq}`,
  task_id: 't-page',
  seq,
  kind: 'text',
  content: `msg-${seq}`,
  truncated: false,
  created_at: '2026-10-12T00:0' + seq + ':00Z',
})

function renderDrawer(task: WorkspaceTaskRow = TASK) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const invalidateSpy = vi.spyOn(qc, 'invalidateQueries')
  const rendered = render(
    <QueryClientProvider client={qc}>
      <TaskTranscriptDrawer task={task} onClose={() => {}} />
    </QueryClientProvider>,
  )
  return { ...rendered, invalidateSpy }
}
const orderedText = () => {
  // base-ui Dialog 走 Portal 挂到 document.body，不能从 container 查
  const ol = document.body.querySelector('ol')
  expect(ol).not.toBeNull()
  return ol!.textContent ?? ''
}

beforeEach(() => {
  subscribe.mockClear()
  unsubscribe.mockClear()
  server.use(
    http.get('/api/agents', () => HttpResponse.json({ agents: [] })),
    http.get('/api/tasks/t-page/messages', ({ request }) => {
      const sp = new URL(request.url).searchParams
      expect(sp.get('order')).toBe('desc')
      if (!sp.get('before_seq')) {
        // 首页：最新 3 条（wire 新→旧）+ 更早游标
        return HttpResponse.json({ messages: [msg(5), msg(4), msg(3)], next_before_seq: 2 })
      }
      expect(sp.get('before_seq')).toBe('2')
      // 末页（不满页）：省略游标 → 耗尽
      return HttpResponse.json({ messages: [msg(2), msg(1)] })
    }),
  )
})

describe('TaskTranscriptDrawer', () => {
  it('subscribes to the task scope on mount (fixes dead replay, FP-12 P2-1)', async () => {
    const { unmount } = renderDrawer()
    await waitFor(() => expect(subscribe).toHaveBeenCalledWith('task', 't-page'))
    unmount()
    // 卸载时经 subscribe 返回的清理函数退订
    expect(unsubscribe).toHaveBeenCalledTimes(1)
  })

  it('renders the newest page ascending and pages older history upward (FP-05 P1-1)', async () => {
    const user = userEvent.setup()
    renderDrawer()
    // 首页即最新 3 条，升序渲染
    await screen.findByText('msg-5')
    let text = orderedText()
    expect(text.indexOf('msg-3')).toBeLessThan(text.indexOf('msg-4'))
    expect(text.indexOf('msg-4')).toBeLessThan(text.indexOf('msg-5'))
    // 向上翻更早历史
    await user.click(screen.getByRole('button', { name: 'Load more' }))
    await screen.findByText('msg-1')
    text = orderedText()
    expect(text.indexOf('msg-1')).toBeLessThan(text.indexOf('msg-2'))
    expect(text.indexOf('msg-2')).toBeLessThan(text.indexOf('msg-3'))
    expect(text.indexOf('msg-3')).toBeLessThan(text.indexOf('msg-5'))
    // 末页无游标 → “加载更多”消失
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull(),
    )
  })
})

// wave3 P1（FP-12 P2-5）：直聊任务审批入口——抽屉是全站唯一可达的转写/操作面。
describe('TaskTranscriptDrawer approval actions', () => {
  it('renders approve/deny for awaiting_input, hits the approve endpoint and refreshes via the invalidation chain', async () => {
    const approve = vi.fn()
    const deny = vi.fn()
    server.use(
      http.post('/api/tasks/t-page/approve', () => {
        approve()
        return HttpResponse.json({ result: 'approved' })
      }),
      http.post('/api/tasks/t-page/deny', () => {
        deny()
        return HttpResponse.json({ result: 'denied' })
      }),
    )
    const { invalidateSpy } = renderDrawer(AWAITING_TASK)
    const user = userEvent.setup()
    // 转写照常渲染，操作区带“等待审批”提示
    expect(await screen.findByText('msg-5')).toBeInTheDocument()
    expect(screen.getByText(i18n.t('taskMsg.awaitingDecision', { lng: 'en' }))).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(approve).toHaveBeenCalledTimes(1))
    expect(deny).not.toHaveBeenCalled()
    // 失效链覆盖：列表 + 本任务（消息 paged 键同 ['task', id] 前缀）都刷新
    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['workspace-tasks'] })
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['task', 't-page'] })
    })
    // 审批成功后操作区隐藏（即使快照仍停在 awaiting_input）
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull(),
    )
  })

  it('hides the approval actions unless the task is awaiting_input', async () => {
    renderDrawer() // status: running
    await screen.findByText('msg-5')
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Reject' })).toBeNull()
    expect(
      screen.queryByText(i18n.t('taskMsg.awaitingDecision', { lng: 'en' })),
    ).toBeNull()
  })

  it('shows the i18n failure message and keeps buttons retryable when the settle call fails', async () => {
    server.use(
      http.post('/api/tasks/t-page/approve', () =>
        HttpResponse.json({ error: 'E_CONFLICT', message: 'task not awaiting' }, { status: 409 }),
      ),
    )
    renderDrawer(AWAITING_TASK)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Approve' }))
    expect(
      await screen.findByText(i18n.t('taskMsg.settleFailed', { lng: 'en' })),
    ).toBeInTheDocument()
    // 未误判成功：按钮仍可重试
    expect(screen.getByRole('button', { name: 'Approve' })).toBeEnabled()
  })
})

describe('taskMsg approval i18n keys (wave3 P1)', () => {
  const KEYS = [
    'taskMsg.approve',
    'taskMsg.reject',
    'taskMsg.awaitingDecision',
    'taskMsg.settleFailed',
  ] as const
  it.each(KEYS)('%s exists in both locales', (key) => {
    // i18n.t 缺键时回退返回键名本身——不等于键名即存在
    expect(i18n.t(key, { lng: 'en' })).not.toBe(key)
    expect(i18n.t(key, { lng: 'zh' })).not.toBe(key)
  })
})

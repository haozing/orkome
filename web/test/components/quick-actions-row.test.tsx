import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { QuickActionsRow } from '@/components/work-item/quick-actions-row'
import type { QuickAction } from '@/api/quick-actions'
import { useWorkspaceStore } from '@/stores/workspace'
import { server } from '../msw'

// 快捷动作行（W1-2）组件测试：直接测组件（无路由桩）。
// 覆盖六点：①agent 负责人渲染 chips ②点击 chip → run POST + 切执行 tab
// ③409 ActiveDuplicate 带 active_task_id → 冲突提示 + 直达在途任务
// ④管理 Dialog 新建 → POST body ⑤达上限 3 新建禁用 ⑥非 agent 负责人整行不渲染。

const patchSearch = vi.fn()

const QA = (over: Partial<QuickAction>): QuickAction => ({
  id: 'qa-x',
  workspace_id: 'ws-1',
  user_id: 'u-1',
  label: 'label',
  prompt: 'prompt',
  is_private: true,
  created_at: '2026-10-18T00:00:00Z',
  ...over,
})

// 可变存储：GET 反映 POST/DELETE（模拟服务端真值，供 invalidation 后 refetch 断言）。
let store: QuickAction[] = []

function renderRow(assigneeType = 'agent') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <QuickActionsRow workItemId="wi-1" assigneeType={assigneeType} patchSearch={patchSearch} />
    </QueryClientProvider>,
  )
}

function setWorkspace(id: string | null) {
  useWorkspaceStore.setState({ currentId: id })
}

beforeEach(() => {
  setWorkspace('ws-1')
  patchSearch.mockReset()
  store = [
    QA({ id: 'qa-1', label: 'Inspect daily', prompt: 'Inspect all services and summarize' }),
    QA({ id: 'qa-2', label: 'Draft weekly', prompt: 'Draft the weekly report' }),
  ]
  server.use(
    http.get('/api/quick-actions', () => HttpResponse.json({ quick_actions: store })),
  )
})
afterEach(() => {
  setWorkspace(null)
  server.resetHandlers()
})

describe('quick-actions-row', () => {
  it('renders saved actions as chips when assignee is an agent', async () => {
    renderRow('agent')
    expect(await screen.findByText('Inspect daily')).toBeInTheDocument()
    expect(screen.getByText('Draft weekly')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Manage' })).toBeInTheDocument()
    expect(screen.getByTestId('quick-actions-row')).toBeInTheDocument()
  })

  it('clicking a chip runs POST and switches to the tasks tab', async () => {
    let runHit = ''
    server.use(
      http.post('/api/work-items/wi-1/quick-actions/qa-1/run', ({ request }) => {
        runHit = new URL(request.url).pathname
        return HttpResponse.json({ task_id: 'task-9' }, { status: 201 })
      }),
    )
    renderRow('agent')
    fireEvent.click(await screen.findByText('Inspect daily'))
    await screen.findByText('Task dispatched')
    expect(runHit).toBe('/api/work-items/wi-1/quick-actions/qa-1/run')
    await waitFor(() => expect(patchSearch).toHaveBeenCalledWith({ tab: 'tasks' }))
  })

  it('409 ActiveDuplicate shows conflict hint and links to the active task', async () => {
    // 真实 wire：webx.ErrorBody.Code 序列化为扁平字符串信封
    // {error:"E_CODE",message,details}（respond.go:14），客户端
    // `typeof body?.error === 'string'` 命中 → ApiError.details 保留
    // ——主路径 activeTaskIdFromError 直接命中，无需兜底探针。
    let activeTaskHit = false
    server.use(
      http.post('/api/work-items/wi-1/quick-actions/qa-2/run', () =>
        HttpResponse.json(
          {
            error: 'E_ACTIVE_DUPLICATE',
            message: 'work item already has an active task for this agent (already_active)',
            details: [
              { field: 'active_task_id', problem: 'task-77' },
              { field: 'reason', problem: 'already_active' },
            ],
          },
          { status: 409 },
        ),
      ),
      http.get('/api/work-items/wi-1/active-task', () => {
        activeTaskHit = true
        return HttpResponse.json({ task: { id: 'task-77' } })
      }),
    )
    renderRow('agent')
    fireEvent.click(await screen.findByText('Draft weekly'))
    expect(await screen.findByText('This work item already has an active task')).toBeInTheDocument()
    // 主路径从 Details 拿到 id：按钮出现且不触发兜底探针。
    expect(activeTaskHit).toBe(false)
    fireEvent.click(await screen.findByRole('button', { name: 'View active task' }))
    await waitFor(() =>
      expect(patchSearch).toHaveBeenCalledWith({ tab: 'tasks', task: 'task-77' }),
    )
  })

  it('manage dialog: creating a quick action posts {label,prompt} and refetches', async () => {
    let createdBody: { label?: string; prompt?: string } | undefined
    server.use(
      http.post('/api/quick-actions', async ({ request }) => {
        createdBody = (await request.json()) as { label: string; prompt: string }
        const row = QA({
          id: 'qa-new',
          label: createdBody.label ?? '',
          prompt: createdBody.prompt ?? '',
        })
        store = [...store, row]
        return HttpResponse.json(row, { status: 201 })
      }),
    )
    renderRow('agent')
    fireEvent.click(await screen.findByRole('button', { name: 'Manage' }))
    expect(await screen.findByTestId('qa-manage-dialog')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Triage inbox' } })
    fireEvent.change(screen.getByLabelText('Prompt'), {
      target: { value: 'Triage the inbox and flag urgent items' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(createdBody).toEqual({
        label: 'Triage inbox',
        prompt: 'Triage the inbox and flag urgent items',
      }),
    )
    // invalidation refetch → 新数据同时上行到 chip 与 Dialog 列表
    //（Dialog 打开时背景 aria-hidden，role 查询不可达，用文本断言 refetch 生效）
    await waitFor(() => expect(screen.getAllByText('Triage inbox').length).toBeGreaterThan(0))
  })

  it('at the 3-action cap the create button is disabled with a hint', async () => {
    store = [
      QA({ id: 'qa-1', label: 'One', prompt: 'p1' }),
      QA({ id: 'qa-2', label: 'Two', prompt: 'p2' }),
      QA({ id: 'qa-3', label: 'Three', prompt: 'p3' }),
    ]
    renderRow('agent')
    fireEvent.click(await screen.findByRole('button', { name: 'Manage' }))
    expect(await screen.findByText('Up to 3 actions')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  })

  it('renders nothing when the assignee is not an agent', () => {
    renderRow('user')
    expect(screen.queryByTestId('quick-actions-row')).toBeNull()
    expect(screen.queryByText('Quick actions')).toBeNull()
  })
})

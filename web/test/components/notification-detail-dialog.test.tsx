import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { NotificationItem } from '@/api/notification'
import { NotificationDetailDialog } from '@/components/notification-detail-dialog'
import { server } from '../msw'

// 弹窗用 useNavigate 做跳工单导航；裸渲染无 RouterProvider 上下文会抛错，
// 桩成 no-op——导航行为不属于本测试断言面（同 access-page.test.tsx 惯例）。
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => () => {} }
})

// W1-1 通知内联裁决：approval_request 通知带 task_id 时弹窗 footer 直接出
// 批准/拒绝按钮，成功进终态、失败出 role=alert。setup.ts 固定 i18n=en。
const ITEM: NotificationItem = {
  id: 'n-1',
  type: 'approval_request',
  title: 'needs your approval',
  body: 'Agent wants to deploy to production',
  work_item_id: 'wi-1',
  task_id: 't-1',
  read_at: '2026-10-18T08:00:00Z',
  created_at: '2026-10-18T08:00:00Z',
}

function renderDialog(item: NotificationItem | null = ITEM) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onOpenChange = vi.fn()
  render(
    <QueryClientProvider client={qc}>
      <NotificationDetailDialog item={item} onOpenChange={onOpenChange} />
    </QueryClientProvider>,
  )
  return { onOpenChange }
}

describe('notification detail dialog inline settle (W1-1)', () => {
  it('shows approve/reject for approval_request notifications carrying a task_id', () => {
    renderDialog()
    expect(screen.getByRole('button', { name: 'Approve' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reject' })).toBeInTheDocument()
    // 既有按钮不动（base-ui DialogContent 自带 sr-only "Close" 的 X，同名取全组）
    expect(screen.getByRole('button', { name: 'View work item' })).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: 'Close' }).length).toBeGreaterThan(0)
  })

  it('no inline actions when task_id is absent (legacy rows)', () => {
    renderDialog({ ...ITEM, task_id: undefined })
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Reject' })).toBeNull()
  })

  it('approve posts to the task settle endpoint and enters the terminal state', async () => {
    let settlePath = ''
    server.use(
      http.post('/api/tasks/:taskId/:action', ({ params }) => {
        settlePath = `/api/tasks/${params.taskId}/${params.action}`
        return HttpResponse.json(null)
      }),
    )
    const user = userEvent.setup()
    renderDialog()
    await user.click(screen.getByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(settlePath).toBe('/api/tasks/t-1/approve'))
    // 终态：按钮区替换为已批准文案
    expect(await screen.findByTestId('settle-outcome')).toHaveTextContent('Approved')
    expect(screen.queryByRole('button', { name: 'Approve' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Reject' })).toBeNull()
  })

  it('reject failure surfaces the server message via role=alert and re-enables buttons', async () => {
    server.use(
      http.post('/api/tasks/t-1/deny', () =>
        HttpResponse.json({ error: 'E_CONFLICT', message: 'task not awaiting' }, { status: 409 }),
      ),
    )
    const user = userEvent.setup()
    renderDialog()
    await user.click(screen.getByRole('button', { name: 'Reject' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('task not awaiting')
    // 失败不进终态：按钮恢复可点
    expect(screen.getByRole('button', { name: 'Reject' })).toBeEnabled()
    expect(screen.queryByTestId('settle-outcome')).toBeNull()
  })
})

import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { InfiniteData } from '@tanstack/react-query'
import {
  cancelWorkItemTask,
  listTaskMessages,
  listTaskMessagesPage,
  listWorkItemTasks,
  settleTask,
  taskMessagesAscending,
  taskMessagesInfiniteQuery,
  type TaskMessage,
  type TaskMessagePage,
} from '@/api/orchestration'
import {
  createComment,
  deleteWorkItem,
  getWorkItem,
  listComments,
  listStatuses,
  listTimeline,
  updateWorkItem,
} from '@/api/work'
import { listMembers } from '@/api/identity'
import { server } from '../msw'

// 后端真实 wire 形状：GET /api/tasks/{id}/messages 恒按 seq DESC 返回
// （order=desc 时新→旧），前端归一化为升序消费。
const tmsg = (seq: number): TaskMessage => ({
  id: `m-${seq}`,
  task_id: 't-1',
  seq,
  kind: 'text',
  content: `msg-${seq}`,
  truncated: false,
  created_at: '2026-10-02T00:00:00Z',
})
const pageOf = (seqsAscending: number[], nextBeforeSeq?: number): TaskMessagePage => ({
  messages: seqsAscending.map(tmsg),
  next_before_seq: nextBeforeSeq ?? null,
})
describe('orchestration API', () => {
  it('listWorkItemTasks: normalizes {tasks} (null → empty array)', async () => {
    server.use(http.get('/api/work-items/wi-1/tasks', () => HttpResponse.json({ tasks: null })))
    await expect(listWorkItemTasks('wi-1')).resolves.toEqual([])
  })
  it('listTaskMessages: order=desc on the wire, normalized to seq-ascending (FP-05 P1-1)', async () => {
    server.use(
      http.get('/api/tasks/t-1/messages', ({ request }) => {
        const sp = new URL(request.url).searchParams
        expect(sp.get('limit')).toBe('200')
        expect(sp.get('order')).toBe('desc')
        // 后端 desc：新→旧（真实长任务只回最新 200 条，尾段可见）
        return HttpResponse.json({ messages: [tmsg(3), tmsg(2), tmsg(1)] })
      }),
    )
    const msgs = await listTaskMessages('t-1')
    expect(msgs.map((m) => m.seq)).toEqual([1, 2, 3])
  })
  it('listTaskMessagesPage: before_seq cursor + next_before_seq surfaced, page reversed to ascending', async () => {
    server.use(
      http.get('/api/tasks/t-1/messages', ({ request }) => {
        const sp = new URL(request.url).searchParams
        expect(sp.get('order')).toBe('desc')
        expect(sp.get('before_seq')).toBe('21')
        // 更早一页（seq < 21），仍按 desc：新→旧
        return HttpResponse.json({
          messages: [tmsg(20), tmsg(19)],
          next_before_seq: 19,
        })
      }),
    )
    const page = await listTaskMessagesPage('t-1', { beforeSeq: 21 })
    expect(page.messages.map((m) => m.seq)).toEqual([19, 20])
    expect(page.next_before_seq).toBe(19)
  })
  it('listTaskMessagesPage: first page sends no before_seq; missing cursor → null', async () => {
    server.use(
      http.get('/api/tasks/t-1/messages', ({ request }) => {
        expect(new URL(request.url).searchParams.has('before_seq')).toBe(false)
        // 末页（不满页）：后端省略 next_before_seq → 历史耗尽
        return HttpResponse.json({ messages: [tmsg(2), tmsg(1)] })
      }),
    )
    const page = await listTaskMessagesPage('t-1')
    expect(page.next_before_seq).toBeNull()
  })
  it('taskMessagesInfiniteQuery: initialPageParam 0, cursor ← next_before_seq, missing → exhausted', () => {
    const opts = taskMessagesInfiniteQuery('t-1')
    expect(opts.initialPageParam).toBe(0)
    expect(
      opts.getNextPageParam?.(pageOf([220, 201], 21), [pageOf([220, 201], 21)], 0, [0]),
    ).toBe(21)
    expect(
      opts.getNextPageParam?.(pageOf([20, 1]), [pageOf([220, 201], 21), pageOf([20, 1])], 21, [
        0, 21,
      ]),
    ).toBeUndefined()
  })
  it('taskMessagesAscending: newest-first pages splice into one globally ascending list', () => {
    // data.pages 存的是归一化后的页（页内升序）：首页 seq 3..5，更早页 seq 1..2
    const data = {
      pages: [pageOf([3, 4, 5], 2), pageOf([1, 2])],
      pageParams: [0, 2],
    } as InfiniteData<TaskMessagePage, number>
    expect(taskMessagesAscending(data).map((m) => m.seq)).toEqual([1, 2, 3, 4, 5])
    expect(taskMessagesAscending(undefined)).toEqual([])
  })
  it('settleTask: approve/deny routing and note payload', async () => {
    server.use(
      http.post('/api/tasks/t-1/approve', async ({ request }) => {
        expect(await request.json()).toEqual({ note: 'ok' })
        return HttpResponse.json({})
      }),
    )
    await expect(settleTask('t-1', true, 'ok')).resolves.toBeDefined()
  })
  it('cancelWorkItemTask: path assembly', async () => {
    server.use(
      http.post(
        '/api/work-items/wi-1/tasks/t-1/cancel',
        () => new HttpResponse(null, { status: 204 }),
      ),
    )
    await expect(cancelWorkItemTask('wi-1', 't-1')).resolves.toBeUndefined()
  })
})
describe('work detail API', () => {
  it('getWorkItem: returns detail', async () => {
    server.use(
      http.get('/api/work-items/wi-1', () =>
        HttpResponse.json({ id: 'wi-1', number: 1, title: 'T', status: 'todo' }),
      ),
    )
    const wi = await getWorkItem('wi-1')
    expect(wi.status).toBe('todo')
  })
  it('listComments / createComment / listTimeline: shape and normalization', async () => {
    server.use(
      http.get('/api/work-items/wi-1/comments', () => HttpResponse.json({ items: null })),
      http.post('/api/work-items/wi-1/comments', async ({ request }) => {
        expect(await request.json()).toEqual({ content: 'hello', parent_id: '' })
        return HttpResponse.json(
          { comment: { id: 'c-1', content: 'hello' }, admission: { queued: true } },
          { status: 201 },
        )
      }),
      http.get('/api/work-items/wi-1/timeline', ({ request }) => {
        expect(new URL(request.url).searchParams.get('limit')).toBe('50')
        return HttpResponse.json({
          items: [{ kind: 'activity', id: 'a-1', created_at: '2026-10-02T00:00:00Z' }],
        })
      }),
    )
    await expect(listComments('wi-1')).resolves.toEqual([])
    const res = await createComment('wi-1', 'hello')
    expect(res.comment.id).toBe('c-1')
    const tl = await listTimeline('wi-1')
    expect(tl).toHaveLength(1)
  })
})
describe('detail page API (update/delete/statuses/members)', () => {
  it('updateWorkItem: submits patch fields + expected_revision, returns updated entity', async () => {
    server.use(
      http.put('/api/work-items/wi-1', async ({ request }) => {
        expect(await request.json()).toEqual({ status: 'done', priority: 3, expected_revision: 5 })
        return HttpResponse.json({ id: 'wi-1', status: 'done', priority: 3, revision: 6 })
      }),
    )
    const updated = await updateWorkItem('wi-1', { status: 'done', priority: 3 }, 5)
    expect(updated.revision).toBe(6)
  })
  it('deleteWorkItem：204 → undefined', async () => {
    server.use(http.delete('/api/work-items/wi-1', () => new HttpResponse(null, { status: 204 })))
    await expect(deleteWorkItem('wi-1')).resolves.toBeUndefined()
  })
  it('listStatuses: normalizes catalog (null → empty array)', async () => {
    server.use(http.get('/api/work-item-statuses', () => HttpResponse.json(null)))
    await expect(listStatuses()).resolves.toEqual([])
  })
  it('listMembers: path params + app.Page envelope unwrap (batch1 root cause A)', async () => {
    // 真实 wire 形状是 ploykit app.Page 信封 {items, total}——按裸数组 mock
    // 等于按错误契约钉死测试（batch1 §四.1 模式）。抓样改信封 + 断言解包。
    server.use(
      http.get('/api/workspaces/ws-9/members', ({ request }) => {
        expect(new URL(request.url).pathname).toBe('/api/workspaces/ws-9/members')
        return HttpResponse.json({
          items: [
            {
              user_id: 'u-1',
              role: 'owner',
              email: 'a@b.c',
              display_name: 'Alice Zhang',
              created_at: '2026-10-02T00:00:00Z',
            },
          ],
          total: 1,
        })
      }),
    )
    const members = await listMembers('ws-9')
    expect(members).toHaveLength(1)
    expect(members[0]!.display_name).toBe('Alice Zhang')
  })
})
describe('agents catalog API', () => {
  it('listAgents: normalizes {agents} (null → empty array)', async () => {
    server.use(http.get('/api/agents', () => HttpResponse.json({ agents: null })))
    const { listAgents } = await import('@/api/orchestration')
    await expect(listAgents()).resolves.toEqual([])
  })
  it('listAgents: passes through normal list', async () => {
    server.use(
      http.get('/api/agents', () =>
        HttpResponse.json({
          agents: [{ id: 'a-1', name: 'writer', archived: false }],
        }),
      ),
    )
    const { listAgents } = await import('@/api/orchestration')
    const agents = await listAgents()
    expect(agents[0]!.name).toBe('writer')
  })
})

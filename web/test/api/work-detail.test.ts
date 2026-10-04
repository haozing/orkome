import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import {
  cancelWorkItemTask,
  listTaskMessages,
  listWorkItemTasks,
  settleTask,
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
describe('orchestration API', () => {
  it('listWorkItemTasks: normalizes {tasks} (null → empty array)', async () => {
    server.use(http.get('/api/work-items/wi-1/tasks', () => HttpResponse.json({ tasks: null })))
    await expect(listWorkItemTasks('wi-1')).resolves.toEqual([])
  })
  it('listTaskMessages: normalizes messages + passes limit', async () => {
    server.use(
      http.get('/api/tasks/t-1/messages', ({ request }) => {
        expect(new URL(request.url).searchParams.get('limit')).toBe('200')
        return HttpResponse.json({
          messages: [
            {
              id: 'm-1',
              task_id: 't-1',
              seq: 1,
              kind: 'text',
              content: 'hi',
              truncated: false,
              created_at: '2026-10-02T00:00:00Z',
            },
          ],
        })
      }),
    )
    const msgs = await listTaskMessages('t-1')
    expect(msgs).toHaveLength(1)
    expect(msgs[0]!.kind).toBe('text')
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
  it('listMembers: path params + normalization', async () => {
    server.use(
      http.get('/api/workspaces/ws-9/members', () =>
        HttpResponse.json([
          {
            id: 'm-1',
            workspace_id: 'ws-9',
            user_id: 'u-1',
            role: 'owner',
            email: 'a@b.c',
            display_name: 'Alice Zhang',
            created_at: '2026-10-02T00:00:00Z',
          },
        ]),
      ),
    )
    const members = await listMembers('ws-9')
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

import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { listWorkItems, quickCreateWorkItem, workItemsInfiniteQuery } from '@/api/work'
import { server } from '../msw'
const ITEM = {
  id: 'wi-1',
  workspace_id: 'ws-1',
  number: 12,
  title: 'Sample',
  description: '',
  status: 'todo',
  status_category: 'unstarted',
  priority: 0,
  assignee_type: 'none',
  creator_type: 'user',
  creator_id: 'u-1',
  revision: 1,
  created_at: '2026-10-02T00:00:00Z',
  updated_at: '2026-10-02T00:00:00Z',
  last_activity_at: '2026-10-02T00:00:00Z',
}
describe('work API', () => {
  it('listWorkItems: cursor params + normalizes null items to empty array', async () => {
    server.use(
      http.get('/api/work-items', ({ request }) => {
        expect(new URL(request.url).searchParams.get('limit')).toBe('30')
        expect(new URL(request.url).searchParams.get('cursor')).toBe('c1')
        return HttpResponse.json({ items: null, next_cursor: null })
      }),
    )
    const page = await listWorkItems({ limit: 30, cursor: 'c1' })
    expect(page.items).toEqual([])
    expect(page.next_cursor).toBeNull()
  })
  it('listWorkItems: no query string without params', async () => {
    let gotUrl = ''
    server.use(
      http.get('/api/work-items', ({ request }) => {
        gotUrl = request.url
        return HttpResponse.json({ items: [ITEM], next_cursor: 'next-1' })
      }),
    )
    const page = await listWorkItems()
    expect(gotUrl).not.toContain('?')
    expect(page.items).toHaveLength(1)
    expect(page.next_cursor).toBe('next-1')
  })
  it('quickCreateWorkItem: POST quick create, returns work item', async () => {
    server.use(
      http.post('/api/work-items/quick-create', async ({ request }) => {
        expect(await request.json()).toEqual({ title: 'New item' })
        return HttpResponse.json({ ...ITEM, title: 'New item' }, { status: 201 })
      }),
    )
    const created = await quickCreateWorkItem({ title: 'New item' })
    expect(created.number).toBe(12)
  })
  it('workItemsInfiniteQuery: pagination param mapping (first page empty cursor → last page undefined)', async () => {
    const opts = workItemsInfiniteQuery()
    const page = (next_cursor: string | null) =>
      ({ items: [], next_cursor }) as Awaited<ReturnType<typeof listWorkItems>>
    expect(opts.getNextPageParam?.(page('page-2'), [page('page-2')], '', [''])).toBe('page-2')
    expect(opts.getNextPageParam?.(page(null), [page(null)], 'page-2', [''])).toBeUndefined()
    expect(opts.initialPageParam).toBe('')
  })
})

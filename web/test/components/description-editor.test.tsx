import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { DescriptionEditor } from '@/components/work-item/description-editor'
import { ApiError } from '@/lib/api'
import type { WorkItem } from '@/api/work'
const mocks = vi.hoisted(() => ({
  updateWorkItem: vi.fn(),
  getWorkItem: vi.fn(),
}))
vi.mock('@/api/work', () => ({
  updateWorkItem: (...args: unknown[]) => mocks.updateWorkItem(...args),
  getWorkItem: (...args: unknown[]) => mocks.getWorkItem(...args),
}))
vi.mock('@/components/editor/rich-editor', () => ({
  RichEditor: (props: {
    content: string
    onChange: (v: string) => void
    onBlur?: () => void
    ariaLabel?: string
  }) => (
    <textarea
      aria-label={props.ariaLabel}
      value={props.content}
      onChange={(e) => props.onChange(e.target.value)}
      onBlur={props.onBlur}
    />
  ),
}))
const ITEM = (over: Partial<WorkItem> = {}): WorkItem => ({
  id: 'wi-1',
  workspace_id: 'ws-1',
  number: 1,
  title: 'T',
  description: 'original description',
  status: 'todo',
  status_category: 'unstarted',
  priority: 0,
  assignee_type: 'none',
  creator_type: 'user',
  creator_id: 'u-1',
  revision: 7,
  created_at: '2026-10-03T00:00:00Z',
  updated_at: '2026-10-03T00:00:00Z',
  last_activity_at: '2026-10-03T00:00:00Z',
  ...over,
})
function renderEditor(item: WorkItem) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['work-item', item.id], item)
  return render(
    <QueryClientProvider client={qc}>
      <DescriptionEditor item={item} />
    </QueryClientProvider>,
  )
}
const LONG = 6000
const conflictError = () => new ApiError(409, 'E_REVISION_CONFLICT', 'revision conflict')
async function editTo(text: string) {
  fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
  const ta = await screen.findByRole('textbox', { name: 'Description editor' })
  fireEvent.change(ta, { target: { value: text } })
}
beforeEach(() => {
  vi.clearAllMocks()
})
describe('DescriptionEditor conflict flow', () => {
  it(
    '409 on save: keep draft, refetch server version, show side-by-side dialog',
    async () => {
      renderEditor(ITEM())
      await editTo('my new description')
      mocks.updateWorkItem.mockRejectedValueOnce(conflictError())
      mocks.getWorkItem.mockResolvedValueOnce(
        ITEM({ description: 'someone else version', revision: 9 }),
      )
      await waitFor(
        () =>
          expect(mocks.updateWorkItem).toHaveBeenCalledWith(
            'wi-1',
            { description: 'my new description' },
            7,
          ),
        { timeout: LONG },
      )
      await waitFor(() => expect(screen.getByRole('alertdialog')).toBeInTheDocument(), {
        timeout: LONG,
      })
      const dialog = screen.getByRole('alertdialog')
      expect(dialog).toHaveTextContent('someone else version')
      expect(dialog).toHaveTextContent('my new description')
    },
    LONG * 2,
  )
  it(
    'conflict dialog Keep mine: re-save with refetched revision and close',
    async () => {
      renderEditor(ITEM())
      await editTo('my new description')
      mocks.updateWorkItem.mockRejectedValueOnce(conflictError())
      mocks.getWorkItem.mockResolvedValueOnce(
        ITEM({ description: 'someone else version', revision: 9 }),
      )
      await waitFor(() => expect(screen.getByRole('alertdialog')).toBeInTheDocument(), {
        timeout: LONG,
      })
      mocks.updateWorkItem.mockResolvedValueOnce(
        ITEM({ description: 'my new description', revision: 10 }),
      )
      fireEvent.click(screen.getByRole('button', { name: 'Keep mine' }))
      await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument(), {
        timeout: LONG,
      })
      expect(mocks.updateWorkItem).toHaveBeenLastCalledWith(
        'wi-1',
        { description: 'my new description' },
        9,
      )
    },
    LONG * 2,
  )
  it(
    'conflict dialog Use server version: drop draft, keep server content',
    async () => {
      renderEditor(ITEM())
      await editTo('my new description')
      mocks.updateWorkItem.mockRejectedValueOnce(conflictError())
      mocks.getWorkItem.mockResolvedValueOnce(
        ITEM({ description: 'someone else version', revision: 9 }),
      )
      await waitFor(() => expect(screen.getByRole('alertdialog')).toBeInTheDocument(), {
        timeout: LONG,
      })
      fireEvent.click(screen.getByRole('button', { name: 'Use server version' }))
      await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument(), {
        timeout: LONG,
      })
      expect(mocks.updateWorkItem).toHaveBeenCalledTimes(1)
    },
    LONG * 2,
  )
})

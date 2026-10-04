import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { RouteErrorComponent } from '@/components/route-error'
const mockRouter = vi.hoisted(() => ({
  invalidate: vi.fn(),
  history: { back: vi.fn() },
}))
vi.mock('@tanstack/react-router', () => ({
  useRouter: () => mockRouter,
  Link: (props: React.ComponentProps<'a'>) => <a {...props} />,
}))
describe('RouteErrorComponent', () => {
  it('shows error message; retry calls reset; back calls history.back', async () => {
    const user = userEvent.setup()
    const reset = vi.fn()
    render(<RouteErrorComponent error={new Error('db connection failed')} reset={reset} />)
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('Something went wrong')
    expect(alert).toHaveTextContent('db connection failed')
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(reset).toHaveBeenCalledTimes(1)
    await user.click(screen.getByRole('button', { name: 'Go back' }))
    expect(mockRouter.history.back).toHaveBeenCalledTimes(1)
  })
  it('shows fallback text when error has no message', () => {
    render(<RouteErrorComponent error={new Error()} reset={vi.fn()} />)
    expect(screen.getByRole('alert')).toHaveTextContent('Unknown error')
  })
})

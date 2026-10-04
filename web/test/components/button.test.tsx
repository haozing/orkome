import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Button } from '@/components/ui/button'
describe('Button (Base UI + Tailwind variants)', () => {
  it('renders children and passes disabled', () => {
    render(<Button disabled>Save</Button>)
    const button = screen.getByRole('button', { name: 'Save' })
    expect(button).toBeDisabled()
  })
  it('applies variant classes', () => {
    render(
      <Button variant="danger" size="sm">
        Delete
      </Button>,
    )
    const button = screen.getByRole('button', { name: 'Delete' })
    expect(button.className).toContain('bg-red-600')
    expect(button.className).toContain('h-7')
  })
})

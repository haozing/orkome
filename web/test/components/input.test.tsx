import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Input } from '@/components/ui/input'
describe('Input (themed input)', () => {
  it('passes native props and merges className', () => {
    render(<Input id="x" placeholder="Email" disabled className="w-24" />)
    const input = screen.getByPlaceholderText('Email')
    expect(input).toBeDisabled()
    expect(input.className).toContain('h-8')
    expect(input.className).toContain('w-24')
  })
})

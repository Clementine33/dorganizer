import { describe, expect, it } from 'vitest'
import { cn } from '../utils'

describe('cn', () => {
  it('joins static class names', () => {
    expect(cn('text-sm', 'font-medium')).toBe('text-sm font-medium')
  })

  it('drops falsy values', () => {
    expect(cn('px-2', undefined, null, false, 'py-1', '')).toBe('px-2 py-1')
  })

  it('resolves tailwind-merge conflicts in favor of the later class', () => {
    expect(cn('px-2', 'px-4')).toBe('px-4')
  })
})
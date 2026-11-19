import { act, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'

import { isolatedPreviewDocument } from '../display'
import { DiagnosticPreview } from '../preview'

afterEach(() => vi.useRealTimers())

test('artwork mounts automatically with only script permission and no preview controls', () => {
  render(
    <DiagnosticPreview source='<svg></svg><script>window.example = 1</script>' />
  )
  const frame = screen.getByTitle('Artwork preview')
  expect(screen.queryByRole('button')).not.toBeInTheDocument()
  expect(frame).toHaveAttribute('sandbox', 'allow-scripts')
  expect(frame).toHaveAttribute('credentialless', '')
  expect(frame).toHaveAttribute('referrerpolicy', 'no-referrer')
  expect(frame.getAttribute('srcdoc')).toMatch(
    /^<meta http-equiv="Content-Security-Policy"/
  )
  expect(frame.getAttribute('srcdoc')).toContain("connect-src 'none'")
  expect(frame.getAttribute('srcdoc')).toContain(
    '<script>window.example = 1</script>'
  )
})

test('changing source replaces the browsing context, while an unchanged source preserves it', () => {
  const view = render(<DiagnosticPreview source='<svg></svg>' />)
  const frame = screen.getByTitle('Artwork preview')
  view.rerender(<DiagnosticPreview source='<svg></svg>' />)
  expect(screen.getByTitle('Artwork preview')).toBe(frame)
  view.rerender(<DiagnosticPreview source='<svg><circle r="3"/></svg>' />)
  expect(frame).not.toBeInTheDocument()
  expect(screen.getByTitle('Artwork preview')).not.toBe(frame)
  expect(screen.getByTitle('Artwork preview')).toHaveAttribute(
    'srcdoc',
    isolatedPreviewDocument('<svg><circle r="3"/></svg>')
  )
  view.unmount()
  expect(screen.queryByTitle('Artwork preview')).not.toBeInTheDocument()
})

test('preview stays mounted beyond the former 15-second limit', () => {
  vi.useFakeTimers()
  render(<DiagnosticPreview source='<svg></svg>' />)
  const frame = screen.getByTitle('Artwork preview')
  act(() => vi.advanceTimersByTime(60_000))
  expect(screen.getByTitle('Artwork preview')).toBe(frame)
})

test('preview policy precedes an untouched full document', () => {
  const source =
    '<meta http-equiv="refresh" content="0;url=https://example.com"><a href="https://example.com">link</a><svg viewBox="0 0 10 10"><circle r="3"/></svg><script>requestAnimationFrame(() => {})</script>'
  const isolated = isolatedPreviewDocument(source)
  expect(isolated.endsWith(source)).toBe(true)
  expect(isolated).toContain('requestAnimationFrame')
  expect(isolated).toContain('viewBox="0 0 10 10"')
})

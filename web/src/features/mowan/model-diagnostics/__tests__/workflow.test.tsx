import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { ModelDiagnostics } from '../index'
import type { DiagnosticReport } from '../types'

let client: QueryClient
const report: DiagnosticReport = {
  id: 'report-1',
  model: 'chat-model',
  group: 'standard',
  kind: 'connection',
  creative: false,
  state: 'running',
  created_at: 1_800_000_000,
  updated_at: 1_800_000_000,
  completed_at: 0,
  stale: false,
  version: 'v1',
  owned: false,
  steps: [],
}
beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'reader', role: 1 })
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
})
afterEach(() => {
  client.clear()
  useAuthStore.getState().auth.reset()
})
function mount() {
  return render(
    <QueryClientProvider client={client}>
      <ModelDiagnostics model='chat-model' />
    </QueryClientProvider>
  )
}
function serve(options: { chat?: boolean; reports?: DiagnosticReport[] } = {}) {
  return vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url.endsWith('/catalog')
        ? {
            groups: ['standard', 'premium'],
            chat_supported: options.chat ?? true,
          }
        : (options.reports ?? []),
    },
  }))
}

test('switching groups, result tabs and reports never starts a paid request', async () => {
  serve()
  const post = vi.spyOn(api, 'post')
  const user = userEvent.setup()
  mount()
  await user.click(screen.getByRole('button', { name: 'Model diagnostics' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Model diagnostics',
  })
  await user.click(await screen.findByRole('button', { name: 'premium' }))
  expect(screen.getByRole('button', { name: 'premium' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(screen.getByRole('button', { name: 'premium' })).toHaveClass(
    'max-w-full'
  )
  expect(
    screen.getByRole('navigation', { name: 'Authorized groups' })
  ).toHaveClass('overflow-x-hidden', 'overflow-y-auto')
  expect(screen.getByRole('tabpanel')).toHaveClass('overflow-y-auto')
  await user.click(screen.getByRole('tab', { name: /Drawing test/ }))
  expect(screen.getByRole('tab', { name: /Drawing test/ })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  await user.click(screen.getByRole('button', { name: 'Technical report' }))
  const technical = await screen.findByRole('dialog', {
    name: 'Technical report',
  })
  await user.click(within(technical).getByRole('button', { name: 'Close' }))
  await user.keyboard('{Escape}')
  await waitFor(() => expect(dialog).not.toBeInTheDocument())
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Model diagnostics' })
    ).toHaveFocus()
  )
  expect(post).not.toHaveBeenCalled()
})

test('only confirming actual usage creates one diagnostic with a submission key', async () => {
  serve()
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: report } })
  const user = userEvent.setup()
  mount()
  await user.click(screen.getByRole('button', { name: 'Model diagnostics' }))
  await user.click(await screen.findByRole('button', { name: 'Start test' }))
  expect(post).not.toHaveBeenCalled()
  const confirm = await screen.findByRole('alertdialog')
  await user.click(within(confirm).getByRole('button', { name: 'Start test' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(post).toHaveBeenCalledWith(
    '/api/mowan/model-diagnostics/runs',
    expect.objectContaining({
      model: 'chat-model',
      group: 'standard',
      kind: 'connection',
      creative: false,
      idempotency_key: expect.any(String),
    })
  )
})

test('closing and reopening a running report reads existing progress without posting', async () => {
  serve({ reports: [report] })
  const post = vi.spyOn(api, 'post')
  const user = userEvent.setup()
  mount()
  await user.click(screen.getByRole('button', { name: 'Model diagnostics' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Model diagnostics',
  })
  await waitFor(() =>
    expect(
      within(dialog).getByRole('button', { name: 'Running' })
    ).toBeDisabled()
  )
  await user.click(within(dialog).getByRole('button', { name: 'Close' }))
  await user.click(screen.getByRole('button', { name: 'Model diagnostics' }))
  expect(await screen.findByRole('button', { name: 'Running' })).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
})

test('non-chat models show applicability without a start control', async () => {
  serve({ chat: false })
  const post = vi.spyOn(api, 'post')
  const user = userEvent.setup()
  mount()
  await user.click(screen.getByRole('button', { name: 'Model diagnostics' }))
  expect(
    await screen.findByText('Only text models support diagnostics.')
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Start test' })
  ).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
})

test('a custom trigger replaces only the default entry and opens the same workspace', async () => {
  serve()
  const post = vi.spyOn(api, 'post')
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <ModelDiagnostics
        model='chat-model'
        trigger={
          <Button variant='outline' size='sm'>
            Open diagnostics
          </Button>
        }
      />
    </QueryClientProvider>
  )
  expect(
    screen.queryByRole('button', { name: 'Model diagnostics' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Open diagnostics' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Model diagnostics',
  })
  expect(
    await within(dialog).findByRole('button', { name: 'premium' })
  ).toBeVisible()
  expect(post).not.toHaveBeenCalled()
})

test('failed report reads show an explicit retry rather than a paid start', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url.endsWith('/catalog')) {
      return {
        data: {
          success: true,
          data: { groups: ['standard'], chat_supported: true },
        },
      }
    }
    throw new Error('Network unavailable')
  })
  const post = vi.spyOn(api, 'post')
  const user = userEvent.setup()
  mount()
  await user.click(screen.getByRole('button', { name: 'Model diagnostics' }))
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
  expect(screen.getByRole('button', { name: 'Start test' })).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
})

test('an existing drawing has no retest control while private creation remains a separate explicit action', async () => {
  serve({ reports: [{ ...report, kind: 'drawing', state: 'completed' }] })
  const post = vi.spyOn(api, 'post')
  const user = userEvent.setup()
  mount()
  await user.click(screen.getByRole('button', { name: 'Model diagnostics' }))
  await user.click(await screen.findByRole('tab', { name: /Drawing test/ }))
  expect(
    screen.queryByRole('button', { name: 'Start test' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('switch', { name: 'Free creation' }))
  expect(screen.getByText('Private creative report')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Start test' })).toBeEnabled()
  expect(post).not.toHaveBeenCalled()
})

test('existing artwork previews on drawing open, unloads on tab change and close, and reopens without a paid request', async () => {
  const drawing: DiagnosticReport = {
    ...report,
    kind: 'drawing',
    state: 'completed',
    steps: [
      {
        id: 'art',
        title: 'Artwork',
        prompt: 'Draw',
        state: 'completed',
        complete: true,
        answer: '<svg>first artwork</svg>',
      },
    ],
  }
  serve({
    reports: [
      drawing,
      {
        ...drawing,
        id: 'older-art',
        steps: [
          {
            id: 'old',
            title: 'Artwork',
            prompt: 'Draw',
            state: 'completed',
            complete: true,
            answer: '<svg>older artwork</svg>',
          },
        ],
      },
    ],
  })
  const post = vi.spyOn(api, 'post')
  const user = userEvent.setup()
  const view = mount()
  await user.click(screen.getByRole('button', { name: 'Model diagnostics' }))
  expect(screen.queryByTitle('Artwork preview')).not.toBeInTheDocument()
  await user.click(await screen.findByRole('tab', { name: /Drawing test/ }))
  const first = await screen.findByTitle('Artwork preview')
  expect(first.getAttribute('srcdoc')).toContain('first artwork')
  expect(
    screen.queryByRole('button', { name: /^(Run|Stop|Reload) preview$/ })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('tab', { name: /Connection test/ }))
  expect(first).not.toBeInTheDocument()
  expect(screen.queryByTitle('Artwork preview')).not.toBeInTheDocument()
  await user.click(screen.getByRole('tab', { name: /Drawing test/ }))
  const reopened = await screen.findByTitle('Artwork preview')
  expect(reopened).not.toBe(first)
  await user.click(screen.getByText('Report history'))
  // History entries are labelled by time, kind and verdict, not by report id.
  const history = screen.getByText('Report history').closest('details')
  const entries = within(history as HTMLElement).getAllByRole('button')
  await user.click(entries[1])
  expect(reopened).not.toBeInTheDocument()
  await waitFor(() => {
    const previews = screen.getAllByTitle('Artwork preview')
    expect(previews).toHaveLength(1)
    expect(previews[0]?.getAttribute('srcdoc')).toContain('older artwork')
  })
  await user.click(screen.getByRole('button', { name: 'Technical report' }))
  const technical = await screen.findByRole('dialog', {
    name: 'Technical report',
  })
  expect(
    within(technical).queryByTitle('Artwork preview')
  ).not.toBeInTheDocument()
  await user.click(within(technical).getByRole('button', { name: 'Close' }))
  const dialog = screen.getByRole('dialog', { name: 'Model diagnostics' })
  await user.click(within(dialog).getByRole('button', { name: 'Close' }))
  await waitFor(() =>
    expect(screen.queryByTitle('Artwork preview')).not.toBeInTheDocument()
  )
  await user.click(screen.getByRole('button', { name: 'Model diagnostics' }))
  expect(
    (await screen.findByTitle('Artwork preview')).getAttribute('srcdoc')
  ).toContain('older artwork')
  view.rerender(
    <QueryClientProvider client={client}>
      <ModelDiagnostics model='different-model' />
    </QueryClientProvider>
  )
  expect(screen.queryByTitle('Artwork preview')).not.toBeInTheDocument()
  expect(
    await screen.findByRole('tab', { name: /Connection test/ })
  ).toHaveAttribute('aria-selected', 'true')
  await user.click(await screen.findByRole('tab', { name: /Drawing test/ }))
  expect(
    (await screen.findByTitle('Artwork preview')).getAttribute('srcdoc')
  ).toContain('first artwork')
  expect(post).not.toHaveBeenCalled()
})

test('retrying an uncertain submission reuses its idempotency key', async () => {
  serve()
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(new Error('Response lost'))
    .mockResolvedValueOnce({ data: { success: true, data: report } })
  const user = userEvent.setup()
  mount()
  await user.click(screen.getByRole('button', { name: 'Model diagnostics' }))
  await user.click(await screen.findByRole('button', { name: 'Start test' }))
  const confirm = await screen.findByRole('alertdialog')
  const start = within(confirm).getByRole('button', { name: 'Start test' })
  await user.click(start)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  await waitFor(() => expect(start).toBeEnabled())
  await user.click(start)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  expect(post.mock.calls[1]?.[1]).toEqual(post.mock.calls[0]?.[1])
})

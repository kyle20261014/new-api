import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { CustomRoutingDialog } from '../custom-routing-dialog'
import type { CustomRouting, RoutingCandidate } from '../types'

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
let client: QueryClient
afterEach(() => client?.clear())

const candidates: RoutingCandidate[] = ['standard', 'priority', 'offline'].map(
  (group) => ({
    model: 'chat-model',
    display_name: 'Chat model',
    category: 'text',
    group,
    available: true,
    health: group === 'offline' ? 'soft_offline' : 'normal',
    quote: { currency: 'USD', comparison: 'unavailable', prices: [] },
    metrics: {
      protocol_class: 'openai',
      response_kind: 'ttft',
      response_ms_1h: null,
      response_samples_1h: 0,
      success_24h: null,
      samples_24h: 0,
      stale: true,
      updated_at: null,
    },
  })
)
const empty: CustomRouting = {
  schema_version: 1,
  mode: 'smart',
  strategy: 'custom',
  models: {},
}

function mount(
  value: CustomRouting = empty,
  available: RoutingCandidate[] | Error = candidates
) {
  vi.spyOn(api, 'get').mockImplementation(async () => {
    if (available instanceof Error) throw available
    return {
      data: {
        success: true,
        data: {
          items: available,
          total: available.length,
          page: 1,
          page_size: 100,
        },
      },
    }
  })
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onSave = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <CustomRoutingDialog
          value={value}
          onSave={onSave}
          onClose={() => undefined}
        />
      </I18nextProvider>
    </QueryClientProvider>
  )
  return onSave
}

test('healthy selection survives filters, manual keyboard reorder preserves focus, and apply emits only the draft', async () => {
  const user = userEvent.setup()
  const onSave = mount()
  await user.click(await screen.findByRole('button', { name: 'chat-model' }))
  await user.click(
    screen.getByRole('button', { name: 'Select healthy on this page' })
  )
  expect(
    screen.getByRole('checkbox', { name: /^Select chat-model \/ offline/ })
  ).not.toBeChecked()
  await user.type(
    screen.getByRole('textbox', { name: 'Search model name' }),
    'chat'
  )
  expect(
    screen.getByRole('button', { name: 'Remove chat-model / standard' })
  ).toBeInTheDocument()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Next' })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Next' }))
  await user.click(
    screen.getByRole('combobox', { name: 'Scheduling for chat-model' })
  )
  await user.click(await screen.findByRole('option', { name: 'Manual order' }))
  const handle = screen.getByRole('button', {
    name: 'Drag standard to reorder',
  })
  handle.focus()
  fireEvent.keyDown(handle, { key: 'ArrowDown' })
  expect(handle).toHaveFocus()
  expect(
    screen.getByRole('button', { name: 'Move standard down' })
  ).toBeDisabled()
  await user.click(
    screen.getByRole('switch', {
      name: /Try temporarily offline groups for chat-model$/,
    })
  )
  await user.click(screen.getByRole('button', { name: 'Apply to draft' }))
  expect(onSave).toHaveBeenCalledWith({
    ...empty,
    models: {
      'chat-model': {
        groups: ['priority', 'standard'],
        strategy: 'manual',
        try_soft_offline: true,
      },
    },
  })
})

test('removing the last saved group removes its model and disables continuation', async () => {
  const user = userEvent.setup()
  mount({
    ...empty,
    models: {
      'chat-model': {
        groups: ['standard'],
        strategy: 'balanced',
        try_soft_offline: false,
      },
    },
  })
  await user.click(
    await screen.findByRole('button', { name: 'Remove chat-model / standard' })
  )
  expect(
    screen.queryByRole('button', { name: 'Remove chat-model / standard' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
})

test('a revoked saved group remains visible and must be removed before advancing', async () => {
  mount({
    ...empty,
    models: {
      'chat-model': {
        groups: ['revoked', 'standard'],
        strategy: 'manual',
        try_soft_offline: false,
      },
    },
  })
  await screen.findByRole('alert')
  expect(
    screen.getByRole('button', { name: 'Remove chat-model / revoked' })
  ).toHaveTextContent('Unavailable')
  expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
})

test('candidate loading failure exposes retry and keeps the draft unsaved', async () => {
  const onSave = mount(empty, new Error('offline'))
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeEnabled()
  expect(onSave).not.toHaveBeenCalled()
})

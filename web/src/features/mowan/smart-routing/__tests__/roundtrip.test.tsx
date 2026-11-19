import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { useState } from 'react'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { RoutingControl } from '../routing-control'
import type { RoutingPolicy } from '../types'

test('a saved custom policy survives switching to a dynamic strategy and reopening the custom draft', async () => {
  const i18n = createInstance()
  await i18n
    .use(initReactI18next)
    .init({ lng: 'en', resources: { en: { translation: {} } } })
  const initial: RoutingPolicy = {
    schema_version: 1,
    mode: 'smart',
    strategy: 'custom',
    models: {
      chat: {
        groups: ['standard'],
        strategy: 'manual',
        try_soft_offline_groups: ['standard'],
      },
    },
  }
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          {
            model: 'chat',
            display_name: 'Chat',
            category: 'text',
            group: 'standard',
            available: true,
            health: 'normal',
            quote: { currency: 'USD', comparison: 'unavailable', prices: [] },
            metrics: {
              response_kind: 'ttft',
              response_ms_1h: null,
              response_samples_1h: 0,
              success_24h: null,
              samples_24h: 0,
              stale: true,
              updated_at: null,
            },
          },
        ],
        total: 1,
        page: 1,
        page_size: 100,
      },
    },
  })
  const changed = vi.fn()
  function Harness() {
    const [value, setValue] = useState(initial)
    return (
      <RoutingControl
        value={value}
        onChange={(policy) => {
          setValue(policy)
          changed(policy)
        }}
      />
    )
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <Harness />
      </I18nextProvider>
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: 'Routing strategy' }))
  await user.click(await screen.findByRole('option', { name: 'Price first' }))
  await user.click(screen.getByRole('combobox', { name: 'Routing strategy' }))
  await user.click(
    await screen.findByRole('option', { name: 'Custom per model' })
  )
  expect(
    await screen.findByRole('button', { name: 'Remove chat / standard' })
  ).toBeInTheDocument()
  await user.click(
    screen.getByRole('button', { name: 'Next: Scheduling configuration' })
  )
  expect(
    screen.getByRole('switch', {
      name: 'Try chat / standard while temporarily offline',
    })
  ).toBeChecked()
  await user.click(
    screen.getByRole('button', { name: 'Use this configuration' })
  )
  expect(changed).toHaveBeenLastCalledWith(initial)
  client.clear()
})

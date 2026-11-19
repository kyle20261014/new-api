/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { ModelCard } from '../components/model-card'
import { PricingTable } from '../components/pricing-table'
import type { PricingModel } from '../types'

let client: QueryClient

function pricingModel(overrides: Partial<PricingModel> = {}): PricingModel {
  return {
    id: 1,
    model_name: 'example-model',
    quota_type: 0,
    model_ratio: 1,
    completion_ratio: 3,
    enable_groups: ['default', 'premium'],
    group_ratio: { default: 1, premium: 3 },
    ...overrides,
  }
}

function serveCatalog() {
  return vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url.endsWith('/catalog')
        ? { groups: ['default', 'premium'], chat_supported: true }
        : [],
    },
  }))
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
  vi.restoreAllMocks()
})

test('text model cards expose a diagnostics entry that opens the workspace', async () => {
  serveCatalog()
  const user = userEvent.setup()
  render(
    <QueryClientProvider client={client}>
      <ModelCard model={pricingModel({ tags: 'text' })} onClick={vi.fn()} />
    </QueryClientProvider>
  )

  await user.click(screen.getByRole('button', { name: 'Model diagnostics' }))

  const dialog = await screen.findByRole('dialog', {
    name: 'Model diagnostics',
  })
  expect(
    await within(dialog).findByRole('button', { name: 'premium' })
  ).toBeVisible()
})

test('image and video model cards keep the diagnostics entry hidden', () => {
  render(
    <QueryClientProvider client={client}>
      <ModelCard
        model={pricingModel({ tags: 'image,video-generation' })}
        onClick={vi.fn()}
      />
    </QueryClientProvider>
  )

  expect(
    screen.queryByRole('button', { name: 'Model diagnostics' })
  ).not.toBeInTheDocument()
})

test('text rows expose a compact diagnostics entry without opening row details', async () => {
  serveCatalog()
  const user = userEvent.setup()
  const onModelClick = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <PricingTable
        models={[pricingModel({ tags: 'text' })]}
        onModelClick={onModelClick}
      />
    </QueryClientProvider>
  )

  await user.click(screen.getByRole('button', { name: 'Model diagnostics' }))

  expect(
    await screen.findByRole('dialog', { name: 'Model diagnostics' })
  ).toBeVisible()
  expect(onModelClick).not.toHaveBeenCalled()
})

test('non-text rows keep the diagnostics entry hidden', () => {
  render(
    <QueryClientProvider client={client}>
      <PricingTable models={[pricingModel({ tags: 'image' })]} />
    </QueryClientProvider>
  )

  expect(
    screen.queryByRole('button', { name: 'Model diagnostics' })
  ).not.toBeInTheDocument()
})

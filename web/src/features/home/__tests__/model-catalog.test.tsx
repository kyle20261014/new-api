import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
} from '@testing-library/react'
import type { InternalAxiosRequestConfig } from 'axios'
import { type ReactNode, StrictMode } from 'react'
import { afterEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  selectHomepageModels,
  useHomepageModels,
} from '../components/mowan-landing/model-catalog'
import { ModelsSection } from '../components/mowan-landing/models-section'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))
vi.mock('@/lib/lobe-icon', () => ({
  getLobeIcon: () => <span data-testid='model-icon' />,
  getLobeIconNames: () => ['OpenAI', 'Claude', 'Claude.Color'],
}))
vi.mock('@/features/mowan/smart-routing/routing-experience', () => ({
  RoutingExperience: (props: { model: string; canChat: boolean }) => (
    <div
      role='dialog'
      data-model={props.model}
      data-chat={String(props.canChat)}
    />
  ),
}))

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('homepage public catalog selection', () => {
  test.each([
    // Actual public-models.json output: channel inference labels embeddings OpenAI.
    { id: 'text-embedding-3-small', supported_endpoint_types: ['openai'] },
    {
      id: 'dall-e-3',
      supported_endpoint_types: ['openai', 'image-generation'],
    },
    { id: 'unconfirmed-model', supported_endpoint_types: ['openai'] },
  ])('channel-inferred endpoints never confirm chat for $id', (model) => {
    expect(selectHomepageModels([model])[0]?.canChat).toBe(false)
  })

  test('covers vendors before their other models, using catalog dates rather than model numbers', () => {
    const result = selectHomepageModels([
      { id: 'a-version-999', vendor_id: 1, catalog_created_time: 1 },
      { id: 'a-version-1', vendor_id: 1, catalog_created_time: 3 },
      { id: 'a-version-2', vendor_id: 1, catalog_created_time: 2 },
      { id: 'b-version-1', vendor_id: 2, catalog_created_time: 1 },
    ])
    expect(result.map((model) => model.id)).toEqual([
      'a-version-1',
      'b-version-1',
      'a-version-2',
      'a-version-999',
    ])
  })

  test('deduplicates IDs and gives missing or invalid dates a stable order without inventing vendors', () => {
    const result = selectHomepageModels([
      { id: 'z', catalog_created_time: Number.NaN },
      { id: 'b', vendor_id: 2, catalog_created_time: -1 },
      { id: 'a', vendor_id: 1 },
      { id: 'a', vendor_id: 1, catalog_created_time: 10 },
      { id: '' },
      { id: 'y' },
    ])
    expect(result.map((model) => model.id)).toEqual(['a', 'b', 'y', 'z'])
    expect(selectHomepageModels([])).toEqual([])
    expect(selectHomepageModels([{ id: 'one' }])).toHaveLength(1)
    expect(selectHomepageModels([{ id: 'one' }], 0)).toEqual([])
  })

  test('uses model icons then vendor icons and enables chat only with model-level confirmation', () => {
    const result = selectHomepageModels([
      {
        id: 'known',
        icon: 'Claude.Color',
        vendor_icon: 'OpenAI',
        supported_endpoint_types: ['openai'],
        can_chat: true,
      },
      { id: 'vendor', icon: ' ', vendor_icon: 'OpenAI' },
      { id: 'invalid-icon', icon: 'NotInstalled.Color', vendor_icon: 'OpenAI' },
      { id: 'unknown', supported_endpoint_types: ['embeddings'] },
    ])
    expect(result.find((model) => model.id === 'known')).toMatchObject({
      icon: 'Claude.Color',
      canChat: true,
    })
    expect(result.find((model) => model.id === 'vendor')).toMatchObject({
      icon: 'OpenAI',
      canChat: false,
    })
    expect(result.find((model) => model.id === 'unknown')).toMatchObject({
      icon: undefined,
      canChat: false,
    })
    expect(result.find((model) => model.id === 'invalid-icon')?.icon).toBe(
      'OpenAI'
    )
  })
})

test('two consumers share one public request, reuse five-minute cache and reflect catalog refresh', async () => {
  let ids = ['old']
  const request = vi.fn(async (config: InternalAxiosRequestConfig) => ({
    data: { success: true, data: ids.map((id) => ({ id })) },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  }))
  const previousAdapter = api.defaults.adapter
  api.defaults.adapter = request
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  function wrapper(props: { children: ReactNode }) {
    return (
      <StrictMode>
        <QueryClientProvider client={client}>
          {props.children}
        </QueryClientProvider>
      </StrictMode>
    )
  }
  try {
    const cancelled = renderHook(() => ({ ...useHomepageModels() }), {
      wrapper,
    })
    cancelled.unmount()
    const first = renderHook(() => ({ ...useHomepageModels() }), { wrapper })
    const second = renderHook(() => ({ ...useHomepageModels() }), { wrapper })
    await waitFor(() => expect(first.result.current.data?.[0]?.id).toBe('old'))
    expect(second.result.current.data?.[0]?.id).toBe('old')
    expect(request).toHaveBeenCalledTimes(1)
    expect(request.mock.calls[0]?.[0].url).toBe('/api/mowan/homepage/models')
    expect(
      client
        .getQueryCache()
        .find({ queryKey: ['homepage-models'] })
        ?.isStaleByTime(300000)
    ).toBe(false)
    ids = ['new']
    await act(async () => {
      await client.invalidateQueries({ queryKey: ['homepage-models'] })
    })
    await waitFor(() => expect(second.result.current.data?.[0]?.id).toBe('new'))
    expect(request).toHaveBeenCalledTimes(2)
    request.mockRejectedValue(new Error('offline'))
    await act(async () => {
      await first.result.current.refetch()
    })
    await waitFor(() => expect(first.result.current.isError).toBe(true))
    expect(first.result.current.data?.[0]?.id).toBe('new')
  } finally {
    cleanup()
    client.clear()
    api.defaults.adapter = previousAdapter
  }
})

test('business failure never becomes a successful empty catalog', async () => {
  const previousAdapter = api.defaults.adapter
  api.defaults.adapter = async (config) => ({
    data: { success: false, message: 'Unavailable' },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  try {
    const result = renderHook(() => useHomepageModels(), {
      wrapper: ({ children }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    })
    await waitFor(() => expect(result.result.current.isError).toBe(true))
    expect(result.result.current.data).toBeUndefined()
  } finally {
    cleanup()
    client.clear()
    api.defaults.adapter = previousAdapter
  }
})

test('empty, failed and cached catalogs stay honest, and chips pass the real ID and capability', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const previousAdapter = api.defaults.adapter
  api.defaults.adapter = async () => {
    throw new Error('offline')
  }
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
  try {
    client.setQueryData(['homepage-models'], [])
    const view = render(<ModelsSection />, { wrapper })
    expect(screen.getByText('mowan.models.catalogEmpty')).toBeInTheDocument()
    expect(view.container.querySelector('.xc-model-lane')).toBeNull()
    await act(async () => {
      await client.invalidateQueries({ queryKey: ['homepage-models'] })
    })
    expect(
      await screen.findByText('mowan.models.catalogError')
    ).toBeInTheDocument()
    expect(
      screen.queryByText('mowan.models.catalogEmpty')
    ).not.toBeInTheDocument()
    act(() => {
      client.setQueryData(
        ['homepage-models'],
        selectHomepageModels([
          { id: 'actual/model-id', vendor_name: 'Actual Vendor' },
        ])
      )
    })
    fireEvent.click(
      await screen.findByRole('button', { name: /actual\/model-id/ })
    )
    expect(screen.getByRole('dialog')).toHaveAttribute(
      'data-model',
      'actual/model-id'
    )
    expect(screen.getByRole('dialog')).toHaveAttribute('data-chat', 'false')
    await act(async () => {
      await client.invalidateQueries({ queryKey: ['homepage-models'] })
    })
    expect(
      await screen.findByText('mowan.models.catalogCached')
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: /actual\/model-id/ })
    ).toBeInTheDocument()
    act(() => {
      client.setQueryData(
        ['homepage-models'],
        selectHomepageModels([
          {
            id: 'actual/chat',
            supported_endpoint_types: ['openai'],
            can_chat: true,
          },
        ])
      )
    })
    fireEvent.click(await screen.findByRole('button', { name: /actual\/chat/ }))
    expect(screen.getByRole('dialog')).toHaveAttribute(
      'data-model',
      'actual/chat'
    )
    expect(screen.getByRole('dialog')).toHaveAttribute('data-chat', 'true')
  } finally {
    cleanup()
    client.clear()
    api.defaults.adapter = previousAdapter
  }
})

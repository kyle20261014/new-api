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
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { saveConfig } from '@/features/playground/lib/storage/storage'
import { api } from '@/lib/api'
import { Route as PlaygroundRoute } from '@/routes/_authenticated/playground/index'
import { Route as PricingRoute } from '@/routes/pricing/index'
import { useAuthStore } from '@/stores/auth-store'

// Charts are a browser canvas boundary, unrelated to model navigation.
vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))

let queryClient: QueryClient

beforeEach(() => {
  localStorage.clear()
  useAuthStore.getState().auth.setUser({ id: 1, username: 'tester', role: 1 })
  saveConfig({ model: 'other-model', group: 'default', temperature: 0.3 })
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/pricing') {
      return {
        data: {
          success: true,
          data: [
            {
              id: 1,
              model_name: 'provider/chat-model',
              quota_type: 0,
              model_ratio: 1,
              completion_ratio: 1,
              enable_groups: ['premium'],
            },
          ],
          vendors: [],
          group_ratio: { default: 1, premium: 2 },
          usable_group: { default: {}, premium: {} },
        },
      }
    }
    if (url === '/api/user/models') {
      return {
        data: { success: true, data: ['other-model', 'provider/chat-model'] },
      }
    }
    if (url === '/api/user/self/groups') {
      return {
        data: {
          success: true,
          data: {
            default: { ratio: 1, desc: '' },
            premium: { ratio: 2, desc: '' },
          },
        },
      }
    }
    return { data: { success: true, data: null } }
  })
})

afterEach(() => {
  queryClient.clear()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
})

async function openPage(path: string) {
  const root = createRootRoute({ component: Outlet })
  const pricing = createRoute({
    getParentRoute: () => root,
    path: 'pricing/',
    component: PricingRoute.options.component,
    validateSearch: PricingRoute.options.validateSearch,
  })
  const authenticated = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
    component: Outlet,
  })
  const playground = createRoute({
    getParentRoute: () => authenticated,
    path: 'playground/',
    component: PlaygroundRoute.options.component,
    validateSearch: PlaygroundRoute.options.validateSearch,
  })
  const router = createRouter({
    routeTree: root.addChildren([
      pricing,
      authenticated.addChildren([playground]),
    ]),
    history: createMemoryHistory({ initialEntries: [path] }),
    defaultPendingMinMs: 0,
  })
  await router.load()
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return router
}

it.each(['card', 'card-name', 'table'])(
  'clicking a model in %s view opens Playground with that model and a compatible group',
  async (view) => {
    const user = userEvent.setup()
    const router = await openPage(
      `/pricing?view=${view === 'table' ? 'table' : 'card'}`
    )
    await screen.findByText('provider/chat-model')
    if (view === 'card') {
      screen.getByRole('button', { name: 'Playground' }).focus()
      await user.keyboard('{Enter}')
    } else await user.click(screen.getByText('provider/chat-model'))
    await waitFor(() =>
      expect(router.state.location.pathname).toBe('/playground')
    )
    expect(router.state.location.search).toEqual({
      model: 'provider/chat-model',
      group: 'premium',
    })
    await waitFor(() =>
      expect(screen.getAllByRole('combobox')[0]).toHaveTextContent(
        'provider/chat-model'
      )
    )
    expect(screen.getAllByRole('combobox')[0]).toHaveTextContent('premium')
  }
)

it('an unavailable model link falls back to an available model and accepts another model link', async () => {
  const router = await openPage('/playground?model=unavailable&group=invalid')
  await waitFor(() =>
    expect(screen.getAllByRole('combobox')[0]).toHaveTextContent('other-model')
  )
  await act(() =>
    router.navigate({
      to: '/playground',
      search: { model: 'provider/chat-model', group: 'premium' },
    })
  )
  await waitFor(() =>
    expect(screen.getAllByRole('combobox')[0]).toHaveTextContent(
      'provider/chat-model'
    )
  )
})

it('a preselected model can still be changed manually in Playground', async () => {
  const user = userEvent.setup()
  await openPage('/playground?model=provider%2Fchat-model&group=premium')
  await waitFor(() => expect(screen.getAllByRole('combobox')[0]).toBeEnabled())
  await user.click(screen.getAllByRole('combobox')[0])
  await user.click(await screen.findByRole('option', { name: 'other-model' }))
  await waitFor(() =>
    expect(screen.getAllByRole('combobox')[0]).toHaveTextContent('other-model')
  )
})

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { RoutingExperience } from '../routing-experience'

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
let client: QueryClient
afterEach(() => {
  client?.clear()
  useAuthStore.getState().auth.reset()
})
function mount(onOpenChange = vi.fn()) {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const root = createRootRoute({
    component: () => (
      <RoutingExperience model='image-model' open onOpenChange={onOpenChange} />
    ),
  })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <I18nextProvider i18n={i18n}>
        <RouterProvider router={router} />
      </I18nextProvider>
    </QueryClientProvider>
  )
}
test('visitors can read shared help without querying private candidates or prices', async () => {
  useAuthStore.getState().auth.reset()
  const get = vi.spyOn(api, 'get')
  const post = vi.spyOn(api, 'post')
  const onOpenChange = vi.fn()
  mount(onOpenChange)
  expect(
    await screen.findByRole('button', { name: 'Sign in' })
  ).toBeInTheDocument()
  const user = userEvent.setup()
  const trigger = screen.getByRole('button', { name: 'How to choose?' })
  await user.click(trigger)
  const help = await screen.findByRole('dialog', {
    name: 'How should I choose a routing strategy?',
  })
  expect(
    within(help).getByText(
      /Speed and success rate use call records from the last 24 hours/
    )
  ).toBeInTheDocument()
  expect(
    screen.queryByRole('combobox', { name: 'Routing strategy' })
  ).not.toBeInTheDocument()
  await user.keyboard('{Escape}')
  await waitFor(() => expect(help).not.toBeInTheDocument())
  await waitFor(() => expect(trigger).toHaveFocus())
  expect(screen.getByRole('dialog', { name: 'Try image-model' })).toBeVisible()
  expect(onOpenChange).not.toHaveBeenCalled()
  expect(get).not.toHaveBeenCalled()
  expect(post).not.toHaveBeenCalled()
})
test('signed-in media experience uses the same routing workbench without writing a key or creating a task', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'fixture', role: 1 })
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/user/self/groups'
          ? { standard: { desc: 'Standard', ratio: 1 } }
          : { items: [], total: 0, page: 1, page_size: 100 },
    },
  }))
  const post = vi.spyOn(api, 'post')
  const put = vi.spyOn(api, 'put')
  mount()
  expect(
    await screen.findByText(
      'This media model uses the existing API workflow. A new creation workspace is not available here.'
    )
  ).toBeInTheDocument()
  const user = userEvent.setup()
  const trigger = screen.getByRole('button', { name: 'How to choose?' })
  await user.click(trigger)
  const help = await screen.findByRole('dialog', {
    name: 'How should I choose a routing strategy?',
  })
  await user.click(within(help).getByRole('button', { name: 'Close' }))
  await waitFor(() => expect(trigger).toHaveFocus())
  await user.click(screen.getByRole('button', { name: 'Exclusion settings' }))
  await screen.findByRole('heading', { name: 'Exclusion settings' })
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(
    screen.getByRole('combobox', { name: 'Routing strategy' })
  ).toHaveTextContent('Balanced')
  expect(post).not.toHaveBeenCalled()
  expect(put).not.toHaveBeenCalled()
})

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
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { SidebarProvider } from '@/components/ui/sidebar'
import { DirectionProvider } from '@/context/direction-provider'
import { LayoutProvider } from '@/context/layout-provider'
import { SearchProvider } from '@/context/search-provider'
import { useAuthStore } from '@/stores/auth-store'

import { AppHeader } from '../app-header'

beforeEach(() => {
  vi.stubGlobal('localStorage', {
    getItem: () => null,
    setItem: () => undefined,
    removeItem: () => undefined,
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  useAuthStore.getState().auth.reset()
})

async function renderHeader(showTopNav?: boolean) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {
    system_name: 'New API',
    version: 'v1.0.0',
    HeaderNavModules: JSON.stringify({
      home: true,
      console: true,
      pricing: { enabled: true, requireAuth: false },
      rankings: { enabled: true, requireAuth: false },
      docs: true,
      about: true,
    }),
  })
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'alice',
    role: 1,
  })

  function HeaderTestComponent() {
    return (
      <DirectionProvider>
        <LayoutProvider>
          <SearchProvider>
            <SidebarProvider defaultOpen>
              <AppHeader
                {...(showTopNav !== undefined ? { showTopNav } : {})}
              />
            </SidebarProvider>
          </SearchProvider>
        </LayoutProvider>
      </DirectionProvider>
    )
  }

  const router = createRouter({
    routeTree: createRootRoute({ component: HeaderTestComponent }),
    history: createMemoryHistory({ initialEntries: ['/dashboard/overview'] }),
  })
  await router.load()

  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

describe('AppHeader navigation visibility', () => {
  it('hides top navigation links when showTopNav is set to false in console layout', async () => {
    await renderHeader(false)

    // Navigation links should not be present in the header
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()

    // Header functional buttons must be preserved
    expect(
      screen.getByRole('button', { name: /toggle sidebar/i })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: /search/i })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: /notifications/i })
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: /open theme settings/i })
    ).toBeInTheDocument()
  })

  it('renders top navigation links by default', async () => {
    await renderHeader()

    const nav = screen.getByRole('navigation')
    expect(nav).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Home' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Console' })).toBeInTheDocument()
  })
})

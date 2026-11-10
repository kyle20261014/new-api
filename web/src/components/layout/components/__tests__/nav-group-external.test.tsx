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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BookOpen, Home } from 'lucide-react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { Sidebar, SidebarProvider, SidebarTrigger } from '@/components/ui/sidebar'

import { NavGroup } from '../nav-group'

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
})

function NavGroupTestConsumer() {
  return (
    <div>
      <SidebarTrigger />
      <Sidebar>
        <NavGroup
          title='Site'
          items={[
            {
              title: 'Home',
              url: '/',
              icon: Home,
            },
            {
              title: 'Documentation',
              url: 'https://docs.newapi.pro',
              icon: BookOpen,
              external: true,
            },
          ]}
        />
      </Sidebar>
    </div>
  )
}

async function renderNavGroup(isMobile = false) {
  if (isMobile) {
    window.innerWidth = 500
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      matches: query.includes('max-width'),
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }))
  } else {
    window.innerWidth = 1280
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }))
  }

  const router = createRouter({
    routeTree: createRootRoute({ component: NavGroupTestConsumer }),
    history: createMemoryHistory({ initialEntries: ['/dashboard/overview'] }),
  })
  await router.load()

  render(
    <SidebarProvider defaultOpen>
      <RouterProvider router={router} />
    </SidebarProvider>
  )
}

describe('NavGroup external links behavior', () => {
  it('renders anchor tag with target="_blank" and rel="noopener noreferrer" for external items', async () => {
    await renderNavGroup(false)

    const externalLink = screen.getByRole('link', { name: 'Documentation' })
    expect(externalLink).toBeInTheDocument()
    expect(externalLink.tagName.toLowerCase()).toBe('a')
    expect(externalLink).toHaveAttribute('href', 'https://docs.newapi.pro')
    expect(externalLink).toHaveAttribute('target', '_blank')
    expect(externalLink).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('renders internal router link for normal items', async () => {
    await renderNavGroup(false)

    const homeLink = screen.getByRole('link', { name: 'Home' })
    expect(homeLink).toBeInTheDocument()
    expect(homeLink).toHaveAttribute('href', '/')
    expect(homeLink).not.toHaveAttribute('target')
  })

  it('closes mobile sidebar drawer when clicking an external link in mobile viewport', async () => {
    const user = userEvent.setup()
    await renderNavGroup(true)

    // Initially, on mobile, the sidebar drawer is closed
    expect(screen.queryByRole('dialog', { name: 'Sidebar' })).not.toBeInTheDocument()

    // User clicks toggle sidebar button to open mobile drawer
    const trigger = screen.getByRole('button', { name: 'Toggle Sidebar' })
    await user.click(trigger)

    // The mobile drawer is now visible to the user
    expect(screen.getByRole('dialog', { name: 'Sidebar' })).toBeInTheDocument()

    // User clicks the external link inside the mobile drawer
    const externalLink = screen.getByRole('link', { name: 'Documentation' })
    expect(externalLink).toBeInTheDocument()
    await user.click(externalLink)

    // The mobile drawer is closed and unmounted from DOM
    expect(screen.queryByRole('dialog', { name: 'Sidebar' })).not.toBeInTheDocument()
  })
})

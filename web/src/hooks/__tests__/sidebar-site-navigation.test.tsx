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
import { cleanup, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { useSidebarData } from '../use-sidebar-data'

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

function renderSidebarData(statusOverride: Record<string, unknown> = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {
    system_name: 'New API',
    ...statusOverride,
  })
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'alice',
    role: 1,
  })

  function Wrapper(props: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    )
  }

  return renderHook(() => useSidebarData(), { wrapper: Wrapper })
}

describe('sidebar site navigation group migration', () => {
  it('migrates top navigation items to the site group and excludes console to prevent duplicate overview entry', () => {
    const { result } = renderSidebarData({
      HeaderNavModules: JSON.stringify({
        home: true,
        console: true,
        pricing: { enabled: true, requireAuth: false },
        rankings: { enabled: true, requireAuth: false },
        docs: true,
        about: true,
      }),
    })

    const siteGroup = result.current.navGroups.find(
      (group) => group.id === 'site'
    )
    expect(siteGroup).toBeDefined()
    expect(siteGroup?.title).toBe('Site')

    const siteUrls = siteGroup?.items.map((item) => item.url)
    // Home, Model Square, Rankings, Docs, About
    expect(siteUrls).toEqual(['/', '/pricing', '/rankings', '/docs', '/about'])
    // Console (/dashboard) must be excluded to prevent duplication with Overview (/dashboard/overview)
    expect(siteUrls).not.toContain('/dashboard')
  })

  it('marks external links when custom docs_link is provided', () => {
    const { result } = renderSidebarData({
      docs_link: 'https://docs.newapi.pro',
      HeaderNavModules: JSON.stringify({
        docs: true,
      }),
    })

    const siteGroup = result.current.navGroups.find(
      (group) => group.id === 'site'
    )
    const docsItem = siteGroup?.items.find(
      (item) => item.url === 'https://docs.newapi.pro'
    )
    expect(docsItem).toBeDefined()
    expect(docsItem?.external).toBe(true)
  })

  it('hides disabled modules according to HeaderNavModules settings', () => {
    const { result } = renderSidebarData({
      HeaderNavModules: JSON.stringify({
        home: true,
        console: true,
        pricing: { enabled: false, requireAuth: false },
        rankings: { enabled: false, requireAuth: false },
        docs: false,
        about: false,
      }),
    })

    const siteGroup = result.current.navGroups.find(
      (group) => group.id === 'site'
    )
    expect(siteGroup).toBeDefined()
    const siteUrls = siteGroup?.items.map((item) => item.url)
    expect(siteUrls).toEqual(['/'])
    expect(siteUrls).not.toContain('/pricing')
    expect(siteUrls).not.toContain('/rankings')
    expect(siteUrls).not.toContain('/docs')
    expect(siteUrls).not.toContain('/about')
  })

  it('omits site group entirely when all site modules are disabled', () => {
    const { result } = renderSidebarData({
      HeaderNavModules: JSON.stringify({
        home: false,
        console: true,
        pricing: { enabled: false, requireAuth: false },
        rankings: { enabled: false, requireAuth: false },
        docs: false,
        about: false,
      }),
    })

    const siteGroup = result.current.navGroups.find(
      (group) => group.id === 'site'
    )
    expect(siteGroup).toBeUndefined()
  })

  it('respects user-saved about=false without forcing about into the sidebar', () => {
    const { result } = renderSidebarData({
      HeaderNavModules: JSON.stringify({
        home: true,
        console: true,
        pricing: { enabled: true, requireAuth: false },
        rankings: { enabled: true, requireAuth: false },
        docs: true,
        about: false,
      }),
    })

    const siteGroup = result.current.navGroups.find(
      (group) => group.id === 'site'
    )
    const titles = siteGroup?.items.map((item) => item.title)
    expect(titles).not.toContain('About')
  })
})

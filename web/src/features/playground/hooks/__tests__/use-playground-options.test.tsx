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
import { renderHook, waitFor } from '@testing-library/react'
import { toast } from 'sonner'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import * as playgroundApi from '../../api'
import { usePlaygroundOptions } from '../use-playground-options'

describe('usePlaygroundOptions target model resolution (A4)', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
  })

  afterEach(() => {
    queryClient.clear()
    vi.restoreAllMocks()
  })

  it('retains current group when current group supports the target model', async () => {
    vi.spyOn(playgroundApi, 'getUserGroups').mockResolvedValue([
      { label: 'default', value: 'default', ratio: 1, desc: 'Default Group' },
      { label: 'vip', value: 'vip', ratio: 0.5, desc: 'VIP Group' },
    ])

    vi.spyOn(playgroundApi, 'getUserModels').mockImplementation(async (group) => {
      if (group === 'default') {
        return [
          { label: 'target-model', value: 'target-model' },
          { label: 'other-model', value: 'other-model' },
        ]
      }
      return [{ label: 'target-model', value: 'target-model' }]
    })

    const updateConfig = vi.fn()
    const setGroups = vi.fn()
    const setModels = vi.fn()

    renderHook(
      () =>
        usePlaygroundOptions({
          currentGroup: 'default',
          currentModel: 'other-model',
          targetModel: 'target-model',
          setGroups,
          setModels,
          updateConfig,
        }),
      {
        wrapper: ({ children }) => (
          <QueryClientProvider client={queryClient}>
            {children}
          </QueryClientProvider>
        ),
      }
    )

    await waitFor(() => {
      expect(updateConfig).toHaveBeenCalledWith('model', 'target-model')
    })

    // Group should remain unchanged (not updated to another group)
    expect(updateConfig).not.toHaveBeenCalledWith('group', 'vip')
  })

  it('selects lowest ratio group supporting target model if current group does not support it', async () => {
    vi.spyOn(playgroundApi, 'getUserGroups').mockResolvedValue([
      { label: 'expensive', value: 'expensive', ratio: 2, desc: 'Expensive' },
      { label: 'economy', value: 'economy', ratio: 0.3, desc: 'Economy' },
      { label: 'standard', value: 'standard', ratio: 0.8, desc: 'Standard' },
    ])

    vi.spyOn(playgroundApi, 'getUserModels').mockImplementation(async (group) => {
      if (group === 'expensive') {
        return [{ label: 'other-only', value: 'other-only' }]
      }
      if (group === 'economy') {
        return [{ label: 'special-model', value: 'special-model' }]
      }
      if (group === 'standard') {
        return [{ label: 'special-model', value: 'special-model' }]
      }
      return []
    })

    const updateConfig = vi.fn()
    const setGroups = vi.fn()
    const setModels = vi.fn()

    renderHook(
      () =>
        usePlaygroundOptions({
          currentGroup: 'expensive',
          currentModel: 'other-only',
          targetModel: 'special-model',
          setGroups,
          setModels,
          updateConfig,
        }),
      {
        wrapper: ({ children }) => (
          <QueryClientProvider client={queryClient}>
            {children}
          </QueryClientProvider>
        ),
      }
    )

    await waitFor(() => {
      // Economy has ratio 0.3 < Standard 0.8, should select economy
      expect(updateConfig).toHaveBeenCalledWith('group', 'economy')
      expect(updateConfig).toHaveBeenCalledWith('model', 'special-model')
    })
  })

  it('prompts error and preserves model when target model is not available in any group (A4)', async () => {
    vi.spyOn(playgroundApi, 'getUserGroups').mockResolvedValue([
      { label: 'default', value: 'default', ratio: 1, desc: 'Default Group' },
    ])

    vi.spyOn(playgroundApi, 'getUserModels').mockResolvedValue([
      { label: 'existing-model', value: 'existing-model' },
    ])

    const toastError = vi.spyOn(toast, 'error').mockReturnValue('toast-id' as unknown as string | number)
    const updateConfig = vi.fn()
    const setGroups = vi.fn()
    const setModels = vi.fn()

    renderHook(
      () =>
        usePlaygroundOptions({
          currentGroup: 'default',
          currentModel: 'old-model',
          targetModel: 'nonexistent-model',
          setGroups,
          setModels,
          updateConfig,
        }),
      {
        wrapper: ({ children }) => (
          <QueryClientProvider client={queryClient}>
            {children}
          </QueryClientProvider>
        ),
      }
    )

    await waitFor(() => {
      expect(toastError).toHaveBeenCalled()
    })

    // Must NOT select existing-model to fake success
    expect(updateConfig).not.toHaveBeenCalledWith('model', 'existing-model')
  })

  it('reports error when groups options load empty (A4)', async () => {
    vi.spyOn(playgroundApi, 'getUserGroups').mockResolvedValue([])
    vi.spyOn(playgroundApi, 'getUserModels').mockResolvedValue([])

    const toastError = vi.spyOn(toast, 'error').mockReturnValue('toast-id' as unknown as string | number)
    const updateConfig = vi.fn()
    const setGroups = vi.fn()
    const setModels = vi.fn()

    renderHook(
      () =>
        usePlaygroundOptions({
          currentGroup: '',
          currentModel: '',
          targetModel: 'target-model',
          setGroups,
          setModels,
          updateConfig,
        }),
      {
        wrapper: ({ children }) => (
          <QueryClientProvider client={queryClient}>
            {children}
          </QueryClientProvider>
        ),
      }
    )

    await waitFor(() => {
      expect(toastError).toHaveBeenCalled()
    })

    expect(updateConfig).not.toHaveBeenCalled()
  })
})

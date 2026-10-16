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
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import fr from '@/i18n/locales/fr.json'
import zhCN from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SiteStats } from '../site-stats'

let queryClient: QueryClient

const mockStatsData = {
  today: {
    top_up_quota: 50000000, // 100 USD
    consumed_quota: 1, // 1 quota unit = 0.000002 USD (micro charge precision test)
    request_count: 1234,
  },
  month: {
    top_up_quota: 250000000, // 500 USD
    consumed_quota: 5000000, // 10 USD
    request_count: 98765,
  },
  current_balance_quota: 100000000, // 200 USD
  updated_at: 1740000000,
  timezone: 'Asia/Shanghai',
}

const mockZeroStatsData = {
  today: {
    top_up_quota: 0,
    consumed_quota: 0,
    request_count: 0,
  },
  month: {
    top_up_quota: 0,
    consumed_quota: 0,
    request_count: 0,
  },
  current_balance_quota: 0,
  updated_at: 1740000000,
  timezone: 'Asia/Shanghai',
}

function renderSiteStats() {
  return render(
    <QueryClientProvider client={queryClient}>
      <SiteStats />
    </QueryClientProvider>
  )
}

describe('SiteStats', () => {
  beforeEach(async () => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
        },
      },
    })
    window.localStorage.clear()
    useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)

    i18next.addResourceBundle('zhCN', 'translation', zhCN.translation, true, true)
    i18next.addResourceBundle('fr', 'translation', fr.translation, true, true)
    await act(() => i18next.changeLanguage('en'))
  })

  afterEach(async () => {
    cleanup()
    queryClient.clear()
    useAuthStore.setState(useAuthStore.getInitialState(), true)
    useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
    vi.restoreAllMocks()
    await act(() => i18next.changeLanguage('en'))
  })

  describe('Role access control and fetch contracts', () => {
    it('hides and does not fetch for ordinary users', async () => {
      useAuthStore.getState().auth.setUser({
        id: 1,
        username: 'normal-user',
        role: ROLE.USER,
        quota: 1000,
        used_quota: 0,
        request_count: 0,
      })

      const getSpy = vi.spyOn(api, 'get')

      const { container } = renderSiteStats()

      expect(container.firstChild).toBeNull()
      expect(screen.queryByTestId('site-stats')).not.toBeInTheDocument()
      expect(screen.queryByTestId('site-stats-loading')).not.toBeInTheDocument()
      expect(getSpy).not.toHaveBeenCalled()
    })

    it('hides and does not fetch for guests (unauthenticated)', async () => {
      useAuthStore.getState().auth.setUser(null)
      const getSpy = vi.spyOn(api, 'get')

      const { container } = renderSiteStats()

      expect(container.firstChild).toBeNull()
      expect(getSpy).not.toHaveBeenCalled()
    })

    it('fetches and renders for admin users', async () => {
      useAuthStore.getState().auth.setUser({
        id: 2,
        username: 'admin-user',
        role: ROLE.ADMIN,
        quota: 0,
        used_quota: 0,
        request_count: 0,
      })

      const getSpy = vi.spyOn(api, 'get').mockResolvedValueOnce({
        data: {
          success: true,
          message: '',
          data: mockStatsData,
        },
      })

      renderSiteStats()

      expect(getSpy).toHaveBeenCalledWith('/api/data/site_stats')
      await waitFor(() => {
        expect(screen.getByTestId('site-stats')).toBeInTheDocument()
      })
      expect(screen.getByText('Site statistics')).toBeInTheDocument()
      expect(screen.getByText('Today')).toBeInTheDocument()
      expect(screen.getByText('Current month')).toBeInTheDocument()
    })

    it('fetches and renders for root / super admin users', async () => {
      useAuthStore.getState().auth.setUser({
        id: 3,
        username: 'root-user',
        role: ROLE.SUPER_ADMIN,
        quota: 0,
        used_quota: 0,
        request_count: 0,
      })

      vi.spyOn(api, 'get').mockResolvedValueOnce({
        data: {
          success: true,
          message: '',
          data: mockStatsData,
        },
      })

      renderSiteStats()

      await waitFor(() => {
        expect(screen.getByTestId('site-stats')).toBeInTheDocument()
      })
    })
  })

  describe('Loading state', () => {
    it('shows loading skeleton while query is pending', async () => {
      useAuthStore.getState().auth.setUser({
        id: 2,
        username: 'admin-user',
        role: ROLE.ADMIN,
        quota: 0,
        used_quota: 0,
        request_count: 0,
      })

      vi.spyOn(api, 'get').mockImplementation(
        () => new Promise(() => {}) // pending
      )

      renderSiteStats()

      expect(screen.getByTestId('site-stats-loading')).toBeInTheDocument()
      expect(screen.queryByTestId('site-stats')).not.toBeInTheDocument()
    })
  })

  describe('Error handling and retry', () => {
    it('displays error message and retry button on failure without showing zeros', async () => {
      const user = userEvent.setup()
      useAuthStore.getState().auth.setUser({
        id: 2,
        username: 'admin-user',
        role: ROLE.ADMIN,
        quota: 0,
        used_quota: 0,
        request_count: 0,
      })

      const getSpy = vi
        .spyOn(api, 'get')
        .mockRejectedValueOnce(new Error('Backend database error'))
        .mockResolvedValueOnce({
          data: {
            success: true,
            message: '',
            data: mockStatsData,
          },
        })

      renderSiteStats()

      await waitFor(() => {
        expect(screen.getByTestId('site-stats-error')).toBeInTheDocument()
      })

      expect(screen.getByText('Backend database error')).toBeInTheDocument()
      expect(screen.queryByTestId('site-stats')).not.toBeInTheDocument()

      // Click retry button in ErrorState
      const retryBtn = screen.getByRole('button', { name: 'Retry' })
      await user.click(retryBtn)

      await waitFor(() => {
        expect(screen.getByTestId('site-stats')).toBeInTheDocument()
      })
      expect(screen.queryByTestId('site-stats-error')).not.toBeInTheDocument()
      expect(getSpy).toHaveBeenCalledTimes(2)
    })
  })

  describe('Zero and paid values with precision preservation', () => {
    it('correctly displays zero values for all metrics', async () => {
      useAuthStore.getState().auth.setUser({
        id: 2,
        username: 'admin-user',
        role: ROLE.ADMIN,
        quota: 0,
        used_quota: 0,
        request_count: 0,
      })

      vi.spyOn(api, 'get').mockResolvedValueOnce({
        data: {
          success: true,
          message: '',
          data: mockZeroStatsData,
        },
      })

      renderSiteStats()

      await waitFor(() => {
        expect(screen.getByTestId('site-stats')).toBeInTheDocument()
      })

      // Multiple items show $0 or 0
      const zeroAmounts = screen.getAllByText('$0')
      expect(zeroAmounts.length).toBeGreaterThanOrEqual(2)

      const zeroCounts = screen.getAllByText('0')
      expect(zeroCounts.length).toBeGreaterThanOrEqual(2)
    })

    it('preserves small charge precision down to 1 quota unit ($0.000002)', async () => {
      useAuthStore.getState().auth.setUser({
        id: 2,
        username: 'admin-user',
        role: ROLE.ADMIN,
        quota: 0,
        used_quota: 0,
        request_count: 0,
      })

      vi.spyOn(api, 'get').mockResolvedValueOnce({
        data: {
          success: true,
          message: '',
          data: mockStatsData,
        },
      })

      renderSiteStats()

      await waitFor(() => {
        expect(screen.getByTestId('site-stats')).toBeInTheDocument()
      })

      // Credited top-ups today: 50,000,000 / 500,000 = $100
      expect(screen.getByText('$100')).toBeInTheDocument()

      // 1 quota unit / 500,000 = 0.000002 USD -> must preserve 6 decimal precision without rounding to 0
      expect(screen.getByText('$0.000002')).toBeInTheDocument()

      // Request counts formatted
      expect(screen.getByText('1,234')).toBeInTheDocument()
      expect(screen.getByText('98,765')).toBeInTheDocument()

      // Current balance: 100,000,000 / 500,000 = $200 (appears in both Today & Month)
      const balanceAmounts = screen.getAllByText('$200')
      expect(balanceAmounts.length).toBe(2)

      // Timezone rendered
      expect(screen.getByText('(Asia/Shanghai)')).toBeInTheDocument()
    })

    it('subscribes to currency configuration updates and refreshes display', async () => {
      useAuthStore.getState().auth.setUser({
        id: 2,
        username: 'admin-user',
        role: ROLE.ADMIN,
        quota: 0,
        used_quota: 0,
        request_count: 0,
      })

      vi.spyOn(api, 'get').mockResolvedValueOnce({
        data: {
          success: true,
          message: '',
          data: mockStatsData,
        },
      })

      renderSiteStats()

      await waitFor(() => {
        expect(screen.getByText('$100')).toBeInTheDocument()
      })

      // Update currency config to CNY with exchange rate 7.2
      act(() => {
        useSystemConfigStore.getState().setConfig({
          currency: {
            displayInCurrency: true,
            quotaDisplayType: 'CNY',
            usdExchangeRate: 7.2,
            quotaPerUnit: 500000,
            customCurrencySymbol: '',
            customCurrencyExchangeRate: 1,
          },
        })
      })

      // 100 USD * 7.2 = 720 CNY -> should display with ¥ symbol
      await waitFor(() => {
        expect(screen.getByText('¥720')).toBeInTheDocument()
      })
    })
  })

  describe('Locale and i18n handling', () => {
    it('renders localized labels in Chinese and reacts to language switch', async () => {
      useAuthStore.getState().auth.setUser({
        id: 2,
        username: 'admin-user',
        role: ROLE.ADMIN,
        quota: 0,
        used_quota: 0,
        request_count: 0,
      })

      vi.spyOn(api, 'get').mockResolvedValue({
        data: {
          success: true,
          message: '',
          data: mockStatsData,
        },
      })

      renderSiteStats()

      await waitFor(() => {
        expect(screen.getByText('Site statistics')).toBeInTheDocument()
      })

      // Switch to Chinese
      await act(async () => {
        await i18next.changeLanguage('zhCN')
      })

      expect(screen.getByText('全站统计')).toBeInTheDocument()
      expect(screen.getByText('今天')).toBeInTheDocument()
      expect(screen.getByText('本月')).toBeInTheDocument()
      expect(screen.getAllByText('充值入账').length).toBe(2)
      expect(screen.getAllByText('确认收入').length).toBe(2)
      expect(screen.getAllByText('用户余额').length).toBe(2)
      expect(screen.getAllByText('请求次数').length).toBe(2)

      // Switch to French
      await act(async () => {
        await i18next.changeLanguage('fr')
      })

      expect(screen.getByText('Statistiques du site')).toBeInTheDocument()
      expect(screen.getAllByText('Recharges créditées').length).toBe(2)
      expect(screen.getAllByText('Revenus comptabilisés').length).toBe(2)
    })
  })
})

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { Wallet } from '../index'

// Keep the real Wallet, payment hooks, API serializers, dialogs and history hook.
afterEach(() => {
  vi.restoreAllMocks()
  localStorage.clear()
})
it.each(['focus', 'visibility'])(
  'refreshes self and an already open history after trusted settlement through %s recovery',
  async (trigger) => {
    let visibility: DocumentVisibilityState = 'visible'
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(
      () => visibility
    )
    let paid = false
    const product = {
      sku_id: 'ten',
      name: 'Ten',
      price: 10,
      quota: 12,
      currency: 'USD',
    }
    const order = () => ({
      ...product,
      trade_no: 'wallet-order',
      request_id: 'wallet-request',
      status: paid ? 'success' : 'pending',
      platform_status: paid ? 'DONE' : 'NEW',
      creation_state: 'associated',
      credited: paid,
      refund_status: 'none',
      can_pay: !paid,
      can_check: true,
      can_recreate: false,
      query_error: false,
      payment_methods: [{ method: 'alipay', name: 'Alipay' }],
    })
    const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
      let data: unknown = []
      if (url === '/api/user/self') {
        data = {
          id: 1,
          quota: paid ? 6500000 : 500000,
          used_quota: 0,
          request_count: 0,
          aff_quota: 0,
        }
      } else if (url === '/api/status') data = { quota_display_type: 'USD' }
      else if (url === '/api/mowan/idatariver/products') {
        data = { enabled: true, products: [product] }
      } else if (url.startsWith('/api/user/topup/self?')) {
        data = {
          total: 1,
          items: [
            {
              id: 1,
              user_id: 1,
              amount: 12,
              money: 10,
              trade_no: 'wallet-order',
              status: paid ? 'success' : 'pending',
              payment_method: 'idatariver',
              create_time: 1,
              idatariver: order(),
            },
          ],
        }
      } else if (url === '/api/user/topup/info') {
        data = {
          enable_online_topup: false,
          enable_stripe_topup: false,
          pay_methods: [],
          min_topup: 1,
          stripe_min_topup: 1,
          amount_options: [],
          discount: {},
        }
      } else if (url === '/api/user/aff') data = 'affiliate'
      return { data: { success: true, data } }
    })
    const post = vi.spyOn(api, 'post').mockImplementation(async (url) => {
      if (url === '/api/mowan/idatariver/check') {
        return { data: { success: true, data: order() } }
      }
      return { data: { success: true, data: '1' } }
    })
    localStorage.setItem(
      'mowan-idr:v1:1',
      JSON.stringify({ trade_no: 'wallet-order' })
    )
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const user = userEvent.setup()
    const view = render(
      <QueryClientProvider client={client}>
        <Wallet initialShowHistory />
      </QueryClientProvider>
    )
    try {
      await screen.findByText('wallet-order', { selector: 'code' })
      // Close the automatic payment dialog, leaving the already-open history visible.
      if (screen.queryByRole('alertdialog')) {
        await user.click(screen.getByRole('button', { name: 'Cancel' }))
      }
      const history = within(screen.getByRole('dialog'))
      const selfBefore = get.mock.calls.filter(
        ([url]) => url === '/api/user/self'
      ).length
      const historyBefore = get.mock.calls.filter(([url]) =>
        url.startsWith('/api/user/topup/self?')
      ).length
      paid = true
      // Native event ordering is still independently verified in a real browser.
      act(() => {
        if (trigger === 'visibility') visibility = 'hidden'
        window.dispatchEvent(new Event('focus'))
        if (trigger === 'visibility') {
          visibility = 'visible'
          document.dispatchEvent(new Event('visibilitychange'))
        }
      })
      await waitFor(() =>
        expect(
          get.mock.calls.filter(([url]) => url === '/api/user/self').length
        ).toBeGreaterThan(selfBefore)
      )
      await waitFor(() =>
        expect(
          get.mock.calls.filter(([url]) =>
            url.startsWith('/api/user/topup/self?')
          ).length
        ).toBeGreaterThan(historyBefore)
      )
      expect(history.getByText('Success')).toBeVisible()
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
      expect(
        post.mock.calls.filter(([url]) => url === '/api/mowan/idatariver/order')
      ).toHaveLength(0)
    } finally {
      view.unmount()
      client.clear()
    }
  }
)

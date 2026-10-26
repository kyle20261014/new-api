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
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { IdataRiverTopup } from '../components/idatariver-topup'
import { evaluateTopupOutcome } from '../lib/billing'
import { isAlipayOrWeChat, isSafePaymentUrl } from '../lib/idatariver'

describe('idatariver helpers', () => {
  it('validates safe payment URLs', () => {
    expect(isSafePaymentUrl('https://pay.idatariver.com/cashier/123')).toBe(
      true
    )
    expect(isSafePaymentUrl('http://localhost:3000/pay')).toBe(true)
    expect(isSafePaymentUrl('javascript:alert(1)')).toBe(false)
    expect(isSafePaymentUrl('data:text/html;base64,123')).toBe(false)
    expect(isSafePaymentUrl('')).toBe(false)
  })

  it('detects WeChat and Alipay methods', () => {
    expect(isAlipayOrWeChat('alipay', '支付宝支付')).toBe(true)
    expect(isAlipayOrWeChat('wxpay', '微信扫码')).toBe(true)
    expect(isAlipayOrWeChat('epusdt', 'USDT-TRC20')).toBe(false)
  })

  it('evaluates topup outcomes with refund status priority', () => {
    // Refund recovered takes priority over status 'success'
    const recovered = evaluateTopupOutcome('success', 'recovered')
    expect(recovered.kind).toBe('refunded_recovered')

    // Manual required
    const manual = evaluateTopupOutcome('success', 'manual_required')
    expect(manual.kind).toBe('refunded_manual')

    // Refunded before credit
    const beforeCredit = evaluateTopupOutcome(
      'success',
      'refunded_before_credit'
    )
    expect(beforeCredit.kind).toBe('refunded_before_credit')

    // Normal success
    const normal = evaluateTopupOutcome('success', 'none')
    expect(normal.kind).toBe('success')

    // Pending
    const pending = evaluateTopupOutcome('pending', 'none')
    expect(pending.kind).toBe('pending')
  })
})

describe('IdataRiverTopup component', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    useSystemConfigStore
      .getState()
      .setConfig({ currency: DEFAULT_CURRENCY_CONFIG })
  })

  it('renders products and handles checkout flow', async () => {
    const user = userEvent.setup()

    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        message: '',
        data: {
          enabled: true,
          products: [
            {
              sku_id: '6adb0f2e7cea3a5b4c41745e',
              name: '支付10U',
              price: 10,
              quota: 10,
              currency: 'USD',
            },
          ],
        },
      },
    })

    vi.spyOn(window, 'open').mockImplementation(() => null)
    vi.spyOn(api, 'post').mockImplementation(async (url: string) => {
      if (url === '/api/user/idatariver/pay') {
        return {
          data: {
            success: true,
            message: '',
            data: {
              pay_url: 'https://pay.idatariver.com/cashier/123',
              pay_currency: 'CNY',
              amount: 74.5,
            },
          },
        }
      }
      return {
        data: {
          success: true,
          message: '',
          data: {
            trade_no: 'TR1234567890',
            status: 'pending',
            price: 10,
            quota: 10,
            currency: 'USD',
            payment_methods: [
              { method: 'alipay', name: 'Alipay' },
              { method: 'usdt', name: 'USDT' },
            ],
            refund_status: 'none',
          },
        },
      }
    })

    render(<IdataRiverTopup />)

    // Wait for product package to load
    await waitFor(() => {
      expect(screen.getByText('支付10U')).toBeInTheDocument()
    })

    // Click on the package
    const packageBtn = screen.getByRole('button', { name: /支付10U/i })
    await user.click(packageBtn)

    // Confirm Payment Dialog should appear
    await waitFor(() => {
      expect(screen.getByText('iDataRiver Payment')).toBeInTheDocument()
      expect(screen.getByText('TR1234567890')).toBeInTheDocument()
      expect(
        screen.getByText(/If you cannot open the payment page/i)
      ).toBeInTheDocument()
      expect(
        screen.getByRole('button', { name: /Proceed to Pay/i })
      ).toBeInTheDocument()
    })

    // 1) Verify aria-pressed on payment methods and keyboard navigation
    const alipayBtn = screen.getByRole('button', { name: 'Alipay' })
    const usdtBtn = screen.getByRole('button', { name: 'USDT' })
    expect(alipayBtn).toHaveAttribute('aria-pressed', 'true')
    expect(usdtBtn).toHaveAttribute('aria-pressed', 'false')

    // Switch payment method via keyboard Space
    usdtBtn.focus()
    await user.keyboard(' ')
    expect(usdtBtn).toHaveAttribute('aria-pressed', 'true')
    expect(alipayBtn).toHaveAttribute('aria-pressed', 'false')

    // 2) Verify Wallet Price formatting does not duplicate currency symbol (no "$10 USD")
    expect(screen.queryByText('$10 USD')).not.toBeInTheDocument()
    expect(screen.getByText('10 USD')).toBeInTheDocument()

    // 3) Verify Platform Payable Amount formatting with actual platform currency (e.g. CNY, not USD)
    const proceedBtn = screen.getByRole('button', { name: /Proceed to Pay/i })
    await user.click(proceedBtn)

    await waitFor(() => {
      expect(screen.getByText('Platform Payable Amount')).toBeInTheDocument()
      expect(screen.getByText('74.5 CNY')).toBeInTheDocument()
      expect(screen.queryByText('74.5 USD')).not.toBeInTheDocument()
    })
  })

  it('preserves BTC payment and USD price while switching all interface languages', async () => {
    const user = userEvent.setup()
    const languages = [
      ['en', '0.0000123456789012 BTC', '10.123456 USD'],
      ['zhCN', '0.0000123456789012 BTC', '10.123456 USD'],
      ['zhTW', '0.0000123456789012 BTC', '10.123456 USD'],
      ['fr', '0,0000123456789012 BTC', '10,123456 USD'],
      ['ru', '0,0000123456789012 BTC', '10,123456 USD'],
      ['ja', '0.0000123456789012 BTC', '10.123456 USD'],
      ['vi', '0,0000123456789012 BTC', '10,123456 USD'],
      ['invalid_locale', '0.0000123456789012 BTC', '10.123456 USD'],
    ] as const
    const i18n = createInstance()
    await i18n.init({
      lng: 'en',
      fallbackLng: 'en',
      resources: Object.fromEntries(
        languages.map(([language]) => [
          language,
          {
            translation: {
              'Platform Payable Amount': 'Platform Payable Amount',
            },
          },
        ])
      ),
    })
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        quotaDisplayType: 'CNY',
        usdExchangeRate: 7,
      },
    })

    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        message: '',
        data: {
          enabled: true,
          products: [
            {
              sku_id: 'btc-sku',
              name: 'BTC Package',
              price: 10.123456,
              quota: 10,
              currency: 'USD',
            },
          ],
        },
      },
    })

    vi.spyOn(window, 'open').mockImplementation(() => null)
    vi.spyOn(api, 'post').mockImplementation(async (url: string) => {
      if (url === '/api/user/idatariver/pay') {
        return {
          data: {
            success: true,
            message: '',
            data: {
              pay_url: 'https://pay.idatariver.com/cashier/btc',
              pay_currency: 'BTC',
              amount: 0.0000123456789012,
            },
          },
        }
      }
      return {
        data: {
          success: true,
          message: '',
          data: {
            trade_no: 'TR-BTC-999',
            status: 'pending',
            price: 10.123456,
            quota: 10,
            currency: 'USD',
            payment_methods: [{ method: 'btc', name: 'Bitcoin' }],
            refund_status: 'none',
          },
        },
      }
    })

    render(
      <I18nextProvider i18n={i18n}>
        <IdataRiverTopup />
      </I18nextProvider>
    )

    await waitFor(() => {
      expect(screen.getByText('BTC Package')).toBeInTheDocument()
    })

    const packageBtn = screen.getByRole('button', { name: /BTC Package/i })
    expect(packageBtn).toHaveAccessibleName(/Price: ¥70\.86$/)
    await user.click(packageBtn)

    await waitFor(() => {
      expect(screen.getByText('TR-BTC-999')).toBeInTheDocument()
    })

    // A configured display exchange rate must not alter the quoted USD price.
    expect(screen.getByText('10.123456 USD')).toBeInTheDocument()

    const proceedBtn = screen.getByRole('button', { name: /Proceed to Pay/i })
    await user.click(proceedBtn)

    // Platform payable preserves full 16 decimal places and BTC currency
    await waitFor(() => {
      expect(screen.getByText('Platform Payable Amount')).toBeInTheDocument()
      expect(screen.getByText('0.0000123456789012 BTC')).toBeInTheDocument()
      expect(
        screen.queryByText('0.0000123456789012 USD')
      ).not.toBeInTheDocument()
      expect(screen.queryByText('0.00 BTC')).not.toBeInTheDocument()
    })

    for (const [language, payable, price] of languages) {
      await act(async () => {
        await i18n.changeLanguage(language)
      })
      expect(screen.getByText(payable)).toBeInTheDocument()
      expect(screen.getByText(price)).toBeInTheDocument()
    }
  })

  it('allows manual order check and handles manual_required warning', async () => {
    const user = userEvent.setup()
    const toastWarningSpy = vi.spyOn(toast, 'warning')

    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        message: '',
        data: {
          enabled: true,
          products: [],
        },
      },
    })

    vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        message: '',
        data: {
          trade_no: 'TR999888777',
          status: 'success',
          price: 20,
          quota: 20,
          currency: 'USD',
          payment_methods: [],
          refund_status: 'manual_required',
        },
      },
    })

    render(<IdataRiverTopup />)

    // Wait for initial load to finish
    await waitFor(() => {
      expect(screen.getByText('Fixed Top-up Packages')).toBeInTheDocument()
    })

    // Open Check Order Status dialog
    const checkStatusBtn = screen.getByRole('button', {
      name: /Check Order Status/i,
    })
    await user.click(checkStatusBtn)

    await waitFor(() => {
      expect(
        screen.getByPlaceholderText('Enter order number...')
      ).toBeInTheDocument()
    })

    // Enter trade number
    const input = screen.getByPlaceholderText('Enter order number...')
    await user.type(input, 'TR999888777')

    const checkSubmitBtn = screen.getByRole('button', {
      name: 'Check Status',
    })
    await user.click(checkSubmitBtn)

    // Toast warning should be called and input should still preserve value
    await waitFor(() => {
      expect(toastWarningSpy).toHaveBeenCalled()
      expect(input).toHaveValue('TR999888777')
    })
  })
})

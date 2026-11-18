import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, expect, it, vi } from 'vitest'

import { mapStatusDataToConfig } from '@/lib/status-query'
import { useSystemConfigStore } from '@/stores/system-config-store'

import * as api from '../api'
import { formatPaymentAmount } from '../format'
import { FixedPackages, FixedPaymentDialog } from '../payment'
import { useIdataRiver } from '../use-payment'

const language = vi.hoisted(() => ({ value: 'en' }))
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { language: language.value },
  }),
}))
vi.mock('../api', async (original) => ({
  ...(await original<typeof import('../api')>()),
  products: vi.fn(),
  create: vi.fn(),
  check: vi.fn(),
  pay: vi.fn(),
}))
const product: api.Product = {
  sku_id: 'ten',
  name: 'Ten',
  price: 10,
  quota: 12,
  currency: 'USD',
}
const order: api.Order = {
  ...product,
  trade_no: 'order-ui',
  request_id: 'request-ui',
  status: 'pending',
  platform_status: 'NEW',
  creation_state: 'associated',
  credited: false,
  refund_status: 'none',
  can_pay: true,
  can_check: true,
  can_recreate: false,
  query_error: false,
  payment_methods: [{ method: 'alipay', name: 'Alipay' }],
}
const settled = async () => {}
function WalletPayment() {
  const flow = useIdataRiver(1, undefined, settled)
  const [selected, select] = useState<api.Product | null>(null)
  return (
    <>
      <FixedPackages
        flow={flow}
        onSelect={(next) => {
          select(next)
          flow.choose()
        }}
      />
      <FixedPaymentDialog
        flow={flow}
        selected={selected}
        onClear={() => select(null)}
      />
    </>
  )
}

beforeEach(() => {
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
  language.value = 'en'
  localStorage.clear()
  vi.clearAllMocks()
  vi.mocked(api.products).mockResolvedValue({
    success: true,
    data: { enabled: true, products: [product] },
  })
  vi.mocked(api.create).mockResolvedValue({ success: true, data: order })
  vi.mocked(api.check).mockResolvedValue({ success: true, data: order })
})

it('updates CNY references from shared status without changing USD or create arguments', async () => {
  useSystemConfigStore.getState().setConfig(
    mapStatusDataToConfig({
      quota_display_type: 'CNY',
      usd_exchange_rate: 7.25,
    })
  )
  const user = userEvent.setup()
  render(<WalletPayment />)
  const card = await screen.findByRole('button', { name: /Ten/ })
  expect(within(card).getByText('≈ ¥72.5')).toBeVisible()
  expect(within(card).getByText('≈ ¥87')).toBeVisible()
  await user.click(card)
  const dialog = within(screen.getByRole('alertdialog'))
  expect(dialog.getByText('10 USD')).toBeVisible()
  expect(dialog.getByText('12 USD')).toBeVisible()
  expect(dialog.getByText('≈ ¥72.5')).toBeVisible()
  act(() =>
    useSystemConfigStore.getState().setConfig(
      mapStatusDataToConfig({
        quota_display_type: 'CNY',
        usd_exchange_rate: 8,
      })
    )
  )
  expect(dialog.getByText('≈ ¥80')).toBeVisible()
  expect(dialog.getByText('≈ ¥96')).toBeVisible()
  expect(api.create).not.toHaveBeenCalled()
  await user.click(dialog.getByRole('button', { name: 'Continue' }))
  expect(api.create).toHaveBeenCalledWith('ten', expect.any(String))
  expect(vi.mocked(api.create).mock.calls[0]).toHaveLength(2)
})

it('uses the existing confirmation dialog, distinguishes USD price and credit, and resumes after Escape', async () => {
  const user = userEvent.setup()
  render(<WalletPayment />)
  await user.click(await screen.findByRole('button', { name: /Ten/ }))
  const dialog = within(screen.getByRole('alertdialog'))
  expect(dialog.getByText('10 USD')).toBeInTheDocument()
  expect(dialog.getByText('12 USD')).toBeInTheDocument()
  expect(
    screen.getByText('Confirmed on the official payment page')
  ).toBeInTheDocument()
  expect(api.create).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  expect(await screen.findByRole('radio', { name: 'Alipay' })).toBeChecked()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Continue' })).toBeEnabled()
  )
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(localStorage.getItem('mowan-idr:v1:1')).toContain('order-ui')
  await user.click(screen.getByRole('button', { name: 'Resume payment' }))
  expect(await screen.findByRole('radio', { name: 'Alipay' })).toBeChecked()
  expect(api.create).toHaveBeenCalledTimes(1)
})

it.each([
  ['en', '0.0002'],
  ['zhCN', '0.0002'],
  ['zhTW', '0.0002'],
  ['ja', '0.0002'],
  ['fr', '0,0002'],
  ['ru', '0,0002'],
  ['vi', '0,0002'],
])(
  'preserves small nonzero package and crypto amounts in %s',
  async (locale, amount) => {
    language.value = locale
    const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
    render(
      <FixedPaymentDialog
        flow={{
          ...result.current,
          open: true,
          order: { ...order, price: 0.0002, quota: 0.0002 },
          payment: {
            amount: 0.0002,
            pay_currency: 'BTC',
            pay_url: 'https://example.test/pay',
          },
        }}
        selected={null}
        onClear={() => {}}
      />
    )
    const dialog = within(screen.getByRole('alertdialog'))
    expect(dialog.getAllByText(`${amount} USD`)).toHaveLength(2)
    expect(dialog.getByText(`${amount} BTC`)).toBeInTheDocument()
  }
)

it('offers retry after a creation rejection and preserves the original attempt', async () => {
  useSystemConfigStore
    .getState()
    .setConfig(mapStatusDataToConfig({ quota_display_type: 'CNY' }))
  vi.mocked(api.create).mockResolvedValueOnce({ success: false })
  vi.mocked(api.check).mockResolvedValue({ success: false })
  const user = userEvent.setup()
  render(<WalletPayment />)
  await user.click(await screen.findByRole('button', { name: /Ten/ }))
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  const original = vi.mocked(api.create).mock.calls[0]
  vi.mocked(api.create).mockResolvedValue({ success: true, data: order })
  vi.mocked(api.check).mockResolvedValue({ success: true, data: order })
  const retry = await screen.findByRole('button', { name: 'Retry' })
  await waitFor(() => expect(retry).toBeEnabled())
  await user.click(retry)
  expect(await screen.findByRole('radio', { name: 'Alipay' })).toBeChecked()
  expect(vi.mocked(api.create).mock.calls[1]).toEqual(original)
})

it.each([0.0002, 1e-30, Number.MIN_VALUE])(
  'does not round %s to zero',
  (value) => {
    expect(formatPaymentAmount(value, 'en')).not.toBe('0')
  }
)

it('keeps an older saved request without a SKU from taking over a newly chosen purchase', async () => {
  localStorage.setItem(
    'mowan-idr:v1:1',
    JSON.stringify({ request_id: 'older-attempt' })
  )
  vi.mocked(api.check).mockResolvedValue({ success: false })
  const user = userEvent.setup()
  render(<WalletPayment />)
  const button = await screen.findByRole('button', { name: /Ten/ })
  expect(button).toBeEnabled()
  expect(api.check).not.toHaveBeenCalled()
  await user.click(button)
  vi.mocked(api.check).mockResolvedValue({ success: true, data: order })
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  expect(api.create).toHaveBeenCalledExactlyOnceWith(
    product.sku_id,
    expect.any(String)
  )
  expect(vi.mocked(api.create).mock.calls[0][1]).not.toBe('older-attempt')
})

it('keeps unknown payment amounts distinct from zero', () => {
  const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
  render(
    <FixedPaymentDialog
      flow={{
        ...result.current,
        open: true,
        order,
        payment: {
          amount: null,
          pay_currency: 'BTC',
          pay_url: 'https://example.test/pay',
        },
      }}
      selected={null}
      onClear={() => {}}
    />
  )
  expect(
    screen.getByText('Confirmed on the official payment page')
  ).toBeInTheDocument()
  expect(screen.queryByText('0 BTC')).not.toBeInTheDocument()
})

it.each([0.0002, 72, null])(
  'waits for explicit navigation after displaying channel amount %s',
  async (amount) => {
    useSystemConfigStore.getState().setConfig(
      mapStatusDataToConfig({
        quota_display_type: 'CNY',
        usd_exchange_rate: 7.25,
      })
    )
    vi.mocked(api.pay).mockResolvedValue({
      success: true,
      data: {
        pay_url: 'https://example.test/pay',
        amount,
        pay_currency: amount === 72 ? 'CNY' : 'BTC',
      },
    })
    const assign = vi.fn()
    vi.stubGlobal('location', { ...window.location, assign })
    try {
      const user = userEvent.setup()
      render(<WalletPayment />)
      await user.click(await screen.findByRole('button', { name: /Ten/ }))
      await user.click(screen.getByRole('button', { name: 'Continue' }))
      await screen.findByRole('radio', { name: 'Alipay' })
      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Continue' })).toBeEnabled()
      )
      await user.click(screen.getByRole('button', { name: 'Continue' }))
      await waitFor(() => expect(api.pay).toHaveBeenCalledTimes(1))
      const next = await screen.findByRole('button', {
        name: 'Continue to payment',
      })
      expect(assign).not.toHaveBeenCalled()
      expect(
        screen.getByText(
          amount === null
            ? 'Confirmed on the official payment page'
            : `${amount} ${amount === 72 ? 'CNY' : 'BTC'}`
        )
      ).toBeVisible()
      await user.click(next)
      expect(assign).toHaveBeenCalledExactlyOnceWith('https://example.test/pay')
      expect(api.create).toHaveBeenCalledTimes(1)
    } finally {
      vi.unstubAllGlobals()
    }
  }
)

it('uses the restored USD snapshot instead of the currently selected product for CNY references', () => {
  useSystemConfigStore
    .getState()
    .setConfig(
      mapStatusDataToConfig({ quota_display_type: 'CNY', usd_exchange_rate: 8 })
    )
  const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
  render(
    <FixedPaymentDialog
      flow={{
        ...result.current,
        open: true,
        order: { ...order, price: 20, quota: 25 },
      }}
      selected={product}
      onClear={() => {}}
    />
  )
  expect(screen.getByText('20 USD')).toBeVisible()
  expect(screen.getByText('25 USD')).toBeVisible()
  expect(screen.getByText('≈ ¥160')).toBeVisible()
  expect(screen.getByText('≈ ¥200')).toBeVisible()
  expect(screen.queryByText('≈ ¥80')).not.toBeInTheDocument()
})

it.each(['DONE', 'EXPIRED', 'REFUND', 'no channels'] as const)(
  'shows status instead of payment confirmation for restored %s without creating replacements',
  (state) => {
    const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
    const restored = { ...order, can_pay: false }
    if (state === 'no channels') {
      restored.can_pay = true
      restored.payment_methods = []
    } else restored.platform_status = state
    render(
      <FixedPaymentDialog
        flow={{ ...result.current, open: true, order: restored }}
        selected={null}
        onClear={() => {}}
      />
    )
    expect(screen.getByRole('dialog', { name: 'Check Status' })).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Continue' })
    ).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Close' })).toBeEnabled()
    expect(api.create).not.toHaveBeenCalled()
  }
)

it('clears the old quote when switching channels and fetches the new channel before navigation', async () => {
  const multi = {
    ...order,
    payment_methods: [
      ...order.payment_methods,
      { method: 'crypto', name: 'Crypto' },
    ],
  }
  vi.mocked(api.create).mockResolvedValue({ success: true, data: multi })
  vi.mocked(api.check).mockResolvedValue({ success: true, data: multi })
  vi.mocked(api.pay)
    .mockResolvedValueOnce({
      success: true,
      data: {
        amount: 10,
        pay_currency: 'CNY',
        pay_url: 'https://example.test/old',
      },
    })
    .mockResolvedValueOnce({
      success: true,
      data: {
        amount: 0.0002,
        pay_currency: 'BTC',
        pay_url: 'https://example.test/new',
      },
    })
  const user = userEvent.setup()
  render(<WalletPayment />)
  await user.click(await screen.findByRole('button', { name: /Ten/ }))
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  await screen.findByRole('radio', { name: 'Alipay' })
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Continue' })).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  expect(await screen.findByText('10 CNY')).toBeVisible()
  await user.click(screen.getByRole('radio', { name: 'Crypto' }))
  expect(screen.queryByText('10 CNY')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Continue to payment' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  expect(await screen.findByText('0.0002 BTC')).toBeVisible()
  expect(api.pay).toHaveBeenLastCalledWith(order.trade_no, 'crypto')
  expect(api.create).toHaveBeenCalledTimes(1)
})

it.each([
  { trade_no: 'old-unpaid-order' },
  { request_id: 'old-attempt', sku_id: 'ten' },
])(
  'allows a fresh twenty-dollar purchase without querying saved %j',
  async (saved) => {
    localStorage.setItem('mowan-idr:v1:1', JSON.stringify(saved))
    const twenty = {
      ...product,
      sku_id: 'twenty',
      name: 'Twenty',
      price: 20,
      quota: 20,
    }
    vi.mocked(api.products).mockResolvedValue({
      success: true,
      data: { enabled: true, products: [product, twenty] },
    })
    vi.mocked(api.check).mockRejectedValue(new Error('old order unavailable'))
    const user = userEvent.setup()
    render(<WalletPayment />)
    const card = await screen.findByRole('button', { name: /Twenty/ })
    act(() => {
      window.dispatchEvent(new Event('focus'))
      document.dispatchEvent(new Event('visibilitychange'))
    })
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(api.check).not.toHaveBeenCalled()
    expect(card).toBeEnabled()
    await user.click(card)
    const dialog = within(screen.getByRole('alertdialog'))
    expect(dialog.getAllByText('20 USD')).toHaveLength(2)
    expect(api.create).not.toHaveBeenCalled()
    vi.mocked(api.create).mockResolvedValue({
      success: true,
      data: { ...order, ...twenty, trade_no: 'new-twenty-order' },
    })
    vi.mocked(api.check).mockResolvedValue({
      success: true,
      data: { ...order, ...twenty, trade_no: 'new-twenty-order' },
    })
    await user.click(dialog.getByRole('button', { name: 'Continue' }))
    expect(api.create).toHaveBeenCalledExactlyOnceWith(
      'twenty',
      expect.any(String)
    )
    expect(await screen.findByRole('radio', { name: 'Alipay' })).toBeChecked()
    expect(
      within(screen.getByRole('alertdialog')).getAllByText('20 USD')
    ).toHaveLength(2)
    expect(api.pay).not.toHaveBeenCalled()
  }
)

it('shows retry and close instead of payment confirmation when an explicit old-order query fails', async () => {
  localStorage.setItem(
    'mowan-idr:v1:1',
    JSON.stringify({ trade_no: 'old-order' })
  )
  vi.mocked(api.check).mockRejectedValue(new Error('order unavailable'))
  const user = userEvent.setup()
  render(<WalletPayment />)
  await user.click(
    await screen.findByRole('button', { name: 'Resume payment' })
  )
  const dialog = within(await screen.findByRole('dialog'))
  expect(
    await dialog.findByText(
      'Unable to confirm payment. Your order is preserved.'
    )
  ).toBeVisible()
  expect(dialog.queryByText('Confirm Payment')).not.toBeInTheDocument()
  expect(
    dialog.queryByRole('button', { name: 'Continue' })
  ).not.toBeInTheDocument()
  expect(
    dialog.queryByText(
      'Payment is completed securely on the official payment page.'
    )
  ).not.toBeInTheDocument()
  await waitFor(() =>
    expect(dialog.getByRole('button', { name: 'Retry' })).toBeEnabled()
  )
  await user.click(dialog.getByRole('button', { name: 'Retry' }))
  expect(api.check).toHaveBeenCalledWith({ trade_no: 'old-order' })
  await user.click(dialog.getByRole('button', { name: 'Close' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(await screen.findByRole('button', { name: /Ten/ })).toBeEnabled()
  expect(api.create).not.toHaveBeenCalled()
  expect(api.pay).not.toHaveBeenCalled()
})

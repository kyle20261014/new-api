import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import * as api from '../api'
import { useIdataRiver } from '../use-payment'

vi.mock('../api', async (original) => ({
  ...(await original<typeof import('../api')>()),
  products: vi.fn(),
  create: vi.fn(),
  check: vi.fn(),
  pay: vi.fn(),
}))
const product: api.Product = {
  sku_id: 'sku',
  name: 'Ten',
  price: 10,
  quota: 12,
  currency: 'USD',
}
const order: api.Order = {
  ...product,
  trade_no: 'order-1',
  request_id: 'request-1',
  status: 'pending',
  platform_status: 'NEW',
  creation_state: 'associated',
  credited: false,
  refund_status: 'none',
  payment_methods: [{ method: 'alipay', name: 'Alipay' }],
  can_pay: true,
  can_check: true,
  can_recreate: false,
  query_error: false,
}
const settled = vi.fn(async () => {})
beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
  vi.mocked(api.products).mockResolvedValue({
    success: true,
    data: { enabled: true, products: [product] },
  })
  vi.mocked(api.check).mockResolvedValue({ success: true, data: order })
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

it('does not create on selection and preserves a lost create response without replay', async () => {
  vi.mocked(api.create).mockRejectedValue(new Error('response lost'))
  vi.mocked(api.check).mockRejectedValue(new Error('cannot confirm yet'))
  const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
  await waitFor(() => expect(result.current.catalog).not.toBeNull())
  act(() => result.current.choose())
  expect(api.create).not.toHaveBeenCalled()
  await act(() => result.current.create(product))
  expect(api.create).toHaveBeenCalledTimes(1)
  expect(result.current.lookup).not.toBeNull()
  expect(localStorage.getItem('mowan-idr:v1:1')).toContain('request_id')
  await act(() => result.current.create(product))
  expect(api.create).toHaveBeenCalledTimes(1)
})

it('keeps the trusted state on query failure and never pays or recreates', async () => {
  const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
  await act(() => result.current.resume({ trade_no: order.trade_no }))
  await waitFor(() => expect(result.current.busy).toBe(false))
  vi.mocked(api.check).mockRejectedValue(new Error('network'))
  await act(() => result.current.query({ trade_no: order.trade_no }))
  expect(result.current.order?.status).toBe('pending')
  expect(result.current.error).toBe(true)
  await act(() => result.current.pay('alipay'))
  expect(api.pay).not.toHaveBeenCalled()
  expect(api.create).not.toHaveBeenCalled()
})

it('retries a rejected creation after remount with the original request ID and SKU', async () => {
  vi.mocked(api.create).mockResolvedValueOnce({ success: false })
  vi.mocked(api.check).mockResolvedValue({ success: false })
  const first = renderHook(() => useIdataRiver(1, undefined, settled))
  await act(() => first.result.current.create(product))
  const original = vi.mocked(api.create).mock.calls[0]
  first.unmount()
  const restored = renderHook(() => useIdataRiver(1, undefined, settled))
  await waitFor(() => expect(restored.result.current.error).toBe(true))
  vi.mocked(api.create).mockResolvedValue({ success: true, data: order })
  await act(() => restored.result.current.create(product, true))
  expect(api.create).toHaveBeenCalledTimes(2)
  expect(vi.mocked(api.create).mock.calls[1]).toEqual(original)
  expect(restored.result.current.order?.trade_no).toBe(order.trade_no)
})

it('retries an unconfirmed response only with the original identity and SKU', async () => {
  vi.mocked(api.create).mockRejectedValue(new Error('response lost'))
  vi.mocked(api.check).mockRejectedValue(new Error('unknown'))
  const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
  await act(() => result.current.create(product))
  const original = vi.mocked(api.create).mock.calls[0]
  await act(() => result.current.create({ sku_id: 'different' }, true))
  expect(api.create).toHaveBeenCalledTimes(1)
  await act(() => result.current.create(product, true))
  expect(vi.mocked(api.create).mock.calls[1]).toEqual(original)
  expect(localStorage.getItem('mowan-idr:v1:1')).toContain(original[1])
})

it('never retries create for an already persisted unknown order', async () => {
  const unknown: api.Order = {
    ...order,
    creation_state: 'unknown',
    platform_status: '',
    can_pay: false,
    query_error: true,
  }
  vi.mocked(api.create).mockResolvedValue({ success: true, data: unknown })
  vi.mocked(api.check).mockResolvedValue({ success: true, data: unknown })
  const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
  await act(() => result.current.create(product))
  await act(() => result.current.create(product, true))
  expect(api.create).toHaveBeenCalledTimes(1)
  expect(result.current.lookup).toEqual({ trade_no: order.trade_no })
})

it('does not restore another account lookup', async () => {
  localStorage.setItem(
    'mowan-idr:v1:1',
    JSON.stringify({ trade_no: 'private-order' })
  )
  const { result } = renderHook(() => useIdataRiver(2, undefined, settled))
  await waitFor(() => expect(result.current.catalog).not.toBeNull())
  expect(result.current.lookup).toBeNull()
  expect(api.check).not.toHaveBeenCalled()
})

it('keeps the original order when getting a payment link fails', async () => {
  vi.mocked(api.pay).mockRejectedValue(new Error('link unavailable'))
  const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
  await act(() => result.current.resume({ trade_no: order.trade_no }))
  await waitFor(() => expect(result.current.busy).toBe(false))
  await act(() => result.current.pay('alipay'))
  expect(api.pay).toHaveBeenCalledWith(order.trade_no, 'alipay')
  expect(result.current.error).toBe(true)
  expect(result.current.order?.trade_no).toBe(order.trade_no)
  expect(localStorage.getItem('mowan-idr:v1:1')).toContain(order.trade_no)
  expect(api.create).not.toHaveBeenCalled()
})

it('accepts a trusted terminal state returned instead of a payment link', async () => {
  const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
  await act(() => result.current.resume({ trade_no: order.trade_no }))
  await waitFor(() => expect(result.current.busy).toBe(false))
  vi.mocked(api.pay).mockResolvedValue({
    success: true,
    data: {
      ...order,
      status: 'success',
      platform_status: 'DONE',
      credited: true,
      can_pay: false,
    },
  })
  await act(() => result.current.pay('alipay'))
  expect(result.current.order?.credited).toBe(true)
  expect(result.current.error).toBe(false)
  expect(localStorage.getItem('mowan-idr:v1:1')).toBeNull()
  expect(settled).toHaveBeenCalled()
})

it.each(['removed channel', 'already paid', 'expired'])(
  'rechecks before paying: %s',
  async (reason) => {
    const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
    await act(() => result.current.resume({ trade_no: order.trade_no }))
    await waitFor(() => expect(result.current.busy).toBe(false))
    const current: api.Order = { ...order }
    if (reason === 'removed channel') current.payment_methods = []
    else {
      current.can_pay = false
      current.status = reason === 'already paid' ? 'success' : 'expired'
      current.platform_status = reason === 'already paid' ? 'DONE' : 'EXPIRED'
      current.credited = reason === 'already paid'
    }
    vi.mocked(api.check).mockResolvedValue({ success: true, data: current })
    await act(() => result.current.pay('alipay'))
    expect(api.pay).not.toHaveBeenCalled()
    expect(api.create).not.toHaveBeenCalled()
    expect(result.current.order?.platform_status).toBe(current.platform_status)
    if (reason !== 'removed channel') {
      expect(localStorage.getItem('mowan-idr:v1:1')).toBeNull()
    }
  }
)

it('rejects a stale account response without exposing its order', async () => {
  let finish:
    | ((value: Awaited<ReturnType<typeof api.check>>) => void)
    | undefined
  vi.mocked(api.check).mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  const { result, rerender } = renderHook(
    ({ id }) => useIdataRiver(id, undefined, settled),
    { initialProps: { id: 1 } }
  )
  act(() => {
    void result.current.resume({ trade_no: 'private-order' })
  })
  rerender({ id: 2 })
  await act(async () => {
    finish?.({ success: true, data: order })
  })
  expect(result.current.order).toBeNull()
  expect(result.current.lookup).toBeNull()
  expect(localStorage.getItem('mowan-idr:v1:2')).toBeNull()
})

it('invalidates an in-flight quote when the dialog closes', async () => {
  let finish!: (value: Awaited<ReturnType<typeof api.pay>>) => void
  vi.mocked(api.pay).mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
  await act(() => result.current.resume({ trade_no: order.trade_no }))
  await waitFor(() => expect(result.current.busy).toBe(false))
  let pending!: Promise<void>
  act(() => {
    pending = result.current.pay('alipay')
  })
  await waitFor(() => expect(api.pay).toHaveBeenCalledTimes(1))
  act(() => result.current.setOpen(false))
  await act(async () => {
    finish({
      success: true,
      data: {
        amount: 0.0002,
        pay_currency: 'BTC',
        pay_url: 'https://example.test/pay',
      },
    })
    await pending
  })
  expect(result.current.payment).toBeNull()
  expect(result.current.lookup).toEqual({ trade_no: order.trade_no })
})

it('discards a quote on a trusted terminal update and cannot navigate it', async () => {
  vi.mocked(api.pay).mockResolvedValue({
    success: true,
    data: {
      amount: 0.0002,
      pay_currency: 'BTC',
      pay_url: 'https://example.test/pay',
    },
  })
  const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
  await act(() => result.current.resume({ trade_no: order.trade_no }))
  await waitFor(() => expect(result.current.busy).toBe(false))
  await act(() => result.current.pay('alipay'))
  expect(result.current.payment?.amount).toBe(0.0002)
  vi.mocked(api.check).mockResolvedValue({
    success: true,
    data: {
      ...order,
      status: 'success',
      platform_status: 'DONE',
      credited: true,
      can_pay: false,
    },
  })
  await act(() => result.current.query({ trade_no: order.trade_no }))
  expect(result.current.payment).toBeNull()
  expect(result.current.lookup).toBeNull()
})

it('rejects a different channel and consumes the confirmed link only once', async () => {
  const assign = vi.fn()
  vi.stubGlobal('location', { ...window.location, assign })
  try {
    vi.mocked(api.pay).mockResolvedValue({
      success: true,
      data: {
        amount: 10,
        pay_currency: 'USD',
        pay_url: 'https://example.test/pay',
      },
    })
    const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
    await act(() => result.current.resume({ trade_no: order.trade_no }))
    await waitFor(() => expect(result.current.busy).toBe(false))
    await act(() => result.current.pay('alipay'))
    act(() => result.current.continuePayment('crypto'))
    expect(assign).not.toHaveBeenCalled()
    act(() => {
      result.current.continuePayment('alipay')
      result.current.continuePayment('alipay')
    })
    expect(assign).toHaveBeenCalledExactlyOnceWith('https://example.test/pay')
    expect(api.create).not.toHaveBeenCalled()
  } finally {
    vi.unstubAllGlobals()
  }
})

it('stops automatic queries after twelve attempts and preserves manual recovery', async () => {
  vi.useFakeTimers()
  vi.mocked(api.check).mockRejectedValue(new Error('offline'))
  localStorage.setItem(
    'mowan-idr:v1:1',
    JSON.stringify({ trade_no: order.trade_no })
  )
  const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
  await act(() => vi.advanceTimersByTimeAsync(120000))
  expect(api.check).toHaveBeenCalledTimes(12)
  expect(result.current.lookup).not.toBeNull()
  await act(() => result.current.query({ trade_no: order.trade_no }))
  expect(api.check).toHaveBeenCalledTimes(13)
})

it.each(['focus first', 'visibility first'])(
  'recovers the original order after exhausted polling and query errors: %s',
  async (sequence) => {
    vi.useFakeTimers()
    let visibility: DocumentVisibilityState = 'hidden'
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(
      () => visibility
    )
    localStorage.setItem(
      'mowan-idr:v1:1',
      JSON.stringify({ trade_no: order.trade_no })
    )
    vi.mocked(api.check)
      .mockResolvedValueOnce({ success: true, data: order })
      .mockRejectedValue(new Error('offline'))
    const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
    await act(() => vi.advanceTimersByTimeAsync(120000))
    expect(api.check).toHaveBeenCalledTimes(12)
    expect(result.current.error).toBe(true)
    expect(result.current.order?.trade_no).toBe(order.trade_no)
    act(() => {
      window.dispatchEvent(new Event('focus'))
      document.dispatchEvent(new Event('visibilitychange'))
    })
    expect(api.check).toHaveBeenCalledTimes(12)
    vi.mocked(api.check).mockResolvedValue({
      success: true,
      data: {
        ...order,
        status: 'success',
        platform_status: 'DONE',
        credited: true,
        can_pay: false,
      },
    })
    await act(async () => {
      if (sequence === 'focus first') window.dispatchEvent(new Event('focus'))
      visibility = 'visible'
      document.dispatchEvent(new Event('visibilitychange'))
      if (sequence === 'visibility first') {
        window.dispatchEvent(new Event('focus'))
      }
    })
    expect(api.check).toHaveBeenCalledTimes(13)
    expect(api.check).toHaveBeenLastCalledWith({ trade_no: order.trade_no })
    expect(result.current.order?.credited).toBe(true)
    expect(result.current.lookup).toBeNull()
    expect(settled).toHaveBeenCalledTimes(1)
    await act(() => vi.advanceTimersByTimeAsync(120000))
    act(() => {
      window.dispatchEvent(new Event('focus'))
      document.dispatchEvent(new Event('visibilitychange'))
    })
    expect(api.check).toHaveBeenCalledTimes(13)
    expect(api.create).not.toHaveBeenCalled()
    expect(api.pay).not.toHaveBeenCalled()
  }
)

it('keeps failed original attempts through repeated returns without restarting bounded polling', async () => {
  vi.useFakeTimers()
  let visibility: DocumentVisibilityState = 'hidden'
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(
    () => visibility
  )
  const original = { request_id: 'lost-request', sku_id: product.sku_id }
  localStorage.setItem('mowan-idr:v1:1', JSON.stringify(original))
  vi.mocked(api.check).mockRejectedValue(new Error('offline'))
  const { result } = renderHook(() => useIdataRiver(1, undefined, settled))
  await act(() => vi.advanceTimersByTimeAsync(120000))
  expect(api.check).toHaveBeenCalledTimes(12)
  for (let count = 1; count <= 2; count++) {
    await act(async () => {
      visibility = 'hidden'
      document.dispatchEvent(new Event('visibilitychange'))
      window.dispatchEvent(new Event('focus'))
      visibility = 'visible'
      document.dispatchEvent(new Event('visibilitychange'))
      window.dispatchEvent(new Event('focus'))
    })
    expect(api.check).toHaveBeenCalledTimes(12 + count)
    expect(api.check).toHaveBeenLastCalledWith(original)
    expect(result.current.lookup).toEqual(original)
    expect(result.current.error).toBe(true)
    await act(() => vi.advanceTimersByTimeAsync(120000))
    expect(api.check).toHaveBeenCalledTimes(12 + count)
  }
  expect(api.create).not.toHaveBeenCalled()
  expect(localStorage.getItem('mowan-idr:v1:1')).toBe(JSON.stringify(original))
})

it('coalesces recovery while a query is pending and removes both listeners on unmount', async () => {
  vi.useFakeTimers()
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  localStorage.setItem(
    'mowan-idr:v1:1',
    JSON.stringify({ trade_no: order.trade_no })
  )
  let finish!: (value: Awaited<ReturnType<typeof api.check>>) => void
  vi.mocked(api.check).mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  const view = renderHook(() => useIdataRiver(1, undefined, settled))
  act(() => {
    document.dispatchEvent(new Event('visibilitychange'))
    window.dispatchEvent(new Event('focus'))
    document.dispatchEvent(new Event('visibilitychange'))
  })
  expect(api.check).toHaveBeenCalledTimes(1)
  await act(async () => {
    finish({ success: true, data: order })
  })
  view.unmount()
  await act(async () => {
    document.dispatchEvent(new Event('visibilitychange'))
    window.dispatchEvent(new Event('focus'))
    await vi.advanceTimersByTimeAsync(120000)
  })
  expect(api.check).toHaveBeenCalledTimes(1)
})

it('does not recover the previous account order after switching accounts', async () => {
  vi.useFakeTimers()
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  localStorage.setItem(
    'mowan-idr:v1:1',
    JSON.stringify({ trade_no: order.trade_no })
  )
  const view = renderHook(({ id }) => useIdataRiver(id, undefined, settled), {
    initialProps: { id: 1 },
  })
  await act(() => vi.advanceTimersByTimeAsync(120000))
  const before = vi.mocked(api.check).mock.calls.length
  view.rerender({ id: 2 })
  await act(async () => {
    document.dispatchEvent(new Event('visibilitychange'))
    window.dispatchEvent(new Event('focus'))
    await vi.advanceTimersByTimeAsync(120000)
  })
  expect(api.check).toHaveBeenCalledTimes(before)
  expect(view.result.current.lookup).toBeNull()
  expect(view.result.current.order).toBeNull()
})

it.each([
  'javascript:alert(1)',
  'data:text/html,evil',
  'https://user:pass@example.com',
])('rejects an unsafe payment URL: %s', (url) => {
  expect(api.paymentUrl(url)).toBeNull()
})

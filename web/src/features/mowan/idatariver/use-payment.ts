import { useCallback, useEffect, useRef, useState } from 'react'

import * as client from './api'
import type { Order, OrderLookup, Product } from './api'

function readLookup(key: string): OrderLookup | null {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(key) || 'null')
    if (value && typeof value === 'object') {
      if (
        'trade_no' in value &&
        typeof value.trade_no === 'string' &&
        value.trade_no.length <= 128
      ) {
        return { trade_no: value.trade_no }
      }
      if (
        'request_id' in value &&
        typeof value.request_id === 'string' &&
        value.request_id.length <= 128
      ) {
        return {
          request_id: value.request_id,
          sku_id:
            'sku_id' in value &&
            typeof value.sku_id === 'string' &&
            value.sku_id.length <= 128
              ? value.sku_id
              : undefined,
        }
      }
    }
  } catch {
    /* History remains available if browser storage is unavailable. */
  }
  return null
}

export function useIdataRiver(
  userId: number | undefined,
  initialTradeNo: string | undefined,
  onSettled: () => Promise<void>
) {
  const [catalog, setCatalog] = useState<{
    enabled: boolean
    products: Product[]
  } | null>(null)
  const [catalogError, setCatalogError] = useState(false)
  const [loading, setLoading] = useState(false)
  const [order, setOrder] = useState<Order | null>(null)
  const [lookup, setLookup] = useState<OrderLookup | null>(null)
  const currentLookup = useRef('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(false)
  const paymentVersion = useRef(0)
  const clearPayment = useCallback(() => {
    paymentVersion.current++
  }, [])
  const [open, updateOpen] = useState(false)
  const setOpen = (value: boolean) => {
    if (!value) clearPayment()
    updateOpen(value)
  }
  const flight = useRef(false)
  useEffect(() => {
    const restore = (event: PageTransitionEvent) => {
      if (!event.persisted) return
      clearPayment()
      flight.current = false
      setBusy(false)
    }
    window.addEventListener('pageshow', restore)
    return () => {
      clearPayment()
      window.removeEventListener('pageshow', restore)
    }
  }, [clearPayment])
  const account = useRef(userId)
  account.current = userId
  const storageKey = userId ? `mowan-idr:v1:${userId}` : ''
  const save = useCallback(
    (value: OrderLookup | null) => {
      currentLookup.current = value ? JSON.stringify(value) : ''
      setLookup(value)
      try {
        if (storageKey) {
          if (value) localStorage.setItem(storageKey, JSON.stringify(value))
          else localStorage.removeItem(storageKey)
        }
      } catch {
        /* The server's account-scoped order history is the fallback. */
      }
    },
    [storageKey]
  )

  const load = useCallback(async () => {
    if (!userId) return
    setLoading(true)
    setCatalogError(false)
    try {
      const res = await client.products()
      if (account.current !== userId) return
      if (!res.success || !res.data) throw new Error()
      setCatalog(res.data)
    } catch {
      if (account.current === userId) setCatalogError(true)
    } finally {
      if (account.current === userId) setLoading(false)
    }
  }, [userId])

  const accept = useCallback(
    (value: Order) => {
      setOrder(value)
      setError(value.query_error)
      save(client.pending(value) ? { trade_no: value.trade_no } : null)
      if (value.credited || value.platform_status === 'REFUND') void onSettled()
    },
    [save, onSettled]
  )

  const query = useCallback(
    async (value: OrderLookup) => {
      if (flight.current || !userId) return null
      flight.current = true
      setBusy(true)
      try {
        const res = await client.check(value)
        if (account.current !== userId) return null
        if (!res.success || !res.data) throw new Error()
        accept(res.data)
        return res.data
      } catch {
        if (account.current === userId) {
          clearPayment()
          setError(true)
        }
        return null
      } finally {
        flight.current = false
        if (account.current === userId) setBusy(false)
      }
    },
    [userId, accept, clearPayment]
  )

  useEffect(() => {
    setOrder(null)
    clearPayment()
    setError(false)
    updateOpen(false)
    setCatalog(null)
    const saved = storageKey ? readLookup(storageKey) : null
    const restored = initialTradeNo ? { trade_no: initialTradeNo } : saved
    currentLookup.current = restored ? JSON.stringify(restored) : ''
    setLookup(restored)
    if (initialTradeNo && userId) updateOpen(true)
    void load()
  }, [userId, storageKey, initialTradeNo, load, clearPayment])

  const lookupId = lookup ? JSON.stringify(lookup) : ''
  useEffect(() => {
    if (!open || !lookupId || !userId || lookupId !== currentLookup.current) {
      return
    }
    const value: OrderLookup = JSON.parse(lookupId)
    let stopped = false
    let tries = 0
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = async () => {
      const current = await query(value)
      tries++
      if (!stopped && tries < 12 && (!current || client.pending(current))) {
        timer = setTimeout(poll, 5000)
      }
    }
    void poll()
    const recover = () => {
      if (!stopped && document.visibilityState === 'visible') void query(value)
    }
    window.addEventListener('focus', recover)
    document.addEventListener('visibilitychange', recover)
    return () => {
      stopped = true
      clearTimeout(timer)
      window.removeEventListener('focus', recover)
      document.removeEventListener('visibilitychange', recover)
    }
  }, [open, lookupId, query, userId])

  const create = async (product: Pick<Product, 'sku_id'>, retry = false) => {
    if (!userId || flight.current) return
    if (
      lookup &&
      (!retry ||
        !('request_id' in lookup) ||
        (lookup.sku_id && lookup.sku_id !== product.sku_id))
    ) {
      return
    }
    const request_id =
      lookup && 'request_id' in lookup ? lookup.request_id : crypto.randomUUID()
    // Persist before sending: a lost response must never trigger another create.
    // Manual retries reuse this identity; the server never replays a persisted attempt.
    save({ request_id, sku_id: product.sku_id })
    flight.current = true
    setBusy(true)
    setError(false)
    try {
      const res = await client.create(product.sku_id, request_id)
      if (account.current !== userId) return
      if (!res.success || !res.data) throw new Error()
      accept(res.data)
    } catch {
      if (account.current === userId) setError(true)
    } finally {
      flight.current = false
      if (account.current === userId) setBusy(false)
    }
  }

  const resume = async (value: OrderLookup) => {
    if (flight.current) return
    setOrder(null)
    clearPayment()
    setError(false)
    setOpen(true)
    save(value)
    await query(value)
  }

  const pay = async (method: string) => {
    if (!order || flight.current || !userId) return
    clearPayment()
    const version = paymentVersion.current
    const current = await query({ trade_no: order.trade_no })
    if (
      account.current !== userId ||
      version !== paymentVersion.current ||
      !current?.can_pay ||
      current.query_error ||
      !current.payment_methods.some((m) => m.method === method)
    ) {
      return
    }
    flight.current = true
    setBusy(true)
    let leaving = false
    try {
      const res = await client.pay(current.trade_no, method)
      if (account.current !== userId || version !== paymentVersion.current) {
        return
      }
      if (!res.success || !res.data) throw new Error()
      if ('trade_no' in res.data) accept(res.data)
      if (!('pay_url' in res.data)) return
      const url = client.paymentUrl(res.data.pay_url)
      if (!url) throw new Error()
      save({ trade_no: current.trade_no })
      window.location.assign(url)
      leaving = true
    } catch {
      if (account.current === userId) setError(true)
    } finally {
      if (!leaving) {
        flight.current = false
        if (account.current === userId) setBusy(false)
      }
    }
  }

  const choose = () => {
    if (flight.current) return
    // A deliberate package selection starts a new purchase; the old order stays in history.
    save(null)
    setOrder(null)
    clearPayment()
    setError(false)
    setOpen(true)
  }
  return {
    catalog,
    catalogError,
    loading,
    load,
    order,
    lookup,
    busy,
    error,
    open,
    setOpen,
    choose,
    create,
    resume,
    pay,
    clearPayment,
    query,
  }
}

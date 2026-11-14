import type { ApiResponse } from '@/features/wallet/types'
import { api } from '@/lib/api'

export interface Product {
  sku_id: string
  name: string
  price: number
  quota: number
  currency: string
}

export interface Order extends Product {
  request_id: string
  trade_no: string
  status: 'pending' | 'success' | 'expired'
  platform_status: '' | 'NEW' | 'DONE' | 'EXPIRED' | 'REFUND'
  creation_state: 'creating' | 'unknown' | 'associated'
  credited: boolean
  refund_status: 'none' | 'before_credit' | 'recovered' | 'manual_required'
  payment_methods: { method: string; name: string }[]
  can_pay: boolean
  can_check: boolean
  can_recreate: boolean
  query_error: boolean
}

export type OrderLookup =
  | { trade_no: string }
  | { request_id: string; sku_id?: string }
export interface Payment {
  pay_url: string
  pay_currency: string | null
  amount: number | null
}

const base = '/api/mowan/idatariver'
export async function products() {
  return (
    await api.get<ApiResponse<{ enabled: boolean; products: Product[] }>>(
      `${base}/products`
    )
  ).data
}
export async function create(sku_id: string, request_id: string) {
  return (
    await api.post<ApiResponse<Order>>(`${base}/order`, { sku_id, request_id })
  ).data
}
export async function check(lookup: OrderLookup) {
  return (await api.post<ApiResponse<Order>>(`${base}/check`, lookup)).data
}
export async function pay(trade_no: string, method: string) {
  return (
    await api.post<ApiResponse<Payment | Order>>(`${base}/pay`, {
      trade_no,
      method,
    })
  ).data
}

export function paymentUrl(value: string): string | null {
  try {
    const url = new URL(value)
    return ['https:', 'http:'].includes(url.protocol) &&
      !url.username &&
      !url.password
      ? url.href
      : null
  } catch {
    return null
  }
}

export function pending(order: Order) {
  return order.status === 'pending' && order.refund_status === 'none'
}

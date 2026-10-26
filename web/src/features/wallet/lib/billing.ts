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
import type { StatusBadgeProps } from '@/components/status-badge'
import { formatCurrencyFromUSD, formatQuotaWithCurrency } from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'

import type { TopupRecord, TopupRefundStatus, TopupStatus } from '../types'

// ============================================================================
// Billing Utility Functions
// ============================================================================

interface StatusConfig {
  variant: StatusBadgeProps['variant']
  label: string
}

/**
 * Status badge configuration
 */
export const STATUS_CONFIG: Record<TopupStatus, StatusConfig> = {
  success: {
    variant: 'success',
    label: 'Success',
  },
  pending: {
    variant: 'warning',
    label: 'Pending',
  },
  expired: {
    variant: 'danger',
    label: 'Expired',
  },
}

/**
 * Get status badge configuration
 */
export function getStatusConfig(status: TopupStatus): StatusConfig {
  return STATUS_CONFIG[status] || STATUS_CONFIG.pending
}

export interface RefundStatusConfig {
  variant: StatusBadgeProps['variant']
  labelKey: string
}

/**
 * Refund status badge configuration
 */
export const REFUND_STATUS_CONFIG: Record<
  TopupRefundStatus,
  RefundStatusConfig | null
> = {
  none: null,
  recovered: {
    variant: 'warning',
    labelKey: 'Refunded (Recovered)',
  },
  manual_required: {
    variant: 'danger',
    labelKey: 'Refund Pending Review',
  },
  refunded_before_credit: {
    variant: 'neutral',
    labelKey: 'Refunded (Before Credit)',
  },
}

/**
 * Get refund status configuration
 */
export function getRefundStatusConfig(
  status?: string | null
): RefundStatusConfig | null {
  if (!status || status === 'none') return null
  return (
    REFUND_STATUS_CONFIG[status as TopupRefundStatus] || {
      variant: 'warning',
      labelKey: status,
    }
  )
}

/**
 * Payment method display names
 */
export const PAYMENT_METHOD_NAMES: Record<string, string> = {
  stripe: 'Stripe',
  alipay: 'Alipay',
  wxpay: 'WeChat Pay',
  waffo: 'Waffo',
  creem: 'Creem',
  idatariver: 'iDataRiver',
}

/**
 * Get payment method display name
 */
export function getPaymentMethodName(
  method: string,
  t?: (key: string) => string
): string {
  const name = PAYMENT_METHOD_NAMES[method] || method
  return t ? t(name) : name
}

/**
 * Whether a topup record stores actual internal quota in the amount field (Creem / iDataRiver convention)
 */
export function isInternalQuotaRecord(record: TopupRecord): boolean {
  return (
    record.payment_provider === 'creem' ||
    record.payment_method === 'creem' ||
    record.payment_provider === 'idatariver' ||
    record.payment_method === 'idatariver'
  )
}

/**
 * Format topup record amount considering internal quota vs USD storage
 */
export function formatTopupRecordAmount(record: TopupRecord): string {
  if (isInternalQuotaRecord(record)) {
    return formatQuotaWithCurrency(record.amount, {
      digitsLarge: 2,
      digitsSmall: 2,
      abbreviate: false,
    })
  }
  return formatCurrencyFromUSD(record.amount, {
    digitsLarge: 2,
    digitsSmall: 2,
    abbreviate: false,
  })
}

/**
 * Format timestamp to readable date string
 */
export function formatTimestamp(timestamp: number): string {
  return formatTimestampToDate(timestamp)
}

export type TopupOutcome =
  | { kind: 'refunded_recovered'; messageKey: string }
  | { kind: 'refunded_manual'; messageKey: string }
  | { kind: 'refunded_before_credit'; messageKey: string }
  | { kind: 'success'; messageKey: string }
  | { kind: 'pending'; messageKey: string }
  | { kind: 'expired'; messageKey: string }
  | { kind: 'unknown'; messageKey: string }

/**
 * Evaluates the user-facing outcome of an order check, checking refund_status FIRST
 * to avoid misreporting refunded orders (which keep status='success' in DB) as successful credits.
 */
export function evaluateTopupOutcome(
  status: string,
  refundStatus?: string | null
): TopupOutcome {
  if (refundStatus === 'recovered') {
    return {
      kind: 'refunded_recovered',
      messageKey: 'Order has been refunded and balance recovered.',
    }
  }
  if (refundStatus === 'manual_required') {
    return {
      kind: 'refunded_manual',
      messageKey: 'Order refund requires manual review.',
    }
  }
  if (refundStatus === 'refunded_before_credit') {
    return {
      kind: 'refunded_before_credit',
      messageKey: 'Order was refunded before credit.',
    }
  }

  if (status === 'success') {
    return {
      kind: 'success',
      messageKey: 'Payment confirmed and balance updated!',
    }
  }
  if (status === 'pending') {
    return {
      kind: 'pending',
      messageKey: 'Payment is still pending. Please try again after completing payment.',
    }
  }
  if (status === 'expired') {
    return {
      kind: 'expired',
      messageKey: 'Order has expired.',
    }
  }

  return {
    kind: 'unknown',
    messageKey: 'Order status: {{status}}',
  }
}

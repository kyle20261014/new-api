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
/**
 * Validates that a payment URL uses safe HTTP or HTTPS protocol
 */
export function isSafePaymentUrl(url: string): boolean {
  if (!url || typeof url !== 'string') return false
  const trimmed = url.trim()
  try {
    const parsed = new URL(trimmed)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:'
  } catch {
    return false
  }
}

/**
 * Checks if a payment method name or method identifier represents Alipay or WeChat Pay
 */
export function isAlipayOrWeChat(method: string, name?: string): boolean {
  const normalizedMethod = (method || '').toLowerCase()
  const normalizedName = (name || '').toLowerCase()
  return (
    normalizedMethod.includes('alipay') ||
    normalizedMethod.includes('wx') ||
    normalizedMethod.includes('wechat') ||
    normalizedName.includes('alipay') ||
    normalizedName.includes('wechat') ||
    normalizedName.includes('微信') ||
    normalizedName.includes('支付宝')
  )
}

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
import { Sparkles } from 'lucide-react'
import { memo } from 'react'
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getModelDiscount } from '../lib/model-helpers'
import type { PricingModel } from '../types'

interface ModelDiscountBadgeProps {
  model: PricingModel
  selectedGroup?: string
  usableGroup?: Record<string, unknown>
  appearance?: 'default' | 'card' | 'compact' | 'caption'
  className?: string
}

export const ModelDiscountBadge = memo(function ModelDiscountBadge(
  props: ModelDiscountBadgeProps
) {
  const { t, i18n } = useTranslation()
  const discountInfo = getModelDiscount(
    props.model,
    props.selectedGroup,
    props.usableGroup
  )

  if (!discountInfo) {
    return null
  }

  let label: string

  if (discountInfo.isFree) {
    label = t('Free to use')
  } else {
    const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
    const formattedDiscount = formatNumber(discountInfo.discount, locale, {
      maximumSignificantDigits: 17,
      notation: discountInfo.discount < 0.000001 ? 'scientific' : 'standard',
    })
    const rawOff = (1 - discountInfo.ratio) * 100
    const precisionOff = Number(rawOff.toPrecision(12))
    // Remove arithmetic noise (0.995 -> 0.5% off) without rounding to 100% off.
    const off = precisionOff === 100 ? rawOff : precisionOff

    if (rawOff === 100 && !locale?.startsWith('zh')) {
      // Subtraction has lost the positive payment ratio; display it directly.
      const paid = Number((discountInfo.ratio * 100).toPrecision(12))
      const values = {
        paid: formatNumber(paid, locale, {
          maximumSignificantDigits: 17,
          notation: paid < 0.000001 ? 'scientific' : 'standard',
        }),
        discount: formattedDiscount,
      }
      label = discountInfo.isLowest
        ? t('Pay from {{paid}}%', values)
        : t('Pay {{paid}}%', values)
    } else {
      const values = {
        discount: formattedDiscount,
        off: formatNumber(off, locale, {
          maximumSignificantDigits: 17,
          notation: off < 0.000001 ? 'scientific' : 'standard',
        }),
      }
      label = discountInfo.isLowest
        ? t('As low as {{discount}} discount', values)
        : t('{{discount}} discount', values)
    }
  }

  const specialOfferText = t('Special Offer')
  const ariaLabel = `${specialOfferText}: ${label}`

  if (props.appearance === 'card') {
    return (
      <div
        role='status'
        aria-label={ariaLabel}
        className={cn(
          'flex min-w-0 max-w-full flex-col items-end justify-center rounded-xl border px-3 py-1.5 transition-colors select-none',
          'border-primary/25 bg-primary/8 shadow-xs',
          'dark:border-primary/30 dark:bg-primary/12',
          props.className
        )}
      >
        <div className='flex items-center gap-1 text-[11px] font-medium leading-none text-primary/85 dark:text-primary/90'>
          <Sparkles aria-hidden className='size-3 shrink-0 text-primary' />
          <span>{specialOfferText}</span>
        </div>
        <span className='mt-1 max-w-full text-right text-base sm:text-lg font-bold tracking-tight text-primary leading-tight [overflow-wrap:anywhere]'>
          {label}
        </span>
      </div>
    )
  }

  return (
    <div
      role='status'
      aria-label={ariaLabel}
      className={cn(
        'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-xs font-medium transition-colors select-none shrink-0',
        'border-primary/25 bg-primary/8 text-primary dark:border-primary/30 dark:bg-primary/12',
        props.className
      )}
    >
      <Sparkles aria-hidden className='size-3 shrink-0 text-primary' />
      <span>{label}</span>
    </div>
  )
})

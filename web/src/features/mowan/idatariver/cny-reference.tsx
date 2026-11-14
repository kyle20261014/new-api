import { useTranslation } from 'react-i18next'

import { useSystemConfigStore } from '@/stores/system-config-store'

import { formatPaymentAmount } from './format'

export function CnyReference(props: { usd: number }) {
  const { t, i18n } = useTranslation()
  const currency = useSystemConfigStore((state) => state.config.currency)
  if (currency.quotaDisplayType !== 'CNY') return null
  const amount = props.usd * currency.usdExchangeRate
  const available =
    currency.usdExchangeRateValid === true &&
    Number.isFinite(currency.usdExchangeRate) &&
    currency.usdExchangeRate > 0 &&
    Number.isFinite(amount) &&
    amount > 0
  return (
    <span className='text-muted-foreground inline-block max-w-full text-xs font-normal break-all'>
      {available
        ? `≈ ¥${formatPaymentAmount(amount, i18n.resolvedLanguage || i18n.language)}`
        : t('CNY reference unavailable')}
    </span>
  )
}

export function CnyReferenceNote() {
  const { t } = useTranslation()
  const enabled = useSystemConfigStore(
    (state) => state.config.currency.quotaDisplayType === 'CNY'
  )
  if (!enabled) return null
  return (
    <p className='text-muted-foreground text-xs break-words'>
      {t(
        'CNY estimates use the platform display rate for reference only. Actual payment is determined by the payment provider.'
      )}
    </p>
  )
}

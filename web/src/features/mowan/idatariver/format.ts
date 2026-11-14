import { toIntlLocale } from '@/i18n/languages'

// Fixed-fraction currency helpers clamp tiny values; provider amounts need significant digits and no conversion.
export function formatPaymentAmount(value: number, language?: string): string {
  return new Intl.NumberFormat(toIntlLocale(language), {
    maximumSignificantDigits: 21,
  }).format(value)
}

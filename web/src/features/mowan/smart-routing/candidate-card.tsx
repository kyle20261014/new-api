import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Checkbox } from '@/components/ui/checkbox'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { routingReasonLabel } from './labels'
import type { RoutingCandidate } from './types'

export function CandidateCard(props: {
  candidate: RoutingCandidate
  selected?: boolean
  onSelect?: (checked: boolean) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const candidate = props.candidate
  const metrics = candidate.metrics
  const number = (value: number) => formatNumber(value, locale)
  const priceNumber = (value: number) =>
    new Intl.NumberFormat(locale, {
      style: 'currency',
      currency: candidate.quote.currency,
      maximumFractionDigits: 8,
    }).format(value)
  const priceLabels: Record<string, string> = {
    input: t('Input tokens'),
    output: t('Output tokens'),
    cache_read: t('Cache read price'),
    cache_write: t('Cache write'),
    cache_write_1h: t('Cache Write (1h)'),
    cache_write_12h: t('Cache Write (12h)'),
    image_input: t('Image input'),
    audio_input: t('Audio input'),
    audio_output: t('Audio output'),
    billing_expression: t('Billing expression'),
    request: t('Request'),
    request_rule: t('Request pricing rule'),
  }
  const units: Record<string, string> = {
    'USD/1M tokens': t('1M token'),
    'USD/request': t('Request'),
    'USD/image': t('Image'),
    'USD (expression)': t('Expression'),
    'USD (task usage)': t('Task'),
    multiplier: t('Multiplier'),
  }
  const priceLines = candidate.quote.prices.map((price) => {
    let amount = t('Unknown')
    const formatAmount =
      price.unit === 'multiplier'
        ? (value: number) =>
            new Intl.NumberFormat(locale, { maximumFractionDigits: 8 }).format(
              value
            )
        : priceNumber
    if (typeof price.amount === 'number' && Number.isFinite(price.amount)) {
      amount = formatAmount(price.amount)
    } else if (
      typeof price.min === 'number' &&
      typeof price.max === 'number' &&
      Number.isFinite(price.min) &&
      Number.isFinite(price.max)
    ) {
      amount = `${formatAmount(price.min)} – ${formatAmount(price.max)}`
    }
    return `${priceLabels[price.label] ?? price.label}: ${amount} / ${units[price.unit] ?? price.unit}${price.rule ? ` · ${price.rule}` : ''}`
  })
  let responseLabel = t('Response · 1h')
  if (metrics.response_kind === 'ttft') responseLabel = t('First token · 1h')
  if (metrics.response_kind === 'task_terminal') {
    responseLabel = t('Task completion · 1h')
  }
  const healthLabels = {
    normal: t('Healthy'),
    congested: t('Congested'),
    soft_offline: t('Temporarily offline'),
    unknown: t('Unknown'),
    degraded: t('Degraded'),
  }
  return (
    <article className='bg-card min-w-0 space-y-3 rounded-lg border p-3 text-sm'>
      <div className='flex min-w-0 items-start justify-between gap-2'>
        <label className='flex min-h-11 min-w-0 flex-1 items-center gap-3'>
          {props.onSelect && (
            <Checkbox
              checked={!!props.selected}
              disabled={!candidate.available && !props.selected}
              onCheckedChange={(checked) => props.onSelect?.(checked === true)}
              aria-label={t('Select {{model}} / {{group}}', {
                model: candidate.model,
                group: candidate.group,
              })}
            />
          )}
          <span className='min-w-0 font-medium break-words'>
            {candidate.group}
            <span className='text-muted-foreground mt-1 block text-xs'>
              {candidate.display_name || candidate.model}
            </span>
          </span>
        </label>
        <span className='text-muted-foreground max-w-36 text-xs break-words'>
          {healthLabels[candidate.health]}
          {!candidate.available && ` · ${t('Unavailable')}`}
        </span>
      </div>
      <div className='flex items-start justify-between gap-2'>
        <div className='min-w-0 space-y-1 text-xs break-words'>
          {priceLines.map((line) => (
            <p key={line}>{line}</p>
          ))}
          {priceLines.length === 0 && <p>{t('Price unavailable')}</p>}
          {typeof candidate.quote.estimated_total === 'number' &&
            Number.isFinite(candidate.quote.estimated_total) && (
              <p>
                {t('Estimated total')}:{' '}
                {priceNumber(candidate.quote.estimated_total)}
              </p>
            )}
          {candidate.quote.comparison === 'unavailable' && (
            <p className='text-muted-foreground'>{t('Not price-comparable')}</p>
          )}
          {candidate.quote.reason && (
            <p className='text-muted-foreground'>
              {routingReasonLabel(candidate.quote.reason, t)}
            </p>
          )}
        </div>
        <CopyButton
          value={priceLines.join('\n')}
          aria-label={t('Copy effective prices')}
          className='min-h-11 min-w-11 shrink-0'
        />
      </div>
      <dl className='text-muted-foreground grid gap-2 text-xs sm:grid-cols-2'>
        <div>
          <dt>{responseLabel}</dt>
          <dd>
            {metrics.response_ms_1h === null
              ? t('Unknown')
              : `${number(metrics.response_ms_1h)} ms`}{' '}
            · n={number(metrics.response_samples_1h)}
          </dd>
        </div>
        <div>
          <dt>{t('Success rate · 24h')}</dt>
          <dd>
            {metrics.success_24h === null
              ? t('Unknown')
              : new Intl.NumberFormat(locale, {
                  style: 'percent',
                  maximumFractionDigits: 2,
                }).format(metrics.success_24h)}{' '}
            · n={number(metrics.samples_24h)}
          </dd>
        </div>
        <div className='sm:col-span-2'>
          <dt className='sr-only'>{t('Updated')}</dt>
          <dd>
            {metrics.updated_at === null
              ? t('Unknown')
              : new Date(metrics.updated_at * 1000).toLocaleString(locale)}
            {metrics.stale && ` · ${t('Stale samples')}`} ·{' '}
            {metrics.protocol_class}
          </dd>
        </div>
      </dl>
      {candidate.reason && (
        <p className='text-muted-foreground text-xs break-words'>
          {routingReasonLabel(candidate.reason, t)}
        </p>
      )}
    </article>
  )
}

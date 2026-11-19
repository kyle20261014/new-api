import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Checkbox } from '@/components/ui/checkbox'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'

import { routingReasonLabel } from './labels'
import type { RoutingCandidate } from './types'

export function CandidateCard(props: {
  candidate: RoutingCandidate
  selected?: boolean
  onSelect?: (checked: boolean) => void
  selectionDisabled?: boolean
  selectionHint?: string
  allowUnavailableSelection?: boolean
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
    const label = priceLabels[price.label] ?? price.label
    const value = `${amount} / ${units[price.unit] ?? price.unit}${price.rule ? ` · ${price.rule}` : ''}`
    return { label, value, line: `${label}: ${value}` }
  })
  const responseLabels = {
    ttft: t('Time to start replying · Last hour'),
    response: t('Time to return a result · Last hour'),
    task_terminal: t('Task completion time · Last hour'),
  }
  const healthLabels = {
    normal: t('Healthy'),
    congested: t('Congested'),
    soft_offline: t('Temporarily offline'),
    unknown: t('Unknown'),
    degraded: t('Degraded'),
  }
  return (
    <article
      className={cn(
        'bg-card grid min-w-0 overflow-hidden rounded-2xl border text-sm lg:grid-cols-[minmax(210px,0.8fr)_minmax(0,1.2fr)]',
        props.selected && 'border-cyan-600/60'
      )}
    >
      <div
        className={cn(
          'min-w-0 space-y-3 p-4',
          props.selected && 'bg-cyan-500/5'
        )}
      >
        <div className='flex min-w-0 items-start justify-between gap-2'>
          <div className='flex min-h-11 min-w-0 flex-1 items-center gap-3'>
            {props.onSelect && (
              <Checkbox
                className='after:-inset-x-3.5 after:-inset-y-3.5'
                checked={!!props.selected}
                disabled={
                  props.selectionDisabled ||
                  (!props.allowUnavailableSelection &&
                    !candidate.available &&
                    !props.selected)
                }
                onCheckedChange={(checked) =>
                  props.onSelect?.(checked === true)
                }
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
          </div>
          <span
            className={cn(
              'max-w-36 rounded-full px-2 py-1 text-xs break-words',
              candidate.health === 'normal'
                ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                : 'bg-muted text-muted-foreground'
            )}
          >
            {healthLabels[candidate.health]}
            {!candidate.available && ` · ${t('Unavailable')}`}
          </span>
        </div>
        <dl className='text-muted-foreground grid gap-2 text-xs'>
          <div className='bg-muted/60 flex flex-wrap items-baseline gap-x-2 rounded-full px-2 py-1'>
            <dt>{responseLabels[metrics.response_kind]}</dt>
            <dd className='font-medium tabular-nums'>
              {metrics.response_ms_1h === null
                ? t('Unknown')
                : `${number(metrics.response_ms_1h)} ms`}{' '}
              ·{' '}
              {t('Calls counted: {{count}}', {
                count: number(metrics.response_samples_1h),
              })}
            </dd>
          </div>
          <div className='bg-muted/60 flex flex-wrap items-baseline gap-x-2 rounded-full px-2 py-1'>
            <dt>{t('Call success rate · Last 24 hours')}</dt>
            <dd className='font-medium tabular-nums'>
              {metrics.success_24h === null
                ? t('Unknown')
                : new Intl.NumberFormat(locale, {
                    style: 'percent',
                    maximumFractionDigits: 2,
                  }).format(metrics.success_24h)}{' '}
              ·{' '}
              {t('Calls counted: {{count}}', {
                count: number(metrics.samples_24h),
              })}
            </dd>
          </div>
          <div>
            <dt className='sr-only'>{t('Updated')}</dt>
            <dd className='break-words'>
              {metrics.updated_at === null
                ? t('Unknown')
                : new Date(metrics.updated_at * 1000).toLocaleString(locale)}
              {metrics.stale && ` · ${t('Older data, for reference only')}`} ·{' '}
              {metrics.protocol_class}
            </dd>
          </div>
        </dl>
        {props.selectionHint && (
          <p className='text-muted-foreground text-xs'>{props.selectionHint}</p>
        )}
        {candidate.reason && (
          <p className='text-muted-foreground text-xs break-words'>
            {routingReasonLabel(candidate.reason, t)}
          </p>
        )}
        {candidate.variants && candidate.variants.length > 1 && (
          <details className='text-xs'>
            <summary className='min-h-11 cursor-pointer py-3'>
              {t('Protocol observations')}
            </summary>
            <ul className='space-y-2 break-words'>
              {candidate.variants.map((variant) => (
                <li key={`${variant.provider_key}:${variant.operation_key}`}>
                  {variant.provider_key} · {variant.operation_key} ·{' '}
                  {healthLabels[variant.health]} ·{' '}
                  {responseLabels[variant.response_kind]}:{' '}
                  {variant.response_ms_1h === null
                    ? t('Unknown')
                    : `${number(variant.response_ms_1h)} ms`}{' '}
                  ·{' '}
                  {t('Calls counted: {{count}}', {
                    count: number(variant.response_samples_1h),
                  })}{' '}
                  · {t('Call success rate · Last 24 hours')}:{' '}
                  {variant.success_24h === null
                    ? t('Unknown')
                    : new Intl.NumberFormat(locale, {
                        style: 'percent',
                        maximumFractionDigits: 2,
                      }).format(variant.success_24h)}{' '}
                  ·{' '}
                  {t('Calls counted: {{count}}', {
                    count: number(variant.samples_24h),
                  })}
                  {' · '}
                  {t('Updated')}:{' '}
                  {variant.updated_at === null
                    ? t('Unknown')
                    : new Date(variant.updated_at * 1000).toLocaleString(
                        locale
                      )}
                  {variant.stale && ` · ${t('Older data, for reference only')}`}
                </li>
              ))}
            </ul>
          </details>
        )}
      </div>
      <div className='min-w-0 border-t p-4 lg:border-t-0 lg:border-l'>
        <div className='mb-2 flex items-center justify-between gap-2'>
          <h4 className='text-xs font-medium'>{t('Current prices')}</h4>
          <CopyButton
            value={priceLines.map((price) => price.line).join('\n')}
            aria-label={t('Copy effective prices')}
            className='min-h-11 min-w-11 shrink-0'
          />
        </div>
        <div className='min-w-0 space-y-2 text-xs break-words'>
          {priceLines.length > 0 && (
            <table className='w-full table-fixed text-start'>
              <caption className='sr-only'>{t('Current prices')}</caption>
              <thead className='text-muted-foreground'>
                <tr>
                  <th
                    scope='col'
                    className='w-2/5 border-b pb-2 text-start font-medium'
                  >
                    {t('Specification')}
                  </th>
                  <th
                    scope='col'
                    className='border-b pb-2 text-start font-medium'
                  >
                    {t('Price')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {priceLines.map((price) => (
                  <tr key={price.line}>
                    <th
                      scope='row'
                      className='border-b py-2 pr-2 text-start font-normal'
                    >
                      {price.label}:
                    </th>
                    <td className='border-b py-2 tabular-nums'>
                      {price.value}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {priceLines.length === 0 && <p>{t('Price unavailable')}</p>}
          {typeof candidate.quote.estimated_total === 'number' &&
            Number.isFinite(candidate.quote.estimated_total) && (
              <p>
                {t('Estimated total')}:{' '}
                {priceNumber(candidate.quote.estimated_total)}
              </p>
            )}
          {candidate.quote.comparison === 'unavailable' && (
            <p className='text-muted-foreground'>
              {t('Prices cannot be compared yet.')}
            </p>
          )}
          {candidate.quote.reason && (
            <p className='text-muted-foreground'>
              {routingReasonLabel(candidate.quote.reason, t)}
            </p>
          )}
        </div>
      </div>
    </article>
  )
}

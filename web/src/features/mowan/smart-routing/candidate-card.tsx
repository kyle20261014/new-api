import { Info } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Checkbox } from '@/components/ui/checkbox'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

import { candidatePriceRows } from './candidate-prices'
import { routingReasonLabel } from './labels'
import type { RoutingCandidate, RoutingMetrics } from './types'

export function CandidateCard(props: {
  candidate: RoutingCandidate
  selected?: boolean
  onSelect?: (checked: boolean) => void
  selectionDisabled?: boolean
  selectionHint?: string
  allowUnavailableSelection?: boolean
}) {
  const { t, i18n } = useTranslation()
  const language = i18n.resolvedLanguage || i18n.language || 'en'
  const locale = toIntlLocale(language) ?? 'en'
  const candidate = props.candidate
  const quote = candidate.quote
  const priceLines = candidatePriceRows(quote, t, language, locale)
  const number = (value: number) => formatNumber(value, locale)
  const percent = (value: number | null) =>
    value === null
      ? t('Unknown')
      : new Intl.NumberFormat(locale, {
          style: 'percent',
          maximumFractionDigits: 2,
        }).format(value)
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
  const metricDetails = (metrics: RoutingMetrics) => (
    <dl className='space-y-1 text-xs'>
      <div>
        <dt className='inline'>{responseLabels[metrics.response_kind]}: </dt>
        <dd className='inline'>
          {metrics.response_ms_1h === null
            ? t('Unknown')
            : `${number(metrics.response_ms_1h)} ms`}{' '}
          ·{' '}
          {t('Calls counted: {{count}}', {
            count: number(metrics.response_samples_1h),
          })}
        </dd>
      </div>
      <div>
        <dt className='inline'>{t('Call success rate · Last 24 hours')}: </dt>
        <dd className='inline'>
          {percent(metrics.success_24h)} ·{' '}
          {t('Calls counted: {{count}}', {
            count: number(metrics.samples_24h),
          })}
        </dd>
      </div>
      <div>
        <dt className='inline'>{t('Updated')}: </dt>
        <dd className='inline'>
          {metrics.updated_at === null
            ? t('Unknown')
            : new Date(metrics.updated_at * 1000).toLocaleString(locale)}
          {metrics.stale && ` · ${t('Older data, for reference only')}`}
        </dd>
      </div>
      {metrics.submission_ms_1h != null && (
        <div>
          {t('Submission time · Last hour')}: {number(metrics.submission_ms_1h)}{' '}
          ms
        </div>
      )}
    </dl>
  )
  const rules = [
    ...new Set(
      [
        quote.expression?.rule,
        ...quote.prices.map((price) => price.rule),
      ].filter((rule): rule is string => !!rule)
    ),
  ]
  return (
    <article
      className={cn(
        'bg-card grid min-w-0 overflow-hidden rounded-lg border text-sm @container',
        props.selected && 'border-cyan-600/60 bg-cyan-500/[0.025]'
      )}
    >
      <div className='grid min-w-0 @min-[480px]:grid-cols-[minmax(180px,0.8fr)_minmax(0,1.2fr)]'>
        <div className='min-w-0 space-y-1 p-2'>
          <div className='flex min-h-11 min-w-0 items-center gap-2'>
            {props.onSelect && (
              <Checkbox
                className='ml-1.5 after:-inset-3.5'
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
            {candidate.icon && (
              <span className='shrink-0' aria-hidden='true'>
                {getLobeIcon(candidate.icon, 20)}
              </span>
            )}
            <div className='min-w-0 flex-1'>
              <h4 className='font-semibold break-all'>{candidate.group}</h4>
              <p className='text-muted-foreground text-xs break-all'>
                {candidate.display_name || candidate.model}
              </p>
            </div>
            <span
              className={cn(
                'shrink-0 rounded px-1.5 py-0.5 text-[11px]',
                candidate.health === 'normal'
                  ? 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                  : 'bg-muted text-muted-foreground'
              )}
            >
              {healthLabels[candidate.health]}
            </span>
          </div>
          <div className='flex flex-wrap gap-1.5 text-xs tabular-nums'>
            <span
              className='bg-muted/60 rounded px-2 py-1'
              title={t('Call success rate · Last 24 hours')}
            >
              {t('Success rate')} {percent(candidate.metrics.success_24h)}
            </span>
            <span
              className='bg-muted/60 rounded px-2 py-1'
              title={responseLabels[candidate.metrics.response_kind]}
            >
              {candidate.metrics.response_kind === 'ttft'
                ? t('Time to first token')
                : t('Response time')}{' '}
              {candidate.metrics.response_ms_1h === null
                ? t('Unknown')
                : `${number(candidate.metrics.response_ms_1h)} ms`}
            </span>
          </div>
          {!candidate.available && (
            <p className='text-muted-foreground text-xs'>{t('Unavailable')}</p>
          )}
          {props.selectionHint && (
            <p className='text-muted-foreground text-xs'>
              {props.selectionHint}
            </p>
          )}
          <details className='text-muted-foreground text-xs'>
            <summary className='flex min-h-11 cursor-pointer items-center'>
              {t('Protocol observations')}
            </summary>
            {candidate.reason && (
              <p className='mb-2'>{routingReasonLabel(candidate.reason, t)}</p>
            )}
            {!candidate.variants?.length && metricDetails(candidate.metrics)}
            {!!candidate.variants?.length && (
              <ul>
                {candidate.variants.map((variant) => (
                  <li
                    key={`${variant.provider_key}:${variant.operation_key}:${variant.protocol_class}`}
                    className='mb-3 space-y-1 break-words'
                  >
                    <p className='font-medium'>
                      {variant.provider_key} · {variant.operation_key} ·{' '}
                      {variant.protocol_class} · {healthLabels[variant.health]}
                    </p>
                    {metricDetails(variant)}
                  </li>
                ))}
              </ul>
            )}
          </details>
        </div>
        <div className='relative min-w-0 border-t px-3 pb-2 @min-[480px]:border-t-0 @min-[480px]:border-l'>
          <div className='flex min-h-11 items-center justify-between gap-2'>
            <h4 className='pr-11 text-xs font-medium'>{t('Current prices')}</h4>
            <CopyButton
              value={
                priceLines.length
                  ? priceLines
                      .map((price) => `${price.label}: ${price.value}`)
                      .join('\n')
                  : [
                      rules.length
                        ? t('Special pricing')
                        : t('Price unavailable'),
                      quote.reason ? routingReasonLabel(quote.reason, t) : '',
                      ...rules,
                    ]
                      .filter(Boolean)
                      .join('\n')
              }
              aria-label={t('Copy effective prices')}
              className='min-h-11 min-w-11 shrink-0'
            />
          </div>
          {priceLines.length > 0 ? (
            <table className='w-full table-fixed text-start text-xs'>
              <caption className='sr-only'>{t('Current prices')}</caption>
              <thead className='text-muted-foreground'>
                <tr>
                  <th
                    scope='col'
                    className='w-1/2 border-b pb-1 text-start font-normal'
                  >
                    {t('Specification')}
                  </th>
                  <th
                    scope='col'
                    className='border-b pb-1 text-start font-normal'
                  >
                    {t('Price')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {priceLines.map((price) => (
                  <tr key={`${price.label}:${price.value}`}>
                    <th
                      scope='row'
                      className='border-b py-1.5 pr-2 text-start font-normal break-words'
                    >
                      {price.label}
                    </th>
                    <td className='border-b py-1.5 break-words tabular-nums'>
                      {price.value}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <div className='text-muted-foreground space-y-1 py-2 text-xs'>
              <p>
                {rules.length ? t('Special pricing') : t('Price unavailable')}
              </p>
              {quote.reason && <p>{routingReasonLabel(quote.reason, t)}</p>}
            </div>
          )}
          {(rules.length > 0 ||
            quote.reason ||
            candidate.variants?.some(
              (variant) => variant.quote?.expression
            )) && (
            <details className='text-muted-foreground text-xs'>
              <summary
                aria-label={t('Pricing details')}
                className='absolute top-0 right-14 flex min-h-11 min-w-11 cursor-pointer items-center justify-center rounded focus-visible:outline-2'
              >
                <Info className='size-4' aria-hidden='true' />
              </summary>
              {priceLines.length > 0 && quote.reason && (
                <p>{routingReasonLabel(quote.reason, t)}</p>
              )}
              {quote.expression && (
                <p>
                  {t('Group ratio')}: {number(quote.expression.group_ratio)}
                </p>
              )}
              {rules.map((rule) => (
                <pre
                  key={rule}
                  className='bg-muted my-2 max-h-40 overflow-auto rounded p-2 font-mono text-[11px] break-all whitespace-pre-wrap'
                >
                  {rule}
                </pre>
              ))}
              {candidate.variants
                ?.filter(
                  (variant) =>
                    variant.quote?.expression &&
                    quote.expression?.rule !== variant.quote.expression.rule
                )
                .map((variant) => (
                  <section
                    key={`${variant.provider_key}:${variant.operation_key}:${variant.protocol_class}`}
                    className='mt-2 space-y-1'
                  >
                    <p>
                      {variant.provider_key} · {variant.operation_key} ·{' '}
                      {variant.protocol_class}
                    </p>
                    {variant.quote &&
                      candidatePriceRows(
                        variant.quote,
                        t,
                        language,
                        locale
                      ).map((row) => (
                        <p key={`${row.label}:${row.value}`}>
                          {row.label}: {row.value}
                        </p>
                      ))}
                    <pre className='max-h-40 overflow-auto break-all whitespace-pre-wrap'>
                      {variant.quote?.expression?.rule}
                    </pre>
                  </section>
                ))}
            </details>
          )}
        </div>
      </div>
    </article>
  )
}

import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { CodeBlock } from '@/components/ai-elements/code-block'
import { CopyButton } from '@/components/copy-button'
import { Badge } from '@/components/ui/badge'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  anomalyReasonKey,
  diagnosticReasonKey,
  diagnosticStateKey,
  drawingResult,
  fingerprintResult,
  intelligenceRows,
  stepOutcome,
  summarizeReport,
  unknownMetricKey,
  type DrawingMetric,
  type DiagnosticTone,
  type StepOutcome,
} from './display'
import { DiagnosticPreview } from './preview'
import {
  isDiagnosticRunning,
  reportIsStale,
  type DiagnosticReport,
  type DiagnosticStep,
} from './types'

const toneDot: Record<DiagnosticTone, string> = {
  passed: 'bg-success',
  failed: 'bg-destructive',
  pending: 'bg-warning',
  unknown: 'bg-muted-foreground/60',
  idle: 'bg-muted-foreground/40',
}

const toneText: Record<DiagnosticTone, string> = {
  passed: 'text-success',
  failed: 'text-destructive',
  pending: 'text-foreground',
  unknown: 'text-muted-foreground',
  idle: 'text-muted-foreground',
}

function formatPercent(value: number | undefined, locale: string): string {
  if (value === undefined) return '—'
  const percent = value * 100
  if (!Number.isFinite(percent)) return '—'
  if (percent < 0.01) return '<0.01%'
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(percent)}%`
}

export function DiagnosticReportView(props: {
  report: DiagnosticReport
  technical?: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language) ?? 'en'
  const report = props.report
  const result = report.result
  const steps = report.steps ?? []
  const source =
    report.kind === 'drawing'
      ? steps.find((step) => step.answer)?.answer
      : undefined
  const summary = summarizeReport(report)
  const score = summary.score
  const partial = summary.partial
  const completed = steps.filter((step) => step.complete).length
  const fingerprint = fingerprintResult(report)
  let passed: boolean | undefined
  if (summary.tone === 'passed') passed = true
  if (summary.tone === 'failed') passed = false
  const verdict = partial ? t('Partially completed') : t(summary.labelKey)
  const showSteps = props.technical || report.kind === 'connection'
  let headline = (
    <div
      className={cn(
        'font-mono text-3xl leading-none font-semibold tabular-nums',
        passed === true && 'text-success',
        passed === false && 'text-destructive'
      )}
    >
      {score === undefined ? '—' : formatNumber(score, locale)}
      <span className='text-muted-foreground ml-1 font-sans text-xs font-normal'>
        {t('Score')}
      </span>
    </div>
  )
  if (report.kind === 'fingerprint' && fingerprint?.best) {
    headline = (
      <div className='min-w-0'>
        <div className='text-xl font-semibold break-words'>
          {fingerprint.best.model}
        </div>
        <div className='text-muted-foreground text-xs'>
          {t('Closest match')} · {t('Reference similarity')}{' '}
          {formatPercent(fingerprint.best.probability, locale)}
        </div>
      </div>
    )
  } else if (partial) {
    headline = (
      <div className='font-mono text-3xl leading-none font-semibold tabular-nums'>
        {formatNumber(partial.got, locale)}
        <span className='text-muted-foreground text-base'>
          {' '}
          / {formatNumber(partial.max, locale)}
        </span>
        <span className='text-muted-foreground ml-1 font-sans text-xs font-normal'>
          {t('Partial score')}
        </span>
      </div>
    )
  }
  // A partial score already explains why part of the paper is unscored, so the
  // score-blocking reasons would contradict the visible result.
  const partialBlockingReasons = new Set([
    'code_not_in_proven_subset',
    'not_all_questions_completed',
    'no_complete_reply',
    'duplicate_answer_id',
  ])
  const visibleReasons = [
    ...new Set(result?.reasons ?? []),
  ].filter(
    (reason) =>
      !(partial && report.kind === 'intelligence' && partialBlockingReasons.has(reason))
  )
  // Only reasons that have a readable translation reach the normal view; a
  // code without one would otherwise leave an empty list behind.
  const plainReasons = visibleReasons
    .map((reason) => diagnosticReasonKey(reason))
    .filter((key): key is string => Boolean(key))
  let reasonList: ReactNode = null
  if (props.technical && visibleReasons.length) {
    reasonList = (
      <ul className='list-disc space-y-1 pl-5 text-sm'>
        {visibleReasons.map((reason) => (
          <li key={reason} className='font-mono text-xs break-words'>
            {reason}
          </li>
        ))}
      </ul>
    )
  } else if (plainReasons.length) {
    reasonList = (
      <ul className='list-disc space-y-1 pl-5 text-sm'>
        {plainReasons.map((key) => (
          <li key={key} className='break-words'>
            {t(key)}
          </li>
        ))}
      </ul>
    )
  }
  return (
    <article className='min-w-0 space-y-4 [&_summary]:min-h-11'>
      <header className='space-y-3 border-b pb-4'>
        <div className='flex flex-wrap items-end gap-x-3 gap-y-2'>
          {headline}
          <span
            className={cn(
              'flex items-center gap-1.5 text-sm font-medium',
              toneText[summary.tone]
            )}
          >
            <span
              aria-hidden
              className={cn('size-2 rounded-full', toneDot[summary.tone])}
            />
            {result ? verdict : t(diagnosticStateKey(report.state))}
          </span>
          {report.creative && (
            <Badge variant='outline'>{t('Private creative report')}</Badge>
          )}
          {reportIsStale(report) && (
            <Badge variant='outline'>{t('Outdated report')}</Badge>
          )}
        </div>
        <p className='text-muted-foreground text-xs'>
          {new Date(report.created_at * 1000).toLocaleString(locale)} ·{' '}
          {t('{{completed}} of {{total}} steps completed', {
            completed: formatNumber(completed, locale),
            total: formatNumber(steps.length, locale),
          })}
          {summary.judged &&
            ` · ${t('{{judged}} of {{total}} answers could be graded', {
              judged: formatNumber(summary.judged.judged, locale),
              total: formatNumber(summary.judged.total, locale),
            })}`}
        </p>
        <p className='text-muted-foreground text-xs'>
          {t(
            'This routing result does not represent every channel in this group.'
          )}
        </p>
        {report.owned && report.fee && (
          <p className='text-xs'>
            {t('My charge')}:{' '}
            {report.fee.quota == null
              ? t('Unknown')
              : formatQuota(report.fee.quota)}{' '}
            · {t(diagnosticStateKey(report.fee.status))}
          </p>
        )}
      </header>
      {reportIsStale(report) && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Reports become outdated after 24 hours or when configuration changes. Viewing this report does not start a new test.'
          )}
        </p>
      )}
      {reasonList}
      {report.kind === 'connection' && <ConnectionSection report={report} />}
      {report.kind === 'fingerprint' && (
        <FingerprintSection report={report} />
      )}
      {report.kind === 'intelligence' && (
        <IntelligenceSection report={report} />
      )}
      {report.kind === 'drawing' && (
        <DrawingSection
          report={report}
          source={source}
          showPreview={!props.technical}
        />
      )}
      {report.kind === 'fingerprint' && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Fingerprint confidence measures similarity within the reference library, not verified model identity.'
          )}
        </p>
      )}
      {source && (
        <details className='rounded-lg border p-3'>
          <summary className='cursor-pointer text-sm font-medium'>
            {t('Original code')}
          </summary>
          <CodeBlock
            code={source}
            language='html'
            filename={`diagnostic-${report.id}.html`}
            showToolbar
            defaultCollapsed
            maxExpandedLines={24}
            className='[&_button]:min-h-11 [&_button]:min-w-11'
          >
            <CopyButton value={source} aria-label={t('Copy code')} />
          </CodeBlock>
        </details>
      )}
      {report.materials && (
        <details className='rounded-lg border p-3'>
          <summary className='cursor-pointer text-sm font-medium'>
            {t('Creative materials')}
          </summary>
          <dl className='mt-3 space-y-2 text-sm'>
            {Object.entries(report.materials).map(([key, value]) => (
              <div key={key} className='break-words'>
                <dt className='text-muted-foreground'>{t(key)}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
        </details>
      )}
      {report.kind === 'drawing' && (
        <details className='rounded-lg border p-3'>
          <summary className='cursor-pointer text-sm font-medium'>
            {t('Scoring rules')}
          </summary>
          <p className='text-muted-foreground mt-3 text-sm'>
            {t(
              'SVG +40; looping animation +25; elements 15/6: +10/+5; paths 5: +5; colors 4/2: +10/+5; bytes 4096/1500: +10/+5; viewBox +5. External links -25; placeholders -20; unclosed SVG -20; confirmed structural anomaly -30 once. Sum before clamping to 0–100. Unknown metrics do not count as zero.'
            )}
          </p>
        </details>
      )}
      {showSteps &&
        steps.map((step) => {
          const outcome = stepOutcome(report, step.id)
          // A step left at "running" inside a finished report never came back,
          // so it reports the report's own state instead of looking live again.
          const stepState =
            step.state === 'running' && !isDiagnosticRunning(report)
              ? report.state
              : step.state
          return (
            <details key={step.id} className='rounded-lg border p-3'>
              <summary className='cursor-pointer text-sm font-medium break-words'>
                <span className='inline-flex items-center gap-1.5'>
                  <StepOutcomeMarker
                    outcome={outcome}
                    label={t('Outcome')}
                    texts={{
                      passed: t('Correct'),
                      failed: t('Incorrect'),
                      unknown: t('Not judged'),
                    }}
                  />
                  {t(step.title)} · {t(diagnosticStateKey(stepState))}
                </span>
              </summary>
              <StepBody step={step} locale={locale} />
            </details>
          )
        })}
      {props.technical && result?.details !== undefined && (
        <details open className='rounded-lg border p-3'>
          <summary className='cursor-pointer text-sm font-medium'>
            {t('Result details')}
          </summary>
          <CodeBlock
            code={JSON.stringify(result.details, null, 2)}
            language='json'
            showToolbar
            defaultCollapsed
            maxExpandedLines={24}
            className='[&_button]:min-h-11 [&_button]:min-w-11'
          >
            <CopyButton
              value={JSON.stringify(result.details, null, 2)}
              aria-label={t('Copy code')}
            />
          </CodeBlock>
        </details>
      )}
      <p className='text-muted-foreground text-xs break-words'>
        {t('Version')}:{' '}
        {[...new Set([report.version, result?.version].filter(Boolean))].join(
          ' · '
        )}
      </p>
    </article>
  )
}

function StepBody(props: { step: DiagnosticStep; locale: string }) {
  const { t } = useTranslation()
  const step = props.step
  return (
    <div className='mt-3 space-y-3'>
      <p className='text-muted-foreground text-xs'>
        {t('Input')}:{' '}
        {step.input_tokens == null
          ? t('Unknown')
          : formatNumber(step.input_tokens, props.locale)}{' '}
        · {t('Output')}:{' '}
        {step.output_tokens == null
          ? t('Unknown')
          : formatNumber(step.output_tokens, props.locale)}{' '}
        · {t('Reasoning')}:{' '}
        {step.reasoning_tokens == null
          ? t('Unknown')
          : formatNumber(step.reasoning_tokens, props.locale)}{' '}
        · {t('Duration')}:{' '}
        {step.duration_ms == null || step.duration_ms === 0
          ? t('Unknown')
          : `${formatNumber(step.duration_ms, props.locale)} ms`}
      </p>
      {step.error && (
        <p role='alert' className='text-destructive text-sm break-words'>
          {t(step.error)}
        </p>
      )}
      <h4 className='text-sm font-medium'>{t('Prompt')}</h4>
      <pre className='text-muted-foreground max-h-48 overflow-auto text-xs break-words whitespace-pre-wrap'>
        {step.prompt}
      </pre>
      {step.answer && (
        <>
          <h4 className='text-sm font-medium'>{t('Answer')}</h4>
          <pre className='max-h-72 overflow-auto text-xs break-words whitespace-pre-wrap'>
            {step.answer}
          </pre>
        </>
      )}
    </div>
  )
}

function Metric(props: { label: string; value: string }) {
  return (
    <div className='rounded-lg border p-3'>
      <dt className='text-muted-foreground text-xs'>{props.label}</dt>
      <dd className='font-mono text-lg font-semibold tabular-nums'>
        {props.value}
      </dd>
    </div>
  )
}

function ConnectionSection(props: { report: DiagnosticReport }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language) ?? 'en'
  const step = props.report.steps?.[0]
  const answer = (step?.answer ?? '').trim()
  const received = Boolean(step?.complete) && answer !== ''
  const duration = step?.duration_ms
  return (
    <section className='space-y-2 rounded-lg border p-3'>
      <p className='text-base font-medium'>
        {received ? t('A valid reply arrived') : t('No valid reply')}
      </p>
      {/* A failed or stuck attempt has no meaningful timing; showing 0 ms would
          read as an actual measurement. */}
      {duration != null && duration > 0 && (
        <p className='text-muted-foreground text-sm'>
          {t('Duration')}: {formatNumber(duration, locale)} ms
        </p>
      )}
      {received && (
        <>
          <h4 className='text-sm font-medium'>{t('Model reply')}</h4>
          <pre className='bg-muted/40 max-h-40 overflow-auto rounded-md p-2 text-xs break-words whitespace-pre-wrap'>
            {answer.length > 600 ? `${answer.slice(0, 600)}…` : answer}
          </pre>
        </>
      )}
    </section>
  )
}

function FingerprintSection(props: { report: DiagnosticReport }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language) ?? 'en'
  const fingerprint = fingerprintResult(props.report)
  if (!fingerprint) return null
  const best = fingerprint.best
  return (
    <section className='space-y-3'>
      {best && (
        <div className='grid grid-cols-2 gap-3 sm:grid-cols-3'>
          <Metric label={t('Closest match')} value={best.model} />
          <Metric
            label={t('Reference similarity')}
            value={formatPercent(best.probability, locale)}
          />
          {fingerprint.samples !== undefined && (
            <Metric
              label={t('Valid samples')}
              value={formatNumber(fingerprint.samples, locale)}
            />
          )}
        </div>
      )}
      {fingerprint.candidates.length > 1 && (
        <div className='overflow-x-auto rounded-lg border'>
          <table className='w-full text-sm'>
            <thead className='bg-muted/40 text-muted-foreground text-xs'>
              <tr>
                <th className='p-2 text-left font-medium'>{t('Model')}</th>
                <th className='p-2 text-left font-medium'>{t('Family')}</th>
                <th className='p-2 text-right font-medium'>
                  {t('Similarity')}
                </th>
              </tr>
            </thead>
            <tbody>
              {fingerprint.candidates.map((candidate) => (
                <tr key={candidate.model} className='border-t'>
                  <td className='max-w-[16rem] p-2 break-words'>
                    {candidate.model}
                  </td>
                  <td className='p-2'>{candidate.family}</td>
                  <td className='p-2 text-right font-mono tabular-nums'>
                    {formatPercent(candidate.probability, locale)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {fingerprint.insufficient && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Few reference samples support this ranking, so read it as indicative only.'
          )}
        </p>
      )}
    </section>
  )
}

function IntelligenceSection(props: { report: DiagnosticReport }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language) ?? 'en'
  const rows = intelligenceRows(props.report)
  const summary = summarizeReport(props.report)
  const partial = summary.partial
  if (rows.length === 0) return null
  const graded = rows.filter((row) => row.outcome !== 'unknown')
  const correct = rows.filter((row) => row.outcome === 'passed')
  const maxWeight = partial?.max ?? rows.reduce((sum, row) => sum + row.weight, 0)
  const gotWeight = partial?.got ?? correct.reduce((sum, row) => sum + row.weight, 0)
  return (
    <section className='space-y-3'>
      <div className='grid grid-cols-2 gap-3 sm:grid-cols-3'>
        <Metric
          label={t('Weighted points')}
          value={`${formatNumber(gotWeight, locale)} / ${formatNumber(maxWeight, locale)}`}
        />
        <Metric
          label={t('Answers graded')}
          value={`${formatNumber(graded.length, locale)} / ${formatNumber(rows.length, locale)}`}
        />
        <Metric
          label={t('Correct answers')}
          value={formatNumber(correct.length, locale)}
        />
        {partial && (
          <Metric
            label={t('Graded weight')}
            value={`${formatNumber(partial.judgedWeight, locale)} / ${formatNumber(partial.max, locale)}`}
          />
        )}
      </div>
      {partial && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'This is a partial score: ungraded questions earn nothing and do not make the test pass or fail.'
          )}
        </p>
      )}
      <div className='overflow-x-auto rounded-lg border'>
        <table className='w-full text-sm'>
          <thead className='bg-muted/40 text-muted-foreground text-xs'>
            <tr>
              <th className='p-2 text-right font-medium'>{t('Question')}</th>
              <th className='p-2 text-left font-medium'>{t('Type')}</th>
              <th className='p-2 text-right font-medium'>{t('Weight')}</th>
              <th className='p-2 text-left font-medium'>{t('Result')}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={`${row.index}-${row.id}`} className='border-t'>
                <td className='p-2 text-right font-mono tabular-nums'>
                  {row.index}
                </td>
                <td className='p-2'>{row.topic ? t(row.topic) : '—'}</td>
                <td className='p-2 text-right font-mono tabular-nums'>
                  ×{row.weight}
                </td>
                <td className='p-2'>
                  <StepOutcomeMarker
                    outcome={row.outcome}
                    label={t('Outcome')}
                    texts={{
                      passed: t('Correct'),
                      failed: t('Incorrect'),
                      unknown: t('Not judged'),
                    }}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function DrawingSection(props: {
  report: DiagnosticReport
  source?: string
  showPreview: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language) ?? 'en'
  const drawing = drawingResult(props.report)
  if (!drawing && !(props.showPreview && props.source)) return null
  const labels: Record<string, string> = {
    valid_svg: 'Valid SVG',
    loop: 'Looping animation',
    elements: 'Elements',
    paths: 'Paths',
    colors: 'Colors',
    bytes: 'Bytes',
    view_box: 'viewBox',
    external: 'External references',
    placeholder: 'Placeholder content',
    unclosed: 'Unclosed markup',
    anomaly: 'Detected anomaly',
  }
  const metricValue = (value?: string): string => {
    if (value === undefined) return '—'
    if (value === 'yes') return t('Yes')
    if (value === 'no') return t('No')
    if (/^\d+$/.test(value)) return formatNumber(Number(value), locale)
    return value
  }
  // Anomaly rows carry detector codes; show only the translated ones and keep
  // unknown codes out of the normal report.
  const anomalyText = (value?: string): string => {
    const labels = (value ?? '')
      .split(', ')
      .map((code) => anomalyReasonKey(code))
      .filter((key): key is string => Boolean(key))
      .map((key) => t(key))
    return labels.length ? labels.join('; ') : '—'
  }
  const metricText = (metric: DrawingMetric): string =>
    metric.key === 'anomaly'
      ? anomalyText(metric.value)
      : metricValue(metric.value)
  const pointsText = (metric: DrawingMetric): string => {
    if (!metric.known) return t('Unknown')
    return metric.penalty ? `−${metric.points}` : `+${metric.points}`
  }
  const total = props.report.result?.score
  return (
    <section className='space-y-3'>
      {props.showPreview && props.source && (
        <DiagnosticPreview key={props.report.id} source={props.source} />
      )}
      {drawing?.hasBreakdown && (
        <div className='overflow-x-auto rounded-lg border'>
          <table className='w-full text-sm'>
            <thead className='bg-muted/40 text-muted-foreground text-xs'>
              <tr>
                <th className='p-2 text-left font-medium'>
                  {t('Score breakdown')}
                </th>
                <th className='p-2 text-left font-medium'>
                  {t('Detected value')}
                </th>
                <th className='p-2 text-right font-medium'>
                  {t('Points earned')}
                </th>
              </tr>
            </thead>
            <tbody>
              {drawing.metrics.map((metric) => (
                <tr key={metric.key} className='border-t'>
                  <td className='p-2 break-words'>
                    {t(labels[metric.key] ?? metric.key)}
                  </td>
                  <td className='p-2 break-words'>
                    {metricText(metric)}
                  </td>
                  <td className='p-2 text-right font-mono tabular-nums'>
                    {pointsText(metric)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {drawing && (
        <div className='overflow-x-auto rounded-lg border'>
          <table className='w-full text-sm'>
            <tbody>
              <tr>
                <td className='p-2'>{t('Structural score')}</td>
                <td className='p-2 text-right font-mono tabular-nums'>
                  {drawing.structuralPoints ?? '—'}
                </td>
              </tr>
              <tr className='border-t'>
                <td className='p-2'>{t('Structural deduction')}</td>
                <td className='p-2 text-right font-mono tabular-nums'>
                  {drawing.penalty ? `−${drawing.penalty}` : '0'}
                </td>
              </tr>
              <tr className='border-t'>
                <td className='p-2'>{t('Anomaly deduction')}</td>
                <td className='p-2 text-right font-mono tabular-nums'>
                  {drawing.anomalyPenalty ? `−${drawing.anomalyPenalty}` : '0'}
                </td>
              </tr>
              <tr className='border-t font-medium'>
                <td className='p-2'>{t('Total score')}</td>
                <td className='p-2 text-right font-mono tabular-nums'>
                  {typeof total === 'number' ? total : '—'}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      )}
      {drawing && !drawing.hasBreakdown && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'This report predates per-item scoring details, so only the stored totals are shown.'
          )}
        </p>
      )}
      {drawing?.unknownMetrics.length ? (
        <p className='text-muted-foreground text-sm'>
          {t('Unknown metrics')}:{' '}
          {drawing.unknownMetrics
            .map((code) => unknownMetricKey(code))
            .filter((key): key is string => Boolean(key))
            .map((key) => t(key))
            .join('; ') || t('Unknown')}
        </p>
      ) : null}
      <p className='text-muted-foreground text-sm'>
        {t(
          'Local rules assess structure only, not artistic quality, prompt accuracy, or model identity.'
        )}
      </p>
    </section>
  )
}

function StepOutcomeMarker(props: {
  outcome: StepOutcome | undefined
  label: string
  texts: Record<StepOutcome, string>
}) {
  if (!props.outcome) return null
  const text = props.texts[props.outcome]
  return (
    <span className='inline-flex shrink-0 items-center gap-1'>
      <span
        role='img'
        aria-label={`${props.label}: ${text}`}
        title={text}
        className={cn(
          'size-1.5 rounded-full',
          props.outcome === 'passed' && 'bg-success',
          props.outcome === 'failed' && 'bg-destructive',
          props.outcome === 'unknown' && 'bg-muted-foreground/60'
        )}
      />
      <span className='text-muted-foreground text-xs font-normal'>{text}</span>
    </span>
  )
}

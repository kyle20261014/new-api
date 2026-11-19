import { useTranslation } from 'react-i18next'

import { CodeBlock } from '@/components/ai-elements/code-block'
import { CopyButton } from '@/components/copy-button'
import { Badge } from '@/components/ui/badge'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import { diagnosticStateKey } from './display'
import { DiagnosticPreview } from './preview'
import { reportIsStale, type DiagnosticReport } from './types'

export function DiagnosticReportView(props: {
  report: DiagnosticReport
  technical?: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const report = props.report
  const result = report.result
  const steps = report.steps ?? []
  const source =
    report.kind === 'drawing'
      ? steps.find((step) => step.answer)?.answer
      : undefined
  const score =
    result?.complete &&
    typeof result.score === 'number' &&
    Number.isFinite(result.score)
      ? result.score
      : undefined
  const completed = steps.filter((step) => step.complete).length
  let verdict = result ? diagnosticStateKey(result.status) : ''
  let passed = result?.passed
  if (report.kind === 'drawing') {
    passed = undefined
    if (result?.complete && result.status === 'completed') {
      const details = result.details
      const anomaly =
        details !== null &&
        typeof details === 'object' &&
        'anomaly' in details &&
        details.anomaly === 'detected'
      passed = anomaly ? false : result.passed
      if (anomaly) {
        verdict = diagnosticStateKey('structural_anomaly')
      } else if (passed === true) {
        verdict = diagnosticStateKey('structure_passed')
      } else if (passed === false) {
        verdict = diagnosticStateKey('structure_failed')
      }
    }
  } else {
    if (passed === true) verdict = 'Passed'
    if (passed === false) verdict = 'Failed'
  }
  return (
    <article className='min-w-0 space-y-4 [&_summary]:min-h-11'>
      <header className='flex flex-wrap items-start gap-3 border-b pb-4'>
        <div
          className={cn(
            'min-w-16 font-mono text-3xl font-semibold tabular-nums',
            passed === true && 'text-success',
            passed === false && 'text-destructive'
          )}
        >
          {score === undefined ? '—' : formatNumber(score, locale)}
          <div className='text-muted-foreground mt-1 font-sans text-xs'>
            {t('Score')}
          </div>
        </div>
        <div className='min-w-0 flex-1 space-y-2'>
          <div className='flex flex-wrap gap-2'>
            <Badge variant='outline'>
              {t(diagnosticStateKey(report.state))}
            </Badge>
            {result && <Badge variant='secondary'>{t(verdict)}</Badge>}
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
        </div>
      </header>
      {reportIsStale(report) && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Reports become outdated after 24 hours or when configuration changes. Viewing this report does not start a new test.'
          )}
        </p>
      )}
      {result?.reasons?.length ? (
        <ul className='list-disc space-y-1 pl-5 text-sm'>
          {[...new Set(result.reasons)].map((reason) => (
            <li key={reason} className='break-words'>
              {t(reason)}
            </li>
          ))}
        </ul>
      ) : null}
      {report.kind === 'fingerprint' && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Fingerprint confidence measures similarity within the reference library, not verified model identity.'
          )}
        </p>
      )}
      {report.kind === 'drawing' && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Local rules assess structure only, not artistic quality, prompt accuracy, or model identity.'
          )}
        </p>
      )}
      {source && !props.technical && (
        <DiagnosticPreview key={report.id} source={source} />
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
      {steps.map((step) => (
        <details key={step.id} className='rounded-lg border p-3'>
          <summary className='cursor-pointer text-sm font-medium break-words'>
            {t(step.title)} · {t(diagnosticStateKey(step.state))}
          </summary>
          <div className='mt-3 space-y-3'>
            <p className='text-muted-foreground text-xs'>
              {t('Input')}:{' '}
              {step.input_tokens == null
                ? t('Unknown')
                : formatNumber(step.input_tokens, locale)}{' '}
              · {t('Output')}:{' '}
              {step.output_tokens == null
                ? t('Unknown')
                : formatNumber(step.output_tokens, locale)}{' '}
              · {t('Reasoning')}:{' '}
              {step.reasoning_tokens == null
                ? t('Unknown')
                : formatNumber(step.reasoning_tokens, locale)}{' '}
              · {t('Duration')}:{' '}
              {step.duration_ms == null
                ? t('Unknown')
                : `${formatNumber(step.duration_ms, locale)} ms`}
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
        </details>
      ))}
      {result?.details !== undefined && (
        <details open={props.technical} className='rounded-lg border p-3'>
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
        {t('Version')}: {report.version} · {result?.version}
      </p>
    </article>
  )
}

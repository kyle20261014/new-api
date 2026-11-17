import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { toIntlLocale } from '@/i18n/languages'

import type { RoutingSubmission } from './types'

export function SubmissionView(props: { submission: RoutingSubmission }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const value = props.submission
  const statuses = {
    accepted: t('Accepted'),
    unknown: t('Submission outcome unknown'),
    prepared: t('Prepared'),
    sending: t('Sending'),
    not_sent: t('Not sent'),
    rejected: t('Rejected'),
  }
  const funding = {
    pending: t('Funds reserved'),
    manual_review: t('Manual reconciliation'),
    settled: t('Settled'),
    released: t('Released'),
  }
  return (
    <section
      className='min-w-0 space-y-3 text-sm'
      aria-label={t('Submission record')}
    >
      <div className='flex min-w-0 items-center gap-2'>
        <span className='min-w-0 font-mono break-all'>
          {value.submission_id}
        </span>
        <CopyButton
          value={value.submission_id}
          aria-label={t('Copy submission ID')}
          className='min-h-11 min-w-11 shrink-0'
        />
      </div>
      <dl className='grid min-w-0 gap-3 sm:grid-cols-2'>
        <div>
          <dt className='text-muted-foreground'>{t('Status')}</dt>
          <dd>{statuses[value.submission_status]}</dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>{t('Funding status')}</dt>
          <dd>{funding[value.funding_status]}</dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>{t('Reserved amount')}</dt>
          <dd>
            {new Intl.NumberFormat(locale, { maximumFractionDigits: 8 }).format(
              value.reserved_amount
            )}{' '}
            {value.reserved_unit}
          </dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>{t('Updated')}</dt>
          <dd>{new Date(value.updated_at * 1000).toLocaleString(locale)}</dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>{t('Model')}</dt>
          <dd className='break-all'>{value.model}</dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>{t('Group')}</dt>
          <dd className='break-all'>{value.group}</dd>
        </div>
      </dl>
      {value.submission_status === 'unknown' && (
        <p role='status' className='text-muted-foreground'>
          {t(
            'The upstream may have accepted this submission. Keep this ID and wait for reconciliation; do not create it again.'
          )}
        </p>
      )}
      {!value.task_id && (
        <p className='text-muted-foreground'>
          {t('No task record yet. This submission remains trackable here.')}
        </p>
      )}
    </section>
  )
}

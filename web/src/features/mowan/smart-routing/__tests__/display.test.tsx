import { render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { expect, test } from 'vitest'

import { CandidateCard } from '../candidate-card'
import { getSubmissionReference } from '../submission-reference'
import { SubmissionView } from '../submission-view'
import type { RoutingCandidate, RoutingSubmission } from '../types'

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

test('unknown metrics remain unknown while actual zero prices and bounded prices remain distinguishable', () => {
  const candidate: RoutingCandidate = {
    model: 'image',
    display_name: 'Image',
    group: 'standard',
    category: 'image',
    available: true,
    health: 'unknown',
    quote: {
      comparison: 'unavailable',
      currency: 'USD',
      reason: 'request_usage_unknown',
      prices: [
        { label: 'Free input', amount: 0, unit: 'token' },
        {
          label: 'Output',
          min: 0.1,
          max: 0.8,
          unit: 'image',
          rule: 'resolution',
        },
        { label: 'Audio', unit: 'second' },
        { label: 'request_rule', amount: 1.25, unit: 'multiplier' },
        { label: 'input', amount: 0.005, unit: 'USD/1M tokens' },
      ],
    },
    metrics: {
      protocol_class: 'image',
      response_kind: 'task_terminal',
      response_ms_1h: null,
      response_samples_1h: 0,
      success_24h: null,
      samples_24h: 0,
      stale: true,
      updated_at: null,
    },
  }
  render(
    <I18nextProvider i18n={i18n}>
      <CandidateCard candidate={candidate} />
    </I18nextProvider>
  )
  expect(screen.getByText('Free input: $0.00 / token')).toBeInTheDocument()
  expect(
    screen.getByText('Output: $0.10 – $0.80 / image · resolution')
  ).toBeInTheDocument()
  expect(screen.getByText('Audio: Unknown / second')).toBeInTheDocument()
  expect(
    screen.getByText('Request pricing rule: 1.25 / Multiplier')
  ).toBeInTheDocument()
  expect(
    screen.getByText('Input tokens: $0.005 / 1M token')
  ).toBeInTheDocument()
  expect(
    screen.getByText(
      'Request usage is unknown; a reliable price comparison is unavailable.'
    )
  ).toBeInTheDocument()
  expect(screen.getByText('Task completion · 1h')).toBeInTheDocument()
  expect(screen.getByText('Success rate · 24h')).toBeInTheDocument()
  expect(screen.getAllByText('Unknown · n=0')).toHaveLength(2)
})

test.each([
  ['pending', 'Funds reserved'],
  ['manual_review', 'Manual reconciliation'],
  ['settled', 'Settled'],
  ['released', 'Released'],
] as const)(
  'an unknown submission without a Task exposes its amount, ID and %s funding state without recreate actions',
  (funding_status, label) => {
    const submission: RoutingSubmission = {
      submission_id: 'submission-1',
      task_id: null,
      submission_status: 'unknown',
      reserved_amount: 12,
      reserved_unit: 'quota',
      updated_at: 1000,
      funding_status,
      model: 'image',
      group: 'standard',
      submit_time: 900,
    }
    render(
      <I18nextProvider i18n={i18n}>
        <SubmissionView submission={submission} />
      </I18nextProvider>
    )
    expect(screen.getByText('submission-1')).toBeInTheDocument()
    expect(screen.getByText('12 quota')).toBeInTheDocument()
    expect(screen.getByText(label)).toBeInTheDocument()
    expect(
      screen.getByText(
        'No task record yet. This submission remains trackable here.'
      )
    ).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent(
      'do not create it again'
    )
    expect(
      screen.queryByRole('button', { name: /retry|recreate/i })
    ).not.toBeInTheDocument()
  }
)

test('submission references survive nested API errors without accepting invalid statuses', () => {
  expect(
    getSubmissionReference({
      error: { submission_id: 'pending-id', submission_status: 'unknown' },
    })
  ).toEqual({ submission_id: 'pending-id', submission_status: 'unknown' })
  expect(
    getSubmissionReference({ submission_id: 'id', submission_status: 'failed' })
  ).toBeUndefined()
})

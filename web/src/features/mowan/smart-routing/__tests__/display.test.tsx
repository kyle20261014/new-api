import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { expect, test, vi } from 'vitest'

import { CandidateCard } from '../candidate-card'
import { getSubmissionReference } from '../submission-reference'
import { SubmissionView } from '../submission-view'
import type { RoutingCandidate, RoutingSubmission } from '../types'

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

test.each([
  [
    true,
    'tier("base", u("seconds") * 3 * (hour("UTC") >= 0 ? 2 : 1)) * (day("UTC") >= 1 ? 3 : 1)',
  ] as const,
  [
    true,
    '(tier("base", u("seconds") * 3)) * ((hour("UTC") >= 0 ? 2 : 1) > 1 ? 3 : 1)',
  ] as const,
  ...[true, false].flatMap((task) =>
    ['+2', '-0'].flatMap((multiplier) =>
      [false, true].map(
        (mixed) =>
          [
            task,
            `tier("base", ${task ? 'u("seconds")' : 'p'} * 3) * (hour("UTC") >= 0 ? ${multiplier} : 1)${mixed ? ' * (day("UTC") >= 1 ? 3 : 1)' : ''}`,
          ] as const
      )
    )
  ),
])(
  'complex or incomplete pricing retains its entire rule in the card and clipboard (task=%s): %s',
  async (task, rule) => {
    const user = userEvent.setup()
    const clipboard = vi
      .spyOn(navigator.clipboard, 'writeText')
      .mockResolvedValue()
    const candidate: RoutingCandidate = {
      model: 'task',
      display_name: 'task',
      category: 'video',
      group: 'standard',
      available: true,
      health: 'normal',
      quote: {
        comparison: 'available',
        currency: 'USD',
        prices: [],
        expression: {
          rule,
          task,
          group_ratio: 1,
          usage_schema: {
            seconds: {
              type: 'number',
              unit: 'second',
              description: 'Duration',
            },
          },
        },
      },
      metrics: {
        protocol_class: 'task_async',
        response_kind: 'task_terminal',
        response_ms_1h: null,
        response_samples_1h: 0,
        success_24h: null,
        samples_24h: 0,
        updated_at: null,
        stale: true,
      },
    }
    render(
      <I18nextProvider i18n={i18n}>
        <CandidateCard candidate={candidate} />
      </I18nextProvider>
    )
    await user.click(
      screen.getByRole('button', { name: 'Copy effective prices' })
    )
    const copied = clipboard.mock.calls[0][0]
    expect(copied).toContain('Special pricing')
    expect(copied).toContain(rule)
    expect(copied).not.toContain('Base price')
    expect(copied).not.toContain('Otherwise ×1')
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.getByText('Special pricing')).toBeVisible()
  }
)

test.each([false, true])(
  'price table and copied quote preserve request conditions or unavailable-provider meaning (incompatible=%s)',
  async (incompatible) => {
    const user = userEvent.setup()
    const clipboard = vi
      .spyOn(navigator.clipboard, 'writeText')
      .mockResolvedValue()
    const rule = incompatible
      ? 'tier("seconds", u("seconds") * 3 + u("images") * 10)'
      : '(tier("seconds", u("seconds") * 3)) * (hour("UTC") >= 0 ? 2 : 1)'
    const reason =
      'This provider cannot supply all usage fields required by the pricing rule.'
    const candidate: RoutingCandidate = {
      model: 'fixture/task',
      display_name: 'fixture/task',
      category: 'video',
      group: 'standard',
      available: true,
      health: 'normal',
      quote: {
        comparison: 'unavailable',
        reason: incompatible
          ? 'provider_usage_incompatible'
          : 'request_usage_unknown',
        currency: 'USD',
        prices: [],
        expression: {
          rule,
          task: true,
          group_ratio: 1,
          usage_schema: {
            seconds: {
              type: 'number',
              unit: 'second',
              description: 'Duration',
            },
          },
        },
      },
      metrics: {
        protocol_class: 'task_async',
        response_kind: 'task_terminal',
        response_ms_1h: null,
        response_samples_1h: 0,
        success_24h: null,
        samples_24h: 0,
        updated_at: null,
        stale: true,
      },
    }
    render(
      <I18nextProvider i18n={i18n}>
        <CandidateCard candidate={candidate} />
      </I18nextProvider>
    )
    if (incompatible) {
      expect(screen.queryByRole('table')).not.toBeInTheDocument()
      expect(screen.getByText(reason)).toBeVisible()
    } else {
      expect(
        screen.getByRole('row', { name: 'Base price · Duration $3 / s' })
      ).toBeVisible()
      expect(screen.getByRole('table')).toHaveTextContent('Otherwise ×1')
      expect(screen.getByRole('table')).toHaveTextContent('UTC')
    }
    await user.click(
      screen.getByRole('button', { name: 'Copy effective prices' })
    )
    const copied = clipboard.mock.calls[0][0]
    if (incompatible) {
      expect(copied).toContain(reason)
      expect(copied).toContain(rule)
      expect(copied).not.toContain('Duration: $3')
    } else {
      expect(copied).toContain('Base price · Duration: $3 / s')
      expect(copied).toContain('UTC')
      expect(copied).toContain('×2 · Otherwise ×1')
    }
  }
)

test.each([
  ['channel_pricing_ambiguous', 'Prices cannot be compared yet.'],
  ['mixed_provider_pricing', 'Prices cannot be compared yet.'],
  ['estimate_unavailable', 'The cost of this request cannot be estimated yet.'],
  [
    'different_billing_contract',
    'Different billing methods prevent a price comparison.',
  ],
  [
    'request_usage_unknown',
    'Usage is not yet known, so the total cost cannot be estimated.',
  ],
])(
  'an unavailable quote with %s does not claim an unsupported cause',
  (reason, message) => {
    const candidate: RoutingCandidate = {
      model: 'chat',
      display_name: 'Chat',
      group: 'standard',
      category: 'text',
      available: true,
      health: 'unknown',
      quote: { comparison: 'unavailable', reason, currency: 'USD', prices: [] },
      metrics: {
        protocol_class: 'openai',
        response_kind: 'ttft',
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
    expect(screen.getAllByText(message).length).toBeGreaterThan(0)
    if (reason !== 'different_billing_contract') {
      expect(
        screen.queryByText(
          'Different billing methods prevent a price comparison.'
        )
      ).not.toBeInTheDocument()
    }
    if (reason !== 'request_usage_unknown') {
      expect(
        screen.queryByText(
          'Usage is not yet known, so the total cost cannot be estimated.'
        )
      ).not.toBeInTheDocument()
    }
  }
)

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
  expect(
    screen.getByRole('row', { name: 'Free input $0.00 / token' })
  ).toBeInTheDocument()
  expect(
    screen.getByRole('row', {
      name: 'Output $0.10 – $0.80 / image · resolution',
    })
  ).toBeInTheDocument()
  expect(
    screen.getByRole('row', { name: 'Audio Unknown / second' })
  ).toBeInTheDocument()
  expect(
    screen.getByRole('row', { name: 'Request pricing rule 1.25 / Multiplier' })
  ).toBeInTheDocument()
  expect(
    screen.getByRole('row', { name: 'Input tokens $0.005 / 1M token' })
  ).toBeInTheDocument()
  expect(
    screen.getByText(
      'Usage is not yet known, so the total cost cannot be estimated.'
    )
  ).toBeInTheDocument()
  expect(
    screen.getByText('Task completion time · Last hour:')
  ).toBeInTheDocument()
  expect(
    screen.getByText('Call success rate · Last 24 hours:')
  ).toBeInTheDocument()
  expect(screen.getAllByText('Unknown · Calls counted: 0')).toHaveLength(2)
})

test('protocol observations preserve separate response kinds, unknowns, samples and stale timestamps', async () => {
  const metrics: RoutingCandidate['metrics'] = {
    protocol_class: 'openai',
    response_kind: 'ttft',
    response_ms_1h: 3,
    response_samples_1h: 2,
    success_24h: 0.5,
    samples_24h: 4,
    stale: false,
    updated_at: 1000,
  }
  const candidate: RoutingCandidate = {
    model: 'mixed',
    display_name: 'Mixed',
    group: 'standard',
    category: 'text',
    available: true,
    health: 'normal',
    quote: { comparison: 'available', currency: 'USD', prices: [] },
    metrics,
    variants: [
      {
        ...metrics,
        provider_key: 'openai',
        operation_key: 'stream',
        health: 'normal',
      },
      {
        ...metrics,
        provider_key: 'tasks',
        operation_key: 'image',
        health: 'unknown',
        response_kind: 'task_terminal',
        response_ms_1h: null,
        response_samples_1h: 0,
        success_24h: null,
        samples_24h: 0,
        updated_at: null,
        stale: true,
      },
    ],
  }
  render(
    <I18nextProvider i18n={i18n}>
      <CandidateCard candidate={candidate} />
    </I18nextProvider>
  )
  await userEvent.click(screen.getByText('Protocol observations'))
  const observations = screen
    .getByText('Protocol observations')
    .closest('details')
  if (!observations) throw new Error('Missing protocol observations')
  const entries = within(observations).getAllByRole('listitem')
  expect(entries[0]).toHaveTextContent(
    'Time to start replying · Last hour: 3 ms · Calls counted: 2'
  )
  expect(entries[0]).toHaveTextContent(
    'Call success rate · Last 24 hours: 50% · Calls counted: 4'
  )
  expect(entries[1]).toHaveTextContent(
    'Task completion time · Last hour: Unknown · Calls counted: 0'
  )
  expect(entries[1]).toHaveTextContent(
    'Updated: Unknown · Older data, for reference only'
  )
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

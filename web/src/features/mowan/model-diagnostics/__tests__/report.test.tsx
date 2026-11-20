import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { DiagnosticReportView } from '../report'
import { reportIsStale, type DiagnosticReport } from '../types'

const report: DiagnosticReport = {
  id: 'partial',
  model: 'chat-model',
  group: 'standard',
  kind: 'intelligence',
  creative: false,
  state: 'partial',
  created_at: 1_800_000_000,
  updated_at: 1_800_000_001,
  completed_at: 0,
  stale: false,
  version: 'v1',
  owned: false,
  fee: { status: 'settled', quota: 500000 },
  steps: [
    {
      id: 'q1',
      title: 'Q1',
      prompt: 'Question',
      complete: false,
      state: 'failed',
    },
  ],
  result: { status: 'incomplete', score: 0, complete: false, version: 'v1' },
}

test('incomplete results never display a zero score or another user’s actual charge', () => {
  render(<DiagnosticReportView report={report} />)
  expect(screen.getByText('—')).toBeVisible()
  expect(screen.queryByText(/My charge/)).not.toBeInTheDocument()
  expect(screen.getByText(/0 of 1 steps completed/)).toBeVisible()
})

test('reports cross the outdated boundary at exactly 24 hours and respect explicit staleness', () => {
  expect(reportIsStale(report, report.created_at * 1000 + 86_399_999)).toBe(
    false
  )
  expect(reportIsStale(report, report.created_at * 1000 + 86_400_000)).toBe(
    true
  )
  expect(
    reportIsStale({ ...report, stale: true }, report.created_at * 1000)
  ).toBe(true)
})

test('explicit null usage stays unknown and report age starts at completion', () => {
  const completed = {
    ...report,
    owned: true,
    fee: { status: 'pending', quota: null },
    steps: [
      {
        id: 'null-usage',
        title: 'Usage sample',
        prompt: 'Question',
        complete: false,
        state: 'failed',
        input_tokens: null,
        output_tokens: null,
        reasoning_tokens: null,
        duration_ms: null,
      },
    ],
    completed_at: report.created_at + 100,
  }
  // Usage detail for a non-connection report lives in the technical view, so
  // the null token fields stay visible there instead of the normal summary.
  render(<DiagnosticReportView report={completed} technical />)
  expect(screen.getAllByText(/Unknown/)).toHaveLength(2)
  expect(reportIsStale(completed, report.created_at * 1000 + 86_400_000)).toBe(
    false
  )
})

test.each([
  {
    status: 'completed',
    complete: true,
    passed: true,
    anomaly: 'detected',
    expected: 'Structural anomaly',
  },
  {
    status: 'completed',
    complete: true,
    passed: true,
    anomaly: 'not_detected',
    expected: 'Structure passed',
  },
  {
    status: 'completed',
    complete: true,
    passed: false,
    anomaly: 'not_detected',
    expected: 'Structure failed',
  },
  {
    status: 'unknown',
    complete: false,
    passed: undefined,
    anomaly: 'unknown',
    expected: 'Unable to determine',
  },
  {
    status: 'incomplete',
    complete: false,
    passed: true,
    anomaly: 'not_detected',
    expected: 'Incomplete',
  },
])('drawing main verdict is $expected', (result) => {
  render(
    <DiagnosticReportView
      report={{
        ...report,
        kind: 'drawing',
        result: {
          ...result,
          score: result.complete ? 95 : null,
          details: { anomaly: result.anomaly },
          version: 'v1',
        },
      }}
    />
  )
  expect(screen.getByText(result.expected)).toBeVisible()
  expect(screen.queryByText('Passed')).not.toBeInTheDocument()
  expect(screen.queryByText('Failed')).not.toBeInTheDocument()
})

test.each([true, false])('non-drawing verdict retains passed=%s', (passed) => {
  render(
    <DiagnosticReportView
      report={{
        ...report,
        result: { status: 'completed', complete: true, passed, version: 'v1' },
      }}
    />
  )
  expect(screen.getByText(passed ? 'Passed' : 'Failed')).toBeVisible()
})

test('a stuck running step in a finished report reports the report state', () => {
  render(
    <DiagnosticReportView
      report={{
        ...report,
        kind: 'connection',
        state: 'review_pending',
        steps: [
          {
            id: 'conn',
            title: 'Connection',
            prompt: 'ping',
            complete: false,
            state: 'running',
          },
        ],
        result: undefined,
      }}
    />
  )
  expect(screen.getByText(/Connection · Review pending/)).toBeVisible()
  expect(screen.queryByText(/Connection · Running/)).not.toBeInTheDocument()
})

test('artwork copy and download preserve the raw answer rather than the isolated preview document', async () => {
  const source =
    '<!DOCTYPE html>\n<html><body>原始 SVG<script>window.art = 1</script></body></html>\n'
  const user = userEvent.setup()
  const write = vi.spyOn(navigator.clipboard, 'writeText')
  const createObjectURL = vi.fn((_blob: Blob) => 'blob:artwork')
  vi.stubGlobal(
    'URL',
    Object.assign(class extends URL {}, {
      createObjectURL,
      revokeObjectURL: vi.fn(),
    })
  )
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, 'click')
    .mockImplementation(() => {})
  try {
    render(
      <DiagnosticReportView
        report={{
          ...report,
          kind: 'drawing',
          steps: [
            {
              id: 'art',
              title: 'Artwork',
              prompt: 'Draw',
              state: 'completed',
              complete: true,
              answer: source,
            },
          ],
        }}
      />
    )
    await user.click(screen.getByText('Original code'))
    await user.click(screen.getByRole('button', { name: 'Copy code' }))
    await waitFor(() => expect(write).toHaveBeenCalledWith(source))
    await user.click(screen.getByRole('button', { name: 'Download' }))
    expect(click).toHaveBeenCalledTimes(1)
    const blob = createObjectURL.mock.calls[0]?.[0]
    expect(blob).toBeInstanceOf(Blob)
    if (!blob) throw new Error('Artwork download did not create a Blob')
    const downloaded = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.addEventListener('load', () => resolve(String(reader.result)))
      reader.addEventListener('error', reject)
      reader.readAsText(blob)
    })
    expect(downloaded).toBe(source)
    expect(
      screen.getByTitle('Artwork preview').getAttribute('srcdoc')
    ).not.toBe(source)
  } finally {
    vi.unstubAllGlobals()
  }
})

function intelligencePartial(judged: number) {
  return {
    ...report,
    state: 'partial' as const,
    steps: [
      {
        id: 'q1',
        title: 'logic',
        prompt: 'p1',
        complete: true,
        state: 'completed' as const,
      },
      {
        id: 'q2',
        title: 'logic',
        prompt: 'p2',
        complete: true,
        state: 'completed' as const,
      },
    ],
    result: {
      status: 'unknown',
      score: null,
      complete: false,
      version: 'v1',
      reasons: ['code_not_in_proven_subset'],
      details: {
        completed: 2,
        judged,
        total: 2,
        weighted_got: judged > 0 ? 2 : 0,
        weighted_max: 5,
        items: [
          { id: 'q1', prompt: 'p1', weight: 2, passed: judged > 0 },
          { id: 'q2', prompt: 'p2', weight: 3, passed: null },
        ],
      },
    },
  }
}

test('a partial score never also claims that no score can be given', () => {
  render(<DiagnosticReportView report={intelligencePartial(1)} />)
  expect(screen.getByText('Partially completed')).toBeVisible()
  expect(screen.getAllByText('2 / 5').length).toBeGreaterThan(0)
  expect(screen.getByText('1 / 2')).toBeVisible()
  expect(screen.getByText('Graded weight')).toBeVisible()
  expect(
    screen.queryByText(
      'Some answers could not be verified automatically, so no score can be given.'
    )
  ).not.toBeInTheDocument()
})

test('a zero score with one graded answer is still a partial score', () => {
  const zero = intelligencePartial(1)
  zero.result.details.weighted_got = 0
  zero.result.details.items[0].passed = false
  render(<DiagnosticReportView report={zero} />)
  expect(screen.getByText('Partial score')).toBeVisible()
  expect(screen.getAllByText('0 / 5').length).toBeGreaterThan(0)
})

test('nothing graded means no score at all', () => {
  render(<DiagnosticReportView report={intelligencePartial(0)} />)
  expect(screen.queryByText('Partial score')).not.toBeInTheDocument()
  expect(screen.queryByText('Graded weight')).not.toBeInTheDocument()
  expect(screen.queryByText('Partially completed')).not.toBeInTheDocument()
})

test('drawing breakdown shows earned points, not each item’s maximum', () => {
  render(
    <DiagnosticReportView
      report={{
        ...report,
        kind: 'drawing',
        result: {
          status: 'completed',
          score: 80,
          passed: true,
          complete: true,
          version: 'v1',
          details: {
            structural_points: 105,
            structural_penalty: 25,
            anomaly_penalty: 0,
            metrics: [
              {
                key: 'valid_svg',
                value: 'yes',
                points: 40,
                max: 40,
                known: true,
              },
              { key: 'loop', points: 0, max: 25, known: false },
              {
                key: 'external',
                points: 25,
                max: 25,
                penalty: true,
                known: true,
              },
            ],
          },
        },
      }}
    />
  )
  expect(screen.getByText('+40')).toBeVisible()
  const externalRow = screen.getByText('External references').closest('tr')
  expect(externalRow).not.toBeNull()
  expect(within(externalRow as HTMLElement).getByText('−25')).toBeVisible()
  // An unreadable metric stays unknown instead of scoring +0.
  expect(screen.queryByText('+0')).not.toBeInTheDocument()
  expect(screen.getAllByText('Unknown').length).toBeGreaterThan(0)
  expect(screen.getByText('Points earned')).toBeVisible()
})

test('reports from before per-item details degrade to stored totals', () => {
  render(
    <DiagnosticReportView
      report={{
        ...report,
        kind: 'drawing',
        result: {
          status: 'completed',
          score: 80,
          passed: true,
          complete: true,
          version: 'v1',
          details: { structural_points: 105, structural_penalty: 25 },
        },
      }}
    />
  )
  expect(
    screen.getByText(
      'This report predates per-item scoring details, so only the stored totals are shown.'
    )
  ).toBeVisible()
  expect(screen.queryByText('Points earned')).not.toBeInTheDocument()
  expect(screen.getByText('Total score')).toBeVisible()
})

test('anomaly codes are translated and unknown codes stay out of the normal report', () => {
  render(
    <DiagnosticReportView
      report={{
        ...report,
        kind: 'drawing',
        result: {
          status: 'completed',
          score: 55,
          passed: false,
          complete: true,
          version: 'v1',
          reasons: ['missing_svg'],
          details: {
            anomaly: 'detected',
            anomaly_penalty: 30,
            structural_points: 105,
            structural_penalty: 20,
            unknown_metrics: ['javascript_dynamic_content', 'future_code'],
            metrics: [
              {
                key: 'anomaly',
                value: 'missing_svg, no_loop_animation, future_code',
                points: 30,
                max: 30,
                penalty: true,
                known: true,
              },
            ],
          },
        },
      }}
    />
  )
  expect(
    screen.getByText('missing_svg; no_loop_animation')
  ).toBeVisible()
  expect(screen.queryByText('no_loop_animation')).not.toBeInTheDocument()
  expect(screen.queryByText(/future_code/)).not.toBeInTheDocument()
  expect(
    screen.getByText(/JavaScript dynamic content cannot be scored statically\./)
  ).toBeVisible()
})

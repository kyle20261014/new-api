import { render, screen, waitFor } from '@testing-library/react'
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
  render(<DiagnosticReportView report={completed} />)
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

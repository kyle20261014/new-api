/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { expect, test } from 'vitest'

import {
  drawingResult,
  fingerprintResult,
  partialScore,
  stepOutcome,
  summarizeReport,
} from '../display'
import type { DiagnosticReport, DiagnosticStep } from '../types'

function report(overrides: Partial<DiagnosticReport> = {}): DiagnosticReport {
  return {
    id: 'report',
    model: 'chat-model',
    group: 'standard',
    kind: 'intelligence',
    creative: false,
    state: 'completed',
    created_at: 1_800_000_000,
    updated_at: 1_800_000_100,
    completed_at: 1_800_000_100,
    stale: false,
    version: 'v1',
    owned: false,
    steps: [],
    ...overrides,
  }
}

test('a complete graded report exposes its real score and pass verdict', () => {
  const summary = summarizeReport(
    report({
      result: {
        status: 'completed',
        score: 88,
        passed: true,
        complete: true,
        version: 'v1',
      },
    })
  )
  expect(summary.score).toBe(88)
  expect(summary.tone).toBe('passed')
  expect(summary.labelKey).toBe('Passed')
})

test('partial runs never invent a score and stay visibly unfinished', () => {
  const summary = summarizeReport(
    report({
      state: 'partial',
      result: {
        status: 'unknown',
        score: 0,
        complete: false,
        version: 'v1',
        reasons: ['code_not_in_proven_subset'],
      },
    })
  )
  expect(summary.score).toBeUndefined()
  expect(summary.tone).toBe('unknown')
  expect(summary.labelKey).toBe('Unable to determine')
})

test('a null verdict is unknown, never read as a failure', () => {
  const summary = summarizeReport(
    report({
      result: {
        status: 'unknown',
        score: null,
        passed: null,
        complete: true,
        version: 'v1',
        reasons: ['code_not_in_proven_subset'],
      },
    })
  )
  expect(summary.score).toBeUndefined()
  expect(summary.tone).toBe('unknown')
  expect(summary.labelKey).toBe('Unable to determine')
})

test('a completed run without a verdict flag reads as completed, not unknown', () => {
  const summary = summarizeReport(
    report({
      kind: 'fingerprint',
      result: { status: 'completed', complete: true, version: 'v1' },
    })
  )
  expect(summary.score).toBeUndefined()
  expect(summary.tone).toBe('passed')
  expect(summary.labelKey).toBe('Completed')
})

test('intelligence reports surface how many answers could be graded', () => {
  const summary = summarizeReport(
    report({
      state: 'partial',
      result: {
        status: 'unknown',
        complete: false,
        version: 'v1',
        details: { judged: 12, total: 18, items: [] },
      },
    })
  )
  expect(summary.judged).toEqual({ judged: 12, total: 18 })
})

test('a fingerprint ranking admits when the detector had too little evidence', () => {
  const ranked = fingerprintResult(
    report({
      kind: 'fingerprint',
      result: {
        status: 'completed',
        complete: true,
        version: 'v1',
        details: {
          analysis: {
            prediction_name: 'model-a',
            probability: 0.9,
            used_outputs: 2,
            probability_status: 'reference_calibrated',
            results: [
              { display_name: 'model-a', family_name: 'A', probability: 0.9 },
            ],
            evidence: { insufficient: true },
          },
        },
      },
    })
  )
  expect(ranked?.insufficient).toBe(true)
  expect(ranked?.calibrated).toBe(true)
})

test('a fingerprint name without a similarity never becomes 0%', () => {
  const result = fingerprintResult(
    report({
      kind: 'fingerprint',
      result: {
        status: 'completed',
        complete: true,
        version: 'v1',
        details: {
          analysis: {
            prediction_name: 'model-a',
            family_prediction_name: 'A',
            probability_status: 'reference_calibrated',
            evidence: { insufficient: true },
          },
        },
      },
    })
  )
  expect(result?.best?.model).toBe('model-a')
  expect(result?.best?.probability).toBeUndefined()
})

test('partial credit keeps real weights and needs at least one graded answer', () => {
  const partial = report({
    state: 'partial',
    result: {
      status: 'unknown',
      complete: false,
      version: 'v1',
      details: {
        judged: 1,
        total: 2,
        weighted_max: 5,
        items: [
          { id: 'q1', prompt: 'p1', weight: 2, passed: true },
          { id: 'q2', prompt: 'p2', weight: 3, passed: null },
        ],
      },
    },
  })
  expect(partialScore(partial)).toEqual({
    got: 2,
    max: 5,
    judgedWeight: 2,
    judged: 1,
    total: 2,
  })

  const noneGraded = report({
    state: 'partial',
    result: {
      status: 'unknown',
      complete: false,
      version: 'v1',
      details: {
        judged: 0,
        total: 2,
        weighted_max: 5,
        items: [
          { id: 'q1', prompt: 'p1', weight: 2, passed: null },
          { id: 'q2', prompt: 'p2', weight: 3, passed: null },
        ],
      },
    },
  })
  expect(partialScore(noneGraded)).toBeUndefined()
})

test('drawing details publish each item’s real points and unknown metrics', () => {
  const result = drawingResult(
    report({
      kind: 'drawing',
      result: {
        status: 'completed',
        score: 80,
        complete: true,
        version: 'v1',
        details: {
          structural_points: 105,
          structural_penalty: 25,
          anomaly_penalty: 0,
          metrics: [
            { key: 'valid_svg', value: 'yes', points: 40, max: 40, known: true },
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
    })
  )
  expect(result?.hasBreakdown).toBe(true)
  expect(result?.metrics.map((metric) => metric.points)).toEqual([40, 0, 25])
  expect(result?.metrics[1].known).toBe(false)
  expect(result?.metrics[2].penalty).toBe(true)

  const legacy = drawingResult(
    report({
      kind: 'drawing',
      result: {
        status: 'completed',
        score: 80,
        complete: true,
        version: 'v1',
        details: { structural_points: 105, structural_penalty: 25 },
      },
    })
  )
  expect(legacy?.hasBreakdown).toBe(false)
})

test('a drawing anomaly overrides a passed flag and reports no score when incomplete', () => {
  const anomaly = summarizeReport(
    report({
      kind: 'drawing',
      result: {
        status: 'completed',
        score: 95,
        passed: true,
        complete: true,
        version: 'v1',
        details: { anomaly: 'detected' },
      },
    })
  )
  expect(anomaly.score).toBe(95)
  expect(anomaly.tone).toBe('failed')
  expect(anomaly.labelKey).toBe('Structural anomaly')

  const incomplete = summarizeReport(
    report({
      kind: 'drawing',
      state: 'partial',
      result: {
        status: 'incomplete',
        score: null,
        complete: false,
        version: 'v1',
        details: { anomaly: 'unknown' },
      },
    })
  )
  expect(incomplete.score).toBeUndefined()
  expect(incomplete.labelKey).toBe('Incomplete')
})

test('a queued or running report shows live state without a score', () => {
  const summary = summarizeReport(report({ state: 'running', result: undefined }))
  expect(summary.score).toBeUndefined()
  expect(summary.tone).toBe('pending')
  expect(summary.labelKey).toBe('Running')
})

test('per-question grading keeps graded, failed and unjudged answers apart', () => {
  // The server keys steps by step id and grading items by question id; only
  // the prompt is shared between them.
  const step = (id: string, prompt: string): DiagnosticStep => ({
    id,
    title: 'logic',
    prompt,
    state: 'completed',
    complete: true,
  })
  const graded = report({
    steps: [step('s1', 'p1'), step('s2', 'p2'), step('s3', 'p3')],
    result: {
      status: 'unknown',
      complete: false,
      version: 'v1',
      details: {
        judged: 2,
        total: 3,
        items: [
          { id: 'q1', prompt: 'p1', passed: true },
          { id: 'q2', prompt: 'p2', passed: false },
          { id: 'q3', prompt: 'p3', passed: null },
        ],
      },
    },
  })
  expect(stepOutcome(graded, 's1')).toBe('passed')
  expect(stepOutcome(graded, 's2')).toBe('failed')
  expect(stepOutcome(graded, 's3')).toBe('unknown')
  expect(stepOutcome(graded, 'missing')).toBeUndefined()
})

function intelligenceWith(
  steps: DiagnosticStep[],
  items: Record<string, unknown>[]
): DiagnosticReport {
  return report({
    steps,
    result: {
      status: 'unknown',
      complete: false,
      version: 'v1',
      details: { judged: 1, total: steps.length, items },
    },
  })
}

test('reordered grading items still follow their own prompt', () => {
  const step = (id: string, prompt: string): DiagnosticStep => ({
    id,
    title: 'logic',
    prompt,
    state: 'completed',
    complete: true,
  })
  // Items arrive in a different order than the steps, and the first item's
  // verdict must not leak onto the first step.
  const reordered = intelligenceWith(
    [step('s1', 'p1'), step('s2', 'p2')],
    [
      { id: 'q2', prompt: 'p2', passed: false },
      { id: 'q1', prompt: 'p1', passed: true },
    ]
  )
  expect(stepOutcome(reordered, 's1')).toBe('passed')
  expect(stepOutcome(reordered, 's2')).toBe('failed')
})

test('a missing or ambiguous grading item is never graded by position', () => {
  const step = (id: string, prompt: string): DiagnosticStep => ({
    id,
    title: 'logic',
    prompt,
    state: 'completed',
    complete: true,
  })
  // A short item list would shift every later step onto the wrong question.
  const shortList = intelligenceWith(
    [step('s1', 'p1'), step('s2', 'p2')],
    [{ id: 'q2', prompt: 'p2', passed: false }]
  )
  expect(stepOutcome(shortList, 's1')).toBeUndefined()
  expect(stepOutcome(shortList, 's2')).toBe('failed')

  // Two items sharing a prompt cannot be told apart either.
  const ambiguous = intelligenceWith(
    [step('s1', 'p1')],
    [
      { id: 'q1', prompt: 'p1', passed: true },
      { id: 'q1b', prompt: 'p1', passed: false },
    ]
  )
  expect(stepOutcome(ambiguous, 's1')).toBeUndefined()

  // An item list that does not repeat the prompt at all stays unmarked.
  const noPrompt = intelligenceWith([step('s1', 'p1')], [{ id: 'q1', passed: true }])
  expect(stepOutcome(noPrompt, 's1')).toBeUndefined()
})

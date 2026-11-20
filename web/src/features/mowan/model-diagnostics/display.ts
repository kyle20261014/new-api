import type { DiagnosticKind, DiagnosticReport } from './types'

export function diagnosticStateKey(state: string): string {
  const keys: Record<string, string> = {
    queued: 'Queued',
    running: 'Running',
    completed: 'Completed',
    partial: 'Partially completed',
    failed: 'Failed',
    unauthorized: 'Permission expired',
    review_pending: 'Review pending',
    settled: 'Settled',
    pending: 'Pending',
    passed: 'Passed',
    incomplete: 'Incomplete',
    unknown: 'Unable to determine',
    structural_anomaly: 'Structural anomaly',
    structure_passed: 'Structure passed',
    structure_failed: 'Structure failed',
    not_applicable: 'Not applicable',
  }
  return keys[state] ?? state
}

// Internal reason codes stay in the technical report. The normal view shows a
// plain-language explanation so users never read protocol identifiers.
export function diagnosticReasonKey(reason: string): string | undefined {
  const keys: Record<string, string> = {
    code_not_in_proven_subset:
      'Some answers could not be verified automatically, so no score can be given.',
    not_all_questions_completed:
      'Not every question received a complete answer.',
    no_complete_reply: 'No complete reply was received.',
    duplicate_answer_id: 'The reply could not be attributed safely.',
    static_even_sum_loop_proven_for_safe_integer_sums: '',
    static_even_sum_formula_proven_for_safe_integer_sums: '',
  }
  return keys[reason]
}

export type DiagnosticTone = 'passed' | 'failed' | 'pending' | 'unknown' | 'idle'

export interface DiagnosticSummary {
  hasReport: boolean
  score?: number
  tone: DiagnosticTone
  // Text for the compact pill, already an i18n key when `labelIsKey` is true.
  labelKey: string
  judged?: { judged: number; total: number }
  // Present only for a partially graded intelligence report.
  partial?: PartialScore
}

function judgedProgress(report: DiagnosticReport): DiagnosticSummary['judged'] {
  if (report.kind !== 'intelligence' || !report.result?.details) return undefined
  const details = report.result.details
  if (details === null || typeof details !== 'object') return undefined
  const judged = Reflect.get(details, 'judged')
  const total = Reflect.get(details, 'total')
  if (typeof judged !== 'number' || typeof total !== 'number') return undefined
  if (!Number.isFinite(judged) || !Number.isFinite(total)) return undefined
  return { judged, total }
}

function hasAnomaly(details: unknown): boolean {
  return (
    details !== null &&
    typeof details === 'object' &&
    Reflect.get(details, 'anomaly') === 'detected'
  )
}

// One place decides how a report turns into a score, verdict and tone so the
// pill row, the report header and tests never disagree.
export function summarizeReport(report: DiagnosticReport): DiagnosticSummary {
  const result = report.result
  const judged = judgedProgress(report)
  if (report.state === 'queued' || report.state === 'running') {
    return {
      hasReport: true,
      tone: 'pending',
      labelKey: diagnosticStateKey(report.state),
      judged,
    }
  }
  if (!result) {
    return {
      hasReport: true,
      tone: report.state === 'failed' ? 'failed' : 'unknown',
      labelKey: diagnosticStateKey(report.state),
      judged,
    }
  }
  let verdict = diagnosticStateKey(result.status)
  // JSON null means the run reached no verdict; it must not read as failed.
  let passed = result.passed ?? undefined
  let structuralTone: DiagnosticTone | undefined
  if (report.kind === 'drawing') {
    passed = undefined
    if (result.complete) {
      if (hasAnomaly(result.details)) {
        verdict = diagnosticStateKey('structural_anomaly')
        structuralTone = 'failed'
      } else if (result.passed === true) {
        verdict = diagnosticStateKey('structure_passed')
        structuralTone = 'passed'
      } else if (result.passed === false) {
        verdict = diagnosticStateKey('structure_failed')
        structuralTone = 'failed'
      }
    }
  } else {
    if (result.passed === true) verdict = 'Passed'
    if (result.passed === false) verdict = 'Failed'
  }
  const score =
    result.complete &&
    typeof result.score === 'number' &&
    Number.isFinite(result.score)
      ? result.score
      : undefined
  if (score !== undefined && passed !== undefined) {
    return {
      hasReport: true,
      score,
      tone: passed ? 'passed' : 'failed',
      labelKey: verdict,
      judged,
    }
  }
  if (passed !== undefined) {
    return {
      hasReport: true,
      tone: passed ? 'passed' : 'failed',
      labelKey: verdict,
      judged,
    }
  }
  if (score !== undefined) {
    // Drawing has no pass/fail beyond its structural verdict text.
    return {
      hasReport: true,
      score,
      tone: structuralTone ?? completedTone(result.status),
      labelKey: verdict,
      judged,
    }
  }
  // A partly graded intelligence paper keeps its earned weighted points; the
  // ungraded questions earn nothing and are never treated as wrong.
  const partial = partialScore(report)
  if (partial) {
    return {
      hasReport: true,
      score: partial.got,
      tone: 'pending',
      labelKey: 'Partial score',
      judged,
      partial,
    }
  }
  return {
    hasReport: true,
    // A completed run with no pass/fail flag (connection, fingerprint) did
    // finish successfully; only a non-verdict status stays unknown.
    tone: result.complete ? completedTone(result.status) : 'unknown',
    labelKey: verdict,
    judged,
  }
}

function completedTone(status: string): DiagnosticTone {
  if (status === 'completed') return 'passed'
  if (status === 'failed') return 'failed'
  return 'unknown'
}

export type StepOutcome = 'passed' | 'failed' | 'unknown'

// A graded boolean, or anything else (null, missing, non-boolean), becomes one
// of the three outcomes without a nested conditional at every call site.
export function outcomeOf(value: unknown): StepOutcome {
  if (value === true) return 'passed'
  if (value === false) return 'failed'
  return 'unknown'
}

// Intelligence items carry a graded outcome per question; other kinds are
// graded as a whole. Unknown stays unknown, never rendered as a failure.
export function stepOutcome(
  report: DiagnosticReport,
  stepId: string
): StepOutcome | undefined {
  if (report.kind !== 'intelligence' || !report.result?.details) return undefined
  const details = report.result.details
  if (details === null || typeof details !== 'object') return undefined
  const items = Reflect.get(details, 'items')
  if (!Array.isArray(items)) return undefined
  const steps = Array.isArray(report.steps) ? report.steps : []
  const index = steps.findIndex((step) => step.id === stepId)
  if (index < 0) return undefined
  // Items are keyed by question id, which never equals the step id, so the
  // prompt is the only reliable link. A missing or ambiguous prompt yields no
  // verdict at all: never guess, and never fall back to positional order, or a
  // reordered or short item list would grade one question with another's
  // answer.
  const prompt = steps[index].prompt
  if (typeof prompt !== 'string' || prompt === '') return undefined
  const matches = items.filter(
    (entry) =>
      entry !== null &&
      typeof entry === 'object' &&
      Reflect.get(entry, 'prompt') === prompt
  )
  if (matches.length !== 1) return undefined
  const item = matches[0]
  const passed = Reflect.get(item, 'passed')
  // JSON null means "not judged"; booleans are the graded outcome.
  if (passed === true) return 'passed'
  if (passed === false) return 'failed'
  return 'unknown'
}

export function diagnosticLabelKey(kind: DiagnosticKind): string {
  if (kind === 'connection') return 'Connection test'
  if (kind === 'fingerprint') return 'Fingerprint test'
  if (kind === 'intelligence') return 'Intelligence test'
  return 'Drawing test'
}

export interface PartialScore {
  // Weighted points already earned, out of the whole paper's weight.
  got: number
  max: number
  // Weight of the questions that could be graded, out of the whole paper.
  judgedWeight: number
  judged: number
  total: number
}

function readObject(value: unknown): Record<string, unknown> | undefined {
  if (value === null || typeof value !== 'object') return undefined
  return value as Record<string, unknown>
}

function readNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

// Partial credit: graded questions keep their real weight, unjudged questions
// earn nothing and are never treated as wrong or renormalised into a full
// percentage. Zero graded questions means no score at all.
export function partialScore(
  report: DiagnosticReport
): PartialScore | undefined {
  if (report.kind !== 'intelligence') return undefined
  const details = readObject(report.result?.details)
  if (!details) return undefined
  const items = details.items
  const judged = readNumber(details.judged)
  const total = readNumber(details.total)
  const max = readNumber(details.weighted_max)
  if (!Array.isArray(items) || judged === undefined || total === undefined) {
    return undefined
  }
  let judgedWeight = 0
  let got = 0
  let weightSum = 0
  for (const entry of items) {
    const item = readObject(entry)
    if (!item) continue
    const weight = readNumber(item.weight) ?? 0
    weightSum += weight
    const passed = item.passed
    if (passed === true || passed === false) {
      judgedWeight += weight
      if (passed === true) got += weight
    }
  }
  if (judged <= 0 || judged >= total) return undefined
  return { got, max: max ?? weightSum, judgedWeight, judged, total }
}

export interface FingerprintCandidate {
  model: string
  family: string
  // Absent when the server names a prediction without a calibrated score.
  // Never filled in with a placeholder percentage.
  probability?: number
}

export interface FingerprintResult {
  best?: FingerprintCandidate
  candidates: FingerprintCandidate[]
  samples?: number
  // Reference-bank confidence only; never model identity.
  calibrated: boolean
  // The detector itself may flag that too little evidence supports the ranking.
  insufficient: boolean
}

export function fingerprintResult(
  report: DiagnosticReport
): FingerprintResult | undefined {
  if (report.kind !== 'fingerprint') return undefined
  const details = readObject(report.result?.details)
  const analysis = readObject(details?.analysis)
  if (!analysis) return undefined
  const results = Array.isArray(analysis.results) ? analysis.results : []
  const candidates: FingerprintCandidate[] = []
  for (const entry of results) {
    const item = readObject(entry)
    if (!item) continue
    const model = item.display_name ?? item.model
    const probability = readNumber(item.probability)
    if (typeof model !== 'string') continue
    const family = item.family_name ?? item.family
    candidates.push({
      model,
      family: typeof family === 'string' ? family : '',
      probability,
    })
  }
  candidates.sort(
    (a, b) => (b.probability ?? -1) - (a.probability ?? -1)
  )
  const best =
    candidates[0] ??
    (typeof analysis.prediction_name === 'string'
      ? {
          model: analysis.prediction_name,
          family:
            typeof analysis.family_prediction_name === 'string'
              ? analysis.family_prediction_name
              : '',
          probability: readNumber(analysis.probability),
        }
      : undefined)
  return {
    best,
    candidates: candidates.slice(0, 8),
    samples: readNumber(analysis.used_outputs),
    calibrated: analysis.probability_status === 'reference_calibrated',
    insufficient: readObject(analysis.evidence)?.insufficient === true,
  }
}

export interface IntelligenceRow {
  index: number
  // Question identity from the server; steps are joined by prompt only.
  id: string
  topic?: string
  weight: number
  outcome: StepOutcome
  reply: string
  expected: string
}

export function intelligenceRows(report: DiagnosticReport): IntelligenceRow[] {
  if (report.kind !== 'intelligence') return []
  const details = readObject(report.result?.details)
  const items = details && Array.isArray(details.items) ? details.items : []
  const steps = Array.isArray(report.steps) ? report.steps : []
  const rows: IntelligenceRow[] = []
  items.forEach((entry, index) => {
    const item = readObject(entry)
    if (!item) return
    const prompt = typeof item.prompt === 'string' ? item.prompt : ''
    const step = steps.find((candidate) => candidate.prompt === prompt)
    const passed = item.passed
    rows.push({
      index: index + 1,
      id: typeof item.id === 'string' ? item.id : '',
      topic: step?.title,
      weight: readNumber(item.weight) ?? 0,
      outcome: outcomeOf(passed),
      reply: typeof item.reply === 'string' ? item.reply : '',
      expected: typeof item.expected === 'string' ? item.expected : '',
    })
  })
  return rows
}

export interface DrawingMetric {
  key: string
  value?: string
  points: number
  max?: number
  penalty: boolean
  // False when the detector could not read this metric; such rows are shown as
  // unknown and never scored as zero.
  known: boolean
}

// Detector codes that a reader should see in words. Codes outside these maps
// stay in the technical view instead of leaking into the normal report.
const anomalyReasonKeys: Record<string, string> = {
  // The locale files already carry one translation per detector code.
  missing_svg: 'missing_svg',
  unclosed_svg: 'unclosed_svg',
  unfinished_placeholder: 'unfinished_placeholder',
  no_loop_animation: 'no_loop_animation',
  minimal_content_all_three: 'minimal_content_all_three',
}

const unknownMetricKeys: Record<string, string> = {
  colors: 'Colors',
  loop: 'Looping animation',
  elements: 'Elements',
  paths: 'Paths',
  javascript_dynamic_content:
    'JavaScript dynamic content cannot be scored statically.',
  parse: 'The artwork could not be parsed.',
}

export function anomalyReasonKey(code: string): string | undefined {
  return anomalyReasonKeys[code]
}

export function unknownMetricKey(code: string): string | undefined {
  return unknownMetricKeys[code]
}

export interface DrawingResult {
  metrics: DrawingMetric[]
  // Reports created before per-item details existed only store totals.
  hasBreakdown: boolean
  structuralPoints?: number
  penalty?: number
  anomalyPenalty?: number
  anomaly?: string
  unknownMetrics: string[]
  limitation?: string
}

export function drawingResult(
  report: DiagnosticReport
): DrawingResult | undefined {
  if (report.kind !== 'drawing') return undefined
  const details = readObject(report.result?.details)
  if (!details) return undefined
  const metrics: DrawingMetric[] = []
  if (Array.isArray(details.metrics)) {
    for (const entry of details.metrics) {
      const metric = readObject(entry)
      if (!metric || typeof metric.key !== 'string') continue
      metrics.push({
        key: metric.key,
        value:
          typeof metric.value === 'string' && metric.value !== ''
            ? metric.value
            : undefined,
        points: readNumber(metric.points) ?? 0,
        max: readNumber(metric.max),
        penalty: metric.penalty === true,
        known: metric.known !== false,
      })
    }
  }
  const anomaly = details.anomaly
  return {
    metrics,
    hasBreakdown: metrics.length > 0,
    structuralPoints: readNumber(details.structural_points),
    penalty: readNumber(details.structural_penalty),
    anomalyPenalty: readNumber(details.anomaly_penalty),
    anomaly: typeof anomaly === 'string' ? anomaly : undefined,
    unknownMetrics: Array.isArray(details.unknown_metrics)
      ? details.unknown_metrics.filter(
          (entry): entry is string => typeof entry === 'string'
        )
      : [],
    limitation:
      typeof details.limitation === 'string' ? details.limitation : undefined,
  }
}

export function isolatedPreviewDocument(source: string): string {
  // Preserve html/body attributes and never parse the document in the application's DOM.
  // Sandbox blocks top navigation, but CSP cannot guarantee blocking iframe self-navigation.
  return `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; font-src 'none'; connect-src 'none'; media-src 'none'; frame-src 'none'; child-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; navigate-to 'none'"><meta name="referrer" content="no-referrer">${source}`
}

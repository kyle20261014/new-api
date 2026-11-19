export type DiagnosticKind =
  | 'connection'
  | 'fingerprint'
  | 'intelligence'
  | 'drawing'
export type DiagnosticState =
  | 'queued'
  | 'running'
  | 'completed'
  | 'partial'
  | 'failed'
  | 'unauthorized'
  | 'review_pending'

export interface DiagnosticStep {
  id: string
  title: string
  prompt: string
  state: string
  answer?: string
  complete: boolean
  input_tokens?: number | null
  output_tokens?: number | null
  reasoning_tokens?: number | null
  duration_ms?: number | null
  error?: string
}

export interface DiagnosticReport {
  id: string
  model: string
  group: string
  kind: DiagnosticKind
  creative: boolean
  state: DiagnosticState
  created_at: number
  updated_at: number
  completed_at: number
  stale: boolean
  version: string
  materials?: Record<string, string>
  steps: DiagnosticStep[]
  result?: {
    status: string
    score?: number | null
    passed?: boolean | null
    complete: boolean
    reasons?: string[]
    details?: unknown
    version: string
  }
  fee?: { status: string; quota?: number | null }
  owned: boolean
}

export interface DiagnosticCatalog {
  groups: string[]
  chat_supported: boolean
}

export interface StartDiagnostic {
  model: string
  group: string
  kind: DiagnosticKind
  creative: boolean
  idempotency_key: string
}

export function isDiagnosticRunning(report: DiagnosticReport): boolean {
  return report.state === 'queued' || report.state === 'running'
}

export function reportIsStale(
  report: DiagnosticReport,
  now = Date.now()
): boolean {
  const stamp = report.completed_at || report.created_at
  return report.stale || now - stamp * 1000 >= 24 * 60 * 60 * 1000
}

import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  DiagnosticCatalog,
  DiagnosticReport,
  StartDiagnostic,
} from './types'

const endpoint = '/api/mowan/model-diagnostics'
type Response<T> = { success: boolean; message?: string; data: T }

export async function getDiagnosticCatalog(
  model: string,
  signal?: AbortSignal
): Promise<DiagnosticCatalog> {
  const response = await api.get<Response<DiagnosticCatalog>>(
    `${endpoint}/catalog`,
    // React Query owns deduplication; each subscription keeps its own cancellation.
    { params: { model }, signal, disableDuplicate: true }
  )
  return requireServerSuccess(response.data).data
}

export async function getDiagnosticReports(
  model: string,
  group: string,
  signal?: AbortSignal
): Promise<DiagnosticReport[]> {
  const response = await api.get<Response<DiagnosticReport[]>>(
    `${endpoint}/reports`,
    { params: { model, group }, signal, disableDuplicate: true }
  )
  return requireServerSuccess(response.data).data ?? []
}

export async function startDiagnostic(
  payload: StartDiagnostic
): Promise<DiagnosticReport> {
  const response = await api.post<Response<DiagnosticReport>>(
    `${endpoint}/runs`,
    payload
  )
  return requireServerSuccess(response.data).data
}

import { t } from 'i18next'

import type { ApiResponse } from '@/features/keys/types'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  CandidatePage,
  ModelCategory,
  RoutingCandidate,
  RoutingPolicy,
  RoutingPreview,
  RoutingSubmission,
} from './types'

export async function getRoutingCandidates(
  params: {
    model?: string
    search?: string
    category?: ModelCategory
    p?: number
    page_size?: number
    token_id?: number
  },
  signal?: AbortSignal
): Promise<CandidatePage> {
  const response = await api.get<ApiResponse<CandidatePage>>(
    '/api/mowan/smart-routing/candidates',
    { params, signal }
  )
  const body = requireServerSuccess(response.data)
  if (!body.data) throw new Error(t('Failed to load'))
  return body.data
}

export async function getSelectedRoutingCandidates(
  models: string[],
  signal?: AbortSignal,
  tokenId?: number
): Promise<RoutingCandidate[]> {
  const pages = await Promise.all(
    models.map(async (model) => {
      const items: RoutingCandidate[] = []
      for (let page = 1; ; page++) {
        const data = await getRoutingCandidates(
          { model, p: page, page_size: 100, token_id: tokenId },
          signal
        )
        if (!Number.isFinite(data.total) || data.total < 0) {
          throw new Error(t('Failed to load'))
        }
        items.push(...data.items)
        if (!data.items.length || page * 100 >= data.total) return items
      }
    })
  )
  return pages.flat()
}

export async function previewRouting(
  routing: RoutingPolicy,
  model: string,
  request?: Record<string, unknown>,
  tokenId?: number
): Promise<RoutingPreview> {
  const previewRequest = request ? { ...request } : undefined
  if (previewRequest) {
    delete previewRequest.group
    delete previewRequest.mowan_routing
  }
  const response = await api.post<ApiResponse<RoutingPreview>>(
    '/api/mowan/smart-routing/preview',
    { routing, model, request: previewRequest },
    { params: { token_id: tokenId } }
  )
  const body = requireServerSuccess(response.data)
  if (!body.data) throw new Error(t('Failed to load'))
  return body.data
}

export async function getRoutingSubmission(
  id: string
): Promise<RoutingSubmission> {
  const response = await api.get<ApiResponse<RoutingSubmission>>(
    `/api/mowan/smart-routing/submissions/${encodeURIComponent(id)}`
  )
  const body = requireServerSuccess(response.data)
  if (!body.data) throw new Error(t('Failed to load'))
  return body.data
}

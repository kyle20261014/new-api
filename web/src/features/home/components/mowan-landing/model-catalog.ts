import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { getLobeIconNames } from '@/lib/lobe-icon'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { ModelCapsule } from './types'

const iconNames = new Set(getLobeIconNames().map((name) => name.split('.')[0]))

export interface HomepageModel {
  id: string
  icon?: string
  vendor_id?: number
  vendor_name?: string
  vendor_icon?: string
  /** Exact catalog record creation time in seconds; not a vendor release date. */
  catalog_created_time?: number
  supported_endpoint_types?: string[]
  /** Exact model metadata explicitly declares the Chat Completions path. */
  can_chat?: boolean
}

function catalogTime(model: HomepageModel): number {
  const time = model.catalog_created_time
  return typeof time === 'number' && Number.isFinite(time) && time > 0
    ? time
    : 0
}

/** Newest catalog records per vendor first, then round-robin their other models. */
export function selectHomepageModels(
  models: HomepageModel[],
  limit = 18
): ModelCapsule[] {
  const sorted = [...models].sort(
    (a, b) => catalogTime(b) - catalogTime(a) || a.id.localeCompare(b.id, 'en')
  )
  const seen = new Set<string>()
  const vendors = new Map<string, HomepageModel[]>()
  for (const model of sorted) {
    if (!model.id.trim() || seen.has(model.id)) continue
    seen.add(model.id)
    // Unknown vendors share one bucket rather than inventing a brand per model.
    const vendor = model.vendor_id ? `vendor:${model.vendor_id}` : 'unknown'
    const bucket = vendors.get(vendor) ?? []
    bucket.push(model)
    vendors.set(vendor, bucket)
  }
  const selected: ModelCapsule[] = []
  const buckets = [...vendors.values()]
  for (let rank = 0; selected.length < limit; rank++) {
    let added = false
    for (const bucket of buckets) {
      const model = bucket[rank]
      if (!model) continue
      selected.push({
        id: model.id,
        name: model.id,
        canChat: model.can_chat === true,
        icon: [model.icon, model.vendor_icon]
          .map((name) => name?.trim())
          .find((name) => name && iconNames.has(name.split('.')[0])),
        tag: model.vendor_name,
      })
      added = true
      if (selected.length === limit) break
    }
    if (!added) break
  }
  return selected
}

export function useHomepageModels() {
  return useQuery({
    queryKey: ['homepage-models'],
    queryFn: async ({ signal }): Promise<ModelCapsule[]> => {
      const response = await api.get<{
        success: boolean
        message?: string
        data: HomepageModel[]
      }>('/api/mowan/homepage/models', {
        signal,
        // TanStack deduplicates subscribers; HTTP dedup can reuse an aborted mount.
        disableDuplicate: true,
        skipAuthRefresh: true,
        skipErrorHandler: true,
      })
      return selectHomepageModels(requireServerSuccess(response.data).data)
    },
    staleTime: 5 * 60 * 1000,
    meta: { errorToast: false },
  })
}

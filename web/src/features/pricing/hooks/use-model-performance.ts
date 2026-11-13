import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { getPerfMetricsSummary } from '@/features/performance-metrics/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { ModelPerfBadgeData } from '../components/model-perf-badge'
import type { PricingModel } from '../types'

export async function loadModelPerformance(names: string[]) {
  const batches = []
  for (let offset = 0; offset < names.length; offset += 50) {
    const batch = names.slice(offset, offset + 50)
    batches.push(
      getPerfMetricsSummary(24, batch).then((response) => {
        const data = requireServerSuccess(response).data
        // The endpoint also returns nonrequested models with a different window.
        const metrics = data.models
          .filter((model) => batch.includes(model.model_name))
          .map((model) => ({
            ...model,
            recent_success_series:
              data.recent_success_series?.[model.model_name] ??
              model.recent_success_series,
          }))
        for (const name of batch) {
          if (
            !metrics.some((model) => model.model_name === name) &&
            data.recent_success_series?.[name]?.length
          ) {
            metrics.push({
              model_name: name,
              avg_latency_ms: Number.NaN,
              success_rate: Number.NaN,
              avg_tps: Number.NaN,
              recent_success_series: data.recent_success_series[name],
            })
          }
        }
        return metrics
      })
    )
  }
  return (await Promise.all(batches)).flat()
}

export function useModelPerformance(models: PricingModel[], enabled = true) {
  const names = useMemo(
    () => [...new Set(models.map((model) => model.model_name))].sort(),
    [models]
  )
  const query = useQuery({
    queryKey: ['pricing-model-performance', names],
    queryFn: () => loadModelPerformance(names),
    enabled: enabled && names.length > 0,
    staleTime: 60 * 1000,
    retry: false,
  })
  return useMemo(
    () =>
      new Map<string, ModelPerfBadgeData>(
        (query.data ?? []).map((model) => [model.model_name, model])
      ),
    [query.data]
  )
}

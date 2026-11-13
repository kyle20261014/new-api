import { act, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { useFilters } from '../hooks/use-filters'
import { loadModelPerformance } from '../hooks/use-model-performance'
import { filterByCategory, sortModels } from '../lib/filters'
import type { PricingModel } from '../types'

vi.mock('@tanstack/react-router', () => ({ useSearch: () => ({}) }))
afterEach(() => vi.restoreAllMocks())
const model = (name: string, tags?: string): PricingModel => ({
  id: 1,
  model_name: name,
  tags,
  model_ratio: 1,
  completion_ratio: 1,
  quota_type: 0,
  enable_groups: ['default'],
})

describe('model categories and performance sorting', () => {
  it('matches explicit multi-category tags and leaves unknown models only in all', () => {
    const models = [
      model('chat', 'text,vision'),
      model('multi', 'image;video'),
      model('speech', 'AUDIO'),
      model('unknown'),
    ]
    expect(filterByCategory(models, 'all')).toHaveLength(4)
    expect(filterByCategory(models, 'text').map((m) => m.model_name)).toEqual([
      'chat',
    ])
    expect(filterByCategory(models, 'image').map((m) => m.model_name)).toEqual([
      'multi',
    ])
    expect(filterByCategory(models, 'video').map((m) => m.model_name)).toEqual([
      'multi',
    ])
    expect(filterByCategory(models, 'audio').map((m) => m.model_name)).toEqual([
      'speech',
    ])
    expect(filterByCategory([], 'text')).toEqual([])
  })
  it('combines category and other filters, keeps category counts independent, and clears category', () => {
    const models = [
      model('chat', 'text'),
      model('draw', 'image'),
      { ...model('premium', 'text'), enable_groups: ['premium'] },
    ]
    const { result } = renderHook(() => useFilters(models))
    act(() => {
      result.current.setCategoryFilter('image')
      result.current.setGroupFilter('default')
    })
    expect(result.current.filteredModels.map((m) => m.model_name)).toEqual([
      'draw',
    ])
    expect(result.current.categoryModels).toHaveLength(2)
    expect(result.current.activeFilterCount).toBe(2)
    act(() => result.current.clearFilters())
    expect(result.current.categoryFilter).toBe('all')
    expect(result.current.filteredModels).toHaveLength(3)
  })
  it('sorts globally before slicing pages, preserves equal order and keeps missing metrics last', () => {
    const models = Array.from({ length: 22 }, (_, i) => model(`m${i}`))
    const metrics = new Map(
      models
        .slice(0, 21)
        .map((m, i) => [
          m.model_name,
          { avg_latency_ms: 20 - i, success_rate: i, avg_tps: 0 },
        ])
    )
    metrics.set('m1', { avg_latency_ms: 0, success_rate: 0, avg_tps: 0 })
    const sorted = sortModels(models, 'response-speed', metrics)
    expect(sorted.slice(0, 2).map((m) => m.model_name)).toEqual(['m1', 'm20'])
    expect(sorted.at(-1)?.model_name).toBe('m21')
    const success = sortModels(models, 'success-rate', metrics)
    expect(success[0].model_name).toBe('m20')
    expect(success.slice(-3).map((m) => m.model_name)).toEqual([
      'm0',
      'm1',
      'm21',
    ])
    expect(sortModels(models, 'success-rate')).toEqual(models)
    expect(models[0].model_name).toBe('m0')
  })
  it('fetches over 50 models in bounded batches and ignores unrelated summary windows', async () => {
    const names = Array.from({ length: 51 }, (_, i) => `m${i}`)
    const request = vi
      .spyOn(api, 'get')
      .mockImplementation(async (_url, config) => {
        const requested =
          (config?.params as { recent_model: string[] } | undefined)
            ?.recent_model ?? []
        return {
          data: {
            success: true,
            data: {
              models: [
                ...requested.map((name) => ({
                  model_name: name,
                  avg_latency_ms: 1,
                  avg_tps: 1,
                  success_rate: 100,
                })),
                { model_name: 'unrelated', success_rate: 0 },
              ],
            },
          },
        }
      })
    const result = await loadModelPerformance(names)
    expect(request).toHaveBeenCalledTimes(2)
    expect(result.map((m) => m.model_name)).toEqual(names)
    expect(
      request.mock.calls.map(
        (call) =>
          (call[1]?.params as { recent_model: string[] } | undefined)
            ?.recent_model.length
      )
    ).toEqual([50, 1])
  })
  it('rejects incomplete performance batches instead of sorting on partial data', async () => {
    vi.spyOn(api, 'get')
      .mockResolvedValueOnce({ data: { success: true, data: { models: [] } } })
      .mockRejectedValueOnce(new Error('offline'))
    await expect(
      loadModelPerformance(Array.from({ length: 51 }, (_, i) => `m${i}`))
    ).rejects.toThrow('offline')
  })
})

import { expect, test } from 'vitest'

import { api } from '@/lib/api'

import { getRoutingCandidates } from '../api'

test('a new candidate observer does not inherit a canceled predecessor request', async () => {
  const adapter = api.defaults.adapter
  api.defaults.adapter = async (config) => ({
    config,
    status: 200,
    statusText: 'OK',
    headers: {},
    data: {
      success: true,
      data: { items: [], total: 0, page: 1, page_size: 100 },
    },
  })
  try {
    const controller = new AbortController()
    const first = getRoutingCandidates({}, controller.signal)
    controller.abort()
    const second = getRoutingCandidates({}, new AbortController().signal)
    const results = await Promise.allSettled([first, second])
    expect(results[0].status).toBe('rejected')
    expect(results[1]).toMatchObject({
      status: 'fulfilled',
      value: { items: [], total: 0 },
    })
  } finally {
    api.defaults.adapter = adapter
  }
})

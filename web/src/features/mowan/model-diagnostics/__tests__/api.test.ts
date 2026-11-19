import type { AxiosAdapter } from 'axios'
import { expect, test, vi } from 'vitest'

import { api } from '@/lib/http-client'

import { getDiagnosticCatalog, getDiagnosticReports } from '../api'

test.each(['catalog', 'reports'] as const)(
  '%s readers cancel independently, including a new subscription after cancellation',
  async (kind) => {
    const previous = api.defaults.adapter
    const pending: (() => void)[] = []
    const data =
      kind === 'catalog' ? { groups: ['standard'], chat_supported: true } : []
    const adapter = vi.fn<AxiosAdapter>(
      (config) =>
        new Promise((resolve) => {
          pending.push(() =>
            resolve({
              data: { success: true, data },
              status: 200,
              statusText: 'OK',
              headers: {},
              config,
            })
          )
        })
    )
    api.defaults.adapter = adapter
    try {
      const read = (signal: AbortSignal) =>
        kind === 'catalog'
          ? getDiagnosticCatalog(`independent-${kind}`, signal)
          : getDiagnosticReports(`independent-${kind}`, 'standard', signal)
      const old = new AbortController()
      const sibling = new AbortController()
      const fresh = new AbortController()
      const first = read(old.signal).catch((error: unknown) => error)
      const second = read(sibling.signal).catch((error: unknown) => error)
      await vi.waitFor(() => expect(adapter).toHaveBeenCalled())
      old.abort()
      const third = read(fresh.signal).catch((error: unknown) => error)
      // Axios request interceptors dispatch asynchronously; wait before releasing transport.
      await vi.waitFor(() => expect(adapter).toHaveBeenCalledTimes(3))
      for (const resolve of pending) resolve()
      const results = await Promise.all([first, second, third])
      expect(results[0]).toMatchObject({ code: 'ERR_CANCELED' })
      expect(results[1]).toEqual(data)
      expect(results[2]).toEqual(data)
      expect(adapter).toHaveBeenCalledTimes(3)
      expect(sibling.signal.aborted).toBe(false)
      expect(fresh.signal.aborted).toBe(false)
    } finally {
      for (const resolve of pending) resolve()
      api.defaults.adapter = previous
    }
  }
)

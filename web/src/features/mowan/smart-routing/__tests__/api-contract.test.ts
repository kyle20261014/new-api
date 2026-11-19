import { expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { getSelectedRoutingCandidates, previewRouting } from '../api'

test('preview forwards a read-only policy and strips legacy group fields from the actual request quote', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [],
        total: 0,
        page: 1,
        page_size: 100,
        reason: 'No candidates',
        algorithm_version: '1',
      },
    },
  })
  const request = {
    model: 'chat',
    group: 'old-group',
    messages: [{ role: 'user', content: 'quote this input' }],
    max_tokens: 32,
    mowan_routing: { mode: 'fixed' },
  }
  const routing = {
    schema_version: 1,
    mode: 'smart',
    strategy: 'balanced',
  } as const
  await previewRouting(routing, 'chat', request, 8)
  expect(post).toHaveBeenCalledWith(
    '/api/mowan/smart-routing/preview',
    {
      routing,
      model: 'chat',
      request: { model: 'chat', messages: request.messages, max_tokens: 32 },
    },
    { params: { token_id: 8 } }
  )
  expect(request.group).toBe('old-group')
})

test('selected candidates load subsequent pages with the same key restrictions', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          items: [{ model: 'chat', group: 'first' }],
          total: 101,
          page: 1,
          page_size: 100,
        },
      },
    })
    .mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          items: [{ model: 'chat', group: 'last' }],
          total: 101,
          page: 2,
          page_size: 100,
        },
      },
    })
  const values = await getSelectedRoutingCandidates(['chat'], undefined, 8)
  expect(values.map((value) => value.group)).toEqual(['first', 'last'])
  expect(get).toHaveBeenLastCalledWith('/api/mowan/smart-routing/candidates', {
    params: { model: 'chat', p: 2, page_size: 100, token_id: 8 },
    signal: undefined,
    disableDuplicate: true,
  })
})

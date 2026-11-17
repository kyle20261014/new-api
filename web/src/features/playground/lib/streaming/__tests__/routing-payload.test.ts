import { expect, test } from 'vitest'

import { DEFAULT_CONFIG, DEFAULT_PARAMETER_ENABLED } from '../../../constants'
import { buildChatCompletionPayload } from '../payload-builder'
import { parseStreamErrorDetails } from '../stream-utils'

test('temporary routing reaches the real chat payload without legacy group or persisted configuration changes', () => {
  const config = { ...DEFAULT_CONFIG, model: 'chat', group: 'saved-group' }
  const routing = {
    schema_version: 1,
    mode: 'fixed',
    fixed_group: 'priority',
  } as const
  const payload = buildChatCompletionPayload(
    [],
    config,
    DEFAULT_PARAMETER_ENABLED,
    routing
  )
  expect(payload.mowan_routing).toEqual(routing)
  expect(payload).not.toHaveProperty('group')
  expect(config.group).toBe('saved-group')
  expect(
    buildChatCompletionPayload([], config, DEFAULT_PARAMETER_ENABLED).group
  ).toBe('saved-group')
})

test('a stream error keeps its unknown submission reference for status tracking', () => {
  const result = parseStreamErrorDetails(
    JSON.stringify({
      error: {
        message: 'pending',
        submission_id: 'submission-1',
        submission_status: 'unknown',
      },
    })
  )
  expect(result.submission).toEqual({
    submission_id: 'submission-1',
    submission_status: 'unknown',
  })
})

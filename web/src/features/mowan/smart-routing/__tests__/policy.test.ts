import type { TFunction } from 'i18next'
import { expect, test } from 'vitest'

import {
  getApiKeyFormSchema,
  transformApiKeyToFormDefaults,
  transformFormDataToPayload,
} from '@/features/keys/lib/api-key-form'
import { apiKeySchema } from '@/features/keys/types'

import { normalizeRouting, routingIsValid } from '../policy'
import type { CustomRouting } from '../types'

const baseKey = {
  id: 1,
  name: 'key',
  key: 'secret',
  status: 1,
  remain_quota: 100,
  used_quota: 0,
  unlimited_quota: false,
  expired_time: -1,
  created_time: 0,
  accessed_time: 0,
  model_limits_enabled: false,
  model_limits: 'stored-but-disabled',
  allow_ips: '127.0.0.1',
  allow_user_quota_query: false,
  routing: null,
  routing_version: 3,
  allow_routing_self_write: false,
}

test.each([
  [{}, []],
  [{ try_soft_offline_groups: [] }, []],
  [{ try_soft_offline_groups: ['priority'] }, ['priority']],
])(
  'API key response normalizes independent group preferences %j to %s',
  (flag, expected) => {
    const parsed = apiKeySchema.parse({
      ...baseKey,
      routing: {
        schema_version: 1,
        mode: 'smart',
        strategy: 'custom',
        models: {
          'gpt-4o-mini': { groups: ['priority'], strategy: 'manual', ...flag },
        },
      },
    })
    expect(parsed.routing).toMatchObject({
      models: { 'gpt-4o-mini': { try_soft_offline_groups: expected } },
    })
    expect(
      transformFormDataToPayload(transformApiKeyToFormDefaults(parsed)).routing
    ).toMatchObject({
      models: { 'gpt-4o-mini': { try_soft_offline_groups: expected } },
    })
  }
)

test.each([[null], [0], [1], ['false'], ['true'], [[]], [{}]])(
  'API key response rejects invalid soft-offline flag %j',
  (try_soft_offline) => {
    expect(
      apiKeySchema.safeParse({
        ...baseKey,
        routing: {
          schema_version: 1,
          mode: 'smart',
          strategy: 'custom',
          models: {
            'gpt-4o-mini': {
              groups: ['priority'],
              strategy: 'manual',
              try_soft_offline,
            },
          },
        },
      }).success
    ).toBe(false)
  }
)

test('custom routing rejects unknown policy and model fields', () => {
  const routing = {
    schema_version: 1,
    mode: 'smart',
    strategy: 'custom',
    models: {
      'gpt-4o-mini': {
        groups: ['priority'],
        strategy: 'manual',
        try_soft_offline_groups: [],
      },
    },
  }
  expect(
    apiKeySchema.safeParse({
      ...baseKey,
      routing: { ...routing, unknown: true },
    }).success
  ).toBe(false)
  expect(
    apiKeySchema.safeParse({
      ...baseKey,
      routing: {
        ...routing,
        models: {
          'gpt-4o-mini': { ...routing.models['gpt-4o-mini'], unknown: true },
        },
      },
    }).success
  ).toBe(false)
})

test.each([undefined, null, []])(
  'legacy rename omits routing and preserves the original Auto snapshot %j',
  (auto_groups) => {
    const original = apiKeySchema.parse({ ...baseKey, group: '', auto_groups })
    const defaults = transformApiKeyToFormDefaults(original)
    const payload = transformFormDataToPayload(
      { ...defaults, name: 'renamed' },
      original
    )
    expect(payload).not.toHaveProperty('routing')
    expect(payload).not.toHaveProperty('expected_routing_version')
    expect(payload).not.toHaveProperty('group')
    expect(payload).not.toHaveProperty('auto_groups')
    expect(payload).not.toHaveProperty('model_limits_enabled')
    expect(payload).not.toHaveProperty('model_limits')
    expect(payload).not.toHaveProperty('allow_ips')
    expect(payload).not.toHaveProperty('remain_quota')
  }
)

test('custom policies round trip manual order and discard fields inactive in the chosen mode', () => {
  const policy: CustomRouting = {
    schema_version: 1,
    mode: 'smart',
    strategy: 'custom',
    models: {
      z: {
        groups: ['priority', 'standard'],
        strategy: 'manual',
        try_soft_offline_groups: ['priority'],
      },
      a: {
        groups: ['priority', 'standard'],
        strategy: 'manual',
        try_soft_offline_groups: [],
      },
    },
  }
  const original = apiKeySchema.parse({
    ...baseKey,
    group: 'auto',
    routing: policy,
  })
  const draft = transformApiKeyToFormDefaults(original)
  expect(transformFormDataToPayload(draft, original)).not.toHaveProperty(
    'routing'
  )
  draft.routing = { schema_version: 1, mode: 'fixed', fixed_group: 'standard' }
  expect(transformFormDataToPayload(draft, original)).toMatchObject({
    expected_routing_version: 3,
    routing: { schema_version: 1, mode: 'fixed', fixed_group: 'standard' },
  })
  const normalized = normalizeRouting(policy)
  expect(normalized).toMatchObject({
    models: {
      z: { groups: ['priority', 'standard'] },
      a: { groups: ['priority', 'standard'] },
    },
  })
  expect(routingIsValid({ ...policy, models: {} })).toBe(false)
  expect(
    routingIsValid({
      ...policy,
      models: { z: { ...policy.models.z, groups: ['auto'] } },
    })
  ).toBe(false)
})

test('unchanged invalid legacy Auto remains editable while explicit over-limit edits fail validation', () => {
  const original = apiKeySchema.parse({
    ...baseKey,
    group: 'auto',
    auto_groups: ['revoked', 'standard', 'priority'],
  })
  const defaults = transformApiKeyToFormDefaults(original, ['standard'], 1)
  const t = ((key: string) => key) as TFunction
  expect(getApiKeyFormSchema(t, 1, original).safeParse(defaults).success).toBe(
    true
  )
  expect(
    getApiKeyFormSchema(t, 1, original).safeParse({
      ...defaults,
      auto_groups: ['standard', 'priority'],
    }).success
  ).toBe(false)
})

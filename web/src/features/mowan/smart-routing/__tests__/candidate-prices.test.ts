import { createInstance } from 'i18next'
import { expect, test } from 'vitest'

import { candidatePriceRows } from '../candidate-prices'
import type { RoutingCandidate } from '../types'

const quote: RoutingCandidate['quote'] = {
  comparison: 'available',
  currency: 'USD',
  prices: [],
}
const i18n = createInstance()
await i18n.init({ lng: 'en', resources: { en: { translation: {} } } })
const rows = (value: RoutingCandidate['quote']) =>
  candidatePriceRows(value, i18n.t, 'en', 'en-US')
test('numeric catalog prices already include the group ratio, including zero', () => {
  expect(
    rows({
      ...quote,
      prices: [
        { label: 'input', amount: 0.5, unit: 'USD/1M tokens' },
        { label: 'output', amount: 0, unit: 'USD/1M tokens' },
      ],
    }).map((row) => row.value)
  ).toEqual(['$0.50 / 1M token', '$0.00 / 1M token'])
})
test('token expression uses the actual group ratio once', () => {
  const result = rows({
    ...quote,
    expression: {
      rule: 'tier("text", p * 2 + c * 8)',
      group_ratio: 0.25,
      task: false,
    },
  })
  expect(result.map((row) => row.value)).toEqual([
    '$0.5 / 1M token',
    '$2 / 1M token',
  ])
})
test('task pricing uses the supplied usage profile, units and actual zero', () => {
  const result = rows({
    ...quote,
    expression: {
      rule: 'tier("seconds", u("seconds") * 3)',
      group_ratio: 0,
      task: true,
      usage_schema: {
        seconds: { type: 'number', unit: 'second', description: 'Duration' },
      },
    },
  })
  expect(result).toEqual([{ label: 'Duration', value: '$0 / s' }])
})
test('unexpandable expressions do not invent a partial or zero price', () => {
  expect(
    rows({
      ...quote,
      expression: {
        rule: 'tier("dynamic", p * param("custom_price"))',
        group_ratio: 1,
        task: false,
      },
    })
  ).toEqual([])
  expect(
    rows({
      ...quote,
      prices: [
        {
          label: 'billing_expression',
          rule: 'unrecognized rule',
          unit: 'USD (expression)',
        },
      ],
    })
  ).toEqual([])
})

test.each(['header("x-priority") == "high"', 'hour("UTC") >= 0'])(
  'task prices retain base price and request multiplier condition %s',
  (condition) => {
    const result = rows({
      ...quote,
      expression: {
        rule: `(tier("seconds", u("seconds") * 3)) * (${condition} ? 2 : 1)`,
        task: true,
        group_ratio: 1,
        usage_schema: {
          seconds: { type: 'number', unit: 'second', description: 'Duration' },
        },
      },
    })
    expect(result[0]).toEqual({
      label: 'Base price · Duration',
      value: '$3 / s',
    })
    expect(result[1].label).toMatch(/UTC|x-priority/)
    expect(result[1].value).toBe('×2 · Otherwise ×1')
    expect(
      candidatePriceRows(
        {
          ...quote,
          expression: {
            rule: `(tier("seconds", u("seconds") * 3)) * (${condition} ? 2 : 1)`,
            task: true,
            group_ratio: 1,
            usage_schema: { seconds: { type: 'number', unit: 'second' } },
          },
        },
        (key) => key,
        'en',
        'en-US'
      ).at(-1)?.label
    ).toMatch(/UTC|x-priority/)
  }
)

test.each(['tier("text", p * 2 + c * 8)', 'tier("request", fixed(0))'])(
  'token and fixed zero canonical prices retain request conditions: %s',
  (rule) => {
    const result = rows({
      ...quote,
      expression: {
        rule: `(${rule}) * (hour("UTC") >= 0 ? 2 : 1)`,
        task: false,
        group_ratio: 0.5,
      },
    })
    expect(result.length).toBeGreaterThan(1)
    expect(result.at(-1)?.value).toBe('×2 · Otherwise ×1')
    expect(result.at(-1)?.label).toContain('UTC')
  }
)

test('provider-incompatible schema cannot become a partial price, while unknown request usage retains valid unit prices', () => {
  const value = {
    ...quote,
    comparison: 'unavailable',
    reason: 'provider_usage_incompatible',
    expression: {
      rule: 'tier("seconds", u("seconds") * 3 + u("images") * 10)',
      task: true,
      group_ratio: 1,
      usage_schema: {
        seconds: {
          type: 'number',
          unit: 'second',
          description: 'Duration',
        },
      },
    },
  } satisfies RoutingCandidate['quote']
  expect(rows(value)).toEqual([])
  expect(
    rows({
      ...value,
      reason: 'request_usage_unknown',
      expression: {
        ...value.expression,
        rule: 'tier("seconds", u("seconds") * 3)',
      },
    })
  ).toEqual([{ label: 'Duration', value: '$3 / s' }])
})

test.each([0, 1.0001])(
  'request multipliers retain zero and full decimal precision: %s',
  (multiplier) => {
    const result = rows({
      ...quote,
      expression: {
        rule: `(tier("text", p * 2)) * (hour("UTC") >= 0 ? ${multiplier} : 1)`,
        task: false,
        group_ratio: 1,
      },
    })
    expect(result.at(-1)?.value).toBe(`×${multiplier} · Otherwise ×1`)
  }
)

test.each([
  [
    'task',
    'tier("base", u("seconds") * 3 * (hour("UTC") >= 0 ? 2 : 1)) * (day("UTC") >= 1 ? 3 : 1)',
  ],
  [
    'task',
    '(tier("base", u("seconds") * 3)) * ((hour("UTC") >= 0 ? 2 : 1) > 1 ? 3 : 1)',
  ],
  [
    'token',
    'tier("base", p * 3 * (hour("UTC") >= 0 ? 2 : 1)) * (day("UTC") >= 1 ? 3 : 1)',
  ],
  ['token', '(tier("base", p * 3)) * ((hour("UTC") >= 0 ? 2 : 1) > 1 ? 3 : 1)'],
])(
  'nested %s pricing rules do not turn instrumentation into extra charge factors: %s',
  (kind, rule) => {
    // These rules may be kept whole; a partial numeric quote is not safe.
    expect(
      rows({
        ...quote,
        expression: {
          rule,
          task: kind === 'task',
          group_ratio: 1,
          usage_schema: {
            seconds: {
              type: 'number',
              unit: 'second',
              description: 'Duration',
            },
          },
        },
      })
    ).toEqual([])
  }
)

test.each([true, false])(
  'two independent top-level request multipliers each appear exactly once (task=%s)',
  (task) => {
    const base = task ? 'u("seconds") * 3' : 'p * 3'
    const result = rows({
      ...quote,
      expression: {
        rule: `tier("base", ${base}) * (hour("UTC") >= 0 ? 2 : 1) * (day("UTC") >= 1 ? 3 : 1)`,
        task,
        group_ratio: 1,
        usage_schema: {
          seconds: { type: 'number', unit: 'second', description: 'Duration' },
        },
      },
    })
    expect(result.map((row) => row.value)).toEqual([
      task ? '$3 / s' : '$3 / 1M token',
      '×2 · Otherwise ×1',
      '×3 · Otherwise ×1',
    ])
  }
)

test('a nonstandard fallback multiplier retains both complete conditional prices', () => {
  const result = rows({
    ...quote,
    expression: {
      rule: 'tier("base", u("seconds") * 3) * (hour("UTC") >= 0 ? 2 : 1.5)',
      task: true,
      group_ratio: 1,
      usage_schema: {
        seconds: { type: 'number', unit: 'second', description: 'Duration' },
      },
    },
  })
  expect(result.map((row) => row.value)).toEqual(['$6 / s', '$4.5 / s'])
  expect(result.every((row) => row.label.includes('UTC'))).toBe(true)
  expect(result.some((row) => row.value.includes('Otherwise ×1'))).toBe(false)
})

test.each([true, false])(
  'unsupported max costs remain a complete special rule (task=%s)',
  (task) => {
    expect(
      rows({
        ...quote,
        expression: {
          rule: `tier("base", max(${task ? 'u("seconds")' : 'p'} * 3, 10)) * (hour("UTC") >= 0 ? 2 : 1)`,
          task,
          group_ratio: 1,
          usage_schema: {
            seconds: {
              type: 'number',
              unit: 'second',
              description: 'Duration',
            },
          },
        },
      })
    ).toEqual([])
  }
)

test.each(
  [true, false].flatMap((task) =>
    ['+2', '-0'].flatMap((multiplier) =>
      [false, true].map((mixed) => ({ task, multiplier, mixed }))
    )
  )
)(
  'every removed signed fee factor must be represented: $task / $multiplier / mixed=$mixed',
  ({ task, multiplier, mixed }) => {
    const rule = `tier("base", ${task ? 'u("seconds")' : 'p'} * 3) * (hour("UTC") >= 0 ? ${multiplier} : 1)${mixed ? ' * (day("UTC") >= 1 ? 3 : 1)' : ''}`
    expect(
      rows({
        ...quote,
        expression: {
          rule,
          task,
          group_ratio: 1,
          usage_schema: {
            seconds: {
              type: 'number',
              unit: 'second',
              description: 'Duration',
            },
          },
        },
      })
    ).toEqual([])
  }
)

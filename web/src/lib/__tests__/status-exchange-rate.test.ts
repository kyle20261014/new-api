import { expect, it } from 'vitest'

import { mapStatusDataToConfig } from '../status-query'

it.each([
  undefined,
  null,
  0,
  -1,
  '',
  '  ',
  'invalid',
  Number.NaN,
  Infinity,
  'Infinity',
])('marks raw rate %s unavailable', (rate) => {
  expect(
    mapStatusDataToConfig({ usd_exchange_rate: rate }).currency
  ).toMatchObject({ usdExchangeRateValid: false })
})
it.each([7.25, '8', Number.MIN_VALUE])(
  'marks finite positive raw rate %s valid',
  (rate) => {
    expect(
      mapStatusDataToConfig({ usd_exchange_rate: rate }).currency
    ).toMatchObject({
      usdExchangeRateValid: true,
      usdExchangeRate: Number(rate),
    })
  }
)
it('keeps the existing missing-rate fallback', () => {
  expect(mapStatusDataToConfig({}).currency?.usdExchangeRate).toBe(1)
})

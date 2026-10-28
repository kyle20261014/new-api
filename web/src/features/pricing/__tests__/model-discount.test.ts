/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { describe, expect, it } from 'vitest'

import {
  getConfiguredGroupRatio,
  getDisplayGroupRatio,
  getModelDiscount,
  getModelLowestEffectiveRatio,
} from '../lib/model-helpers'
import type { PricingModel } from '../types'

function createModel(overrides: Partial<PricingModel> = {}): PricingModel {
  return {
    id: 1,
    model_name: 'test-model',
    quota_type: 0,
    model_ratio: 1,
    completion_ratio: 1,
    enable_groups: ['default'],
    group_ratio: { default: 1 },
    ...overrides,
  }
}

describe('model discount logic (A1 & A2)', () => {
  it('A1: returns lowest effective ratio and 30% discount for 0.3, 0.5, 1', () => {
    const model = createModel({
      enable_groups: ['group-a', 'group-b', 'group-c'],
      group_ratio: {
        'group-a': 0.3,
        'group-b': 0.5,
        'group-c': 1,
      },
    })
    const usable = { 'group-a': { desc: '', ratio: 0.3 }, 'group-b': { desc: '', ratio: 0.5 }, 'group-c': { desc: '', ratio: 1 } }

    const lowest = getModelLowestEffectiveRatio(model, usable)
    expect(lowest).toBe(0.3)

    const discount = getModelDiscount(model, usable)
    expect(discount).toEqual({
      isFree: false,
      ratio: 0.3,
      discount: 3,
      isLowest: true,
    })
  })

  it('A1: returns specific group discount when selectedGroup is provided and recovers lowest when cleared', () => {
    const model = createModel({
      enable_groups: ['group-a', 'group-b', 'group-c'],
      group_ratio: {
        'group-a': 0.3,
        'group-b': 0.5,
        'group-c': 1,
      },
    })
    const usable = {
      'group-a': { desc: '', ratio: 0.3 },
      'group-b': { desc: '', ratio: 0.5 },
      'group-c': { desc: '', ratio: 1 },
    }

    // 1. All groups (undefined, empty, or 'all' sentinel): returns lowest (0.3 -> 3折, isLowest: true)
    const allGroupsDiscount = getModelDiscount(model, '', usable)
    expect(allGroupsDiscount).toEqual({
      isFree: false,
      ratio: 0.3,
      discount: 3,
      isLowest: true,
    })
    const sentinelAllDiscount = getModelDiscount(model, 'all', usable)
    expect(sentinelAllDiscount).toEqual({
      isFree: false,
      ratio: 0.3,
      discount: 3,
      isLowest: true,
    })

    // 2. Select group-b (0.5): returns exact group discount (0.5 -> 5折, isLowest: false)
    const groupBDiscount = getModelDiscount(model, 'group-b', usable)
    expect(groupBDiscount).toEqual({
      isFree: false,
      ratio: 0.5,
      discount: 5,
      isLowest: false,
    })

    // 3. Switch back to all groups: recovers lowest discount (3折, isLowest: true)
    const switchBackDiscount = getModelDiscount(model, undefined, usable)
    expect(switchBackDiscount).toEqual({
      isFree: false,
      ratio: 0.3,
      discount: 3,
      isLowest: true,
    })

    // 4. Select group-c (ratio 1): ratio >= 1 hides discount (returns null)
    expect(getModelDiscount(model, 'group-c', usable)).toBeNull()

    // 5. Select a group not enabled for model: returns null (does not fall back to other groups)
    expect(getModelDiscount(model, 'unsupported-group', usable)).toBeNull()

    // 6. Select a group not in visitor usable groups: returns null
    const visitorUsableWithoutB = {
      'group-a': { desc: '', ratio: 0.3 },
      'group-c': { desc: '', ratio: 1 },
    }
    expect(
      getModelDiscount(model, 'group-b', visitorUsableWithoutB)
    ).toBeNull()
  })

  it('A1: ignores unpermitted groups when calculating lowest discount', () => {
    const model = createModel({
      enable_groups: ['public-group', 'secret-vip'],
      group_ratio: {
        'public-group': 0.8,
        'secret-vip': 0.1, // very low ratio, but user does not have access
      },
    })
    // User only has access to public-group
    const userUsable = { 'public-group': { desc: '', ratio: 0.8 } }

    const discount = getModelDiscount(model, userUsable)
    expect(discount).toEqual({
      isFree: false,
      ratio: 0.8,
      discount: 8,
      isLowest: true,
    })
  })

  it('A1: handles "all" in enable_groups by checking all visitor usable groups', () => {
    const model = createModel({
      enable_groups: ['all'],
      group_ratio: {
        'group-1': 0.6,
        'group-2': 0.4,
      },
    })
    const userUsable = {
      'group-1': { desc: '', ratio: 0.6 },
      'group-2': { desc: '', ratio: 0.4 },
    }

    const discount = getModelDiscount(model, userUsable)
    expect(discount?.ratio).toBe(0.4)
    expect(discount?.discount).toBe(4)
    expect(discount?.isLowest).toBe(true)
  })

  it('A2: returns isFree: true when lowest ratio is 0', () => {
    const model = createModel({
      enable_groups: ['free-group', 'standard'],
      group_ratio: {
        'free-group': 0,
        standard: 1,
      },
    })

    const discount = getModelDiscount(model)
    expect(discount).toEqual({
      isFree: true,
      ratio: 0,
      discount: 0,
      isLowest: true,
    })
  })

  it('A2: hides discount (returns null) when all effective ratios >= 1', () => {
    const model = createModel({
      enable_groups: ['default', 'expensive'],
      group_ratio: {
        default: 1,
        expensive: 2.5,
      },
    })

    expect(getModelLowestEffectiveRatio(model)).toBeNull()
    expect(getModelDiscount(model)).toBeNull()
  })

  it('A2: ignores negative ratios and does not produce free or invalid discount', () => {
    const model = createModel({
      enable_groups: ['invalid-group', 'standard'],
      group_ratio: {
        'invalid-group': -1,
        standard: 1,
      },
    })

    expect(getModelLowestEffectiveRatio(model)).toBeNull()
    expect(getModelDiscount(model)).toBeNull()
  })

  it('A2: does not treat missing ratios as free (defaults to 1)', () => {
    const model = createModel({
      enable_groups: ['unconfigured-group'],
      group_ratio: {},
    })

    // unconfigured-group defaults to 1, which is >= 1, so no discount
    expect(getModelLowestEffectiveRatio(model)).toBeNull()
    expect(getModelDiscount(model)).toBeNull()
  })

  it('A2: preserves high precision for boundary ratios without rounding to 0 or 10', () => {
    const modelLow = createModel({
      enable_groups: ['micro'],
      group_ratio: { micro: 0.0001 },
    })
    const discountLow = getModelDiscount(modelLow, { micro: { desc: '', ratio: 0.0001 } })
    expect(discountLow).toEqual({
      isFree: false,
      ratio: 0.0001,
      discount: 0.001,
      isLowest: true,
    })

    const modelNearOneExtreme = createModel({
      enable_groups: ['extreme-near-one'],
      group_ratio: { 'extreme-near-one': 0.99999999999999 },
    })
    const discountNearOneExtreme = getModelDiscount(modelNearOneExtreme, {
      'extreme-near-one': { desc: '', ratio: 0.99999999999999 },
    })
    expect(discountNearOneExtreme).not.toBeNull()
    expect(discountNearOneExtreme?.isFree).toBe(false)
    expect(discountNearOneExtreme?.ratio).toBe(0.99999999999999)
    // ratio < 1 must strictly NEVER round up to 10 (which would falsely indicate 10折 / no discount)
    expect(discountNearOneExtreme?.discount).toBeLessThan(10)
    expect(discountNearOneExtreme?.discount).toBe(0.99999999999999 * 10)
  })

  it('preserves getDisplayGroupRatio non-negative semantics and default 1', () => {
    const model = createModel({
      enable_groups: ['neg', 'pos', 'missing'],
      group_ratio: {
        neg: -0.5,
        pos: 1.5,
      },
    })

    // 'missing' defaults to 1, 'neg' is ignored (< 0), 'pos' is 1.5 -> min is 1
    expect(getDisplayGroupRatio(model)).toBe(1)

    // selectedGroup with negative ratio falls back to 1
    expect(getConfiguredGroupRatio({ neg: -0.5 }, 'neg')).toBe(1)
    expect(getConfiguredGroupRatio({ valid: 0 }, 'valid')).toBe(0)
    expect(getConfiguredGroupRatio({ valid: 0.5 }, 'valid')).toBe(0.5)
  })
})

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
import {
  ENDPOINT_TYPES,
  EXCLUDED_GROUPS,
  FILTER_ALL,
  QUOTA_TYPE_VALUES,
} from '../constants'
import type { PricingModel } from '../types'

// ----------------------------------------------------------------------------
// Model Helper Utilities
// ----------------------------------------------------------------------------

const CHAT_ENDPOINTS = new Set<string>([
  ENDPOINT_TYPES.OPENAI,
  ENDPOINT_TYPES.OPENAI_RESPONSE,
  ENDPOINT_TYPES.ANTHROPIC,
  ENDPOINT_TYPES.GEMINI,
])

const NON_CHAT_ENDPOINTS = new Set<string>([
  ENDPOINT_TYPES.IMAGE_GENERATION,
  ENDPOINT_TYPES.OPENAI_VIDEO,
  ENDPOINT_TYPES.EMBEDDINGS,
  ENDPOINT_TYPES.JINA_RERANK,
])

const NON_CHAT_MODEL_PATTERNS = [
  /^dall-e/i,
  /^gpt-image/i,
  /^flux/i,
  /^midjourney/i,
  /^stable-diffusion/i,
  /^text-embedding/i,
  /^tts-/i,
  /^whisper-/i,
  /(?:^|[/:])(?:doubao-)?seed(?:ance|ream)(?:-|$)/i,
  /(?:^|[/:])(?:sora|veo|kling|wan|hunyuan-video|cogvideo|luma|runway)(?:[-.]|$)/i,
  /(?:^|[/:])(?:imagen|jimeng|qwen-image)(?:-|$)/i,
  /(?:embedding|rerank)/i,
]

/**
 * Check if a model is supported in Playground for text/chat conversations.
 */
export function isPlaygroundChatModel(model: PricingModel): boolean {
  if (
    model.capabilities?.includes('embeddings') ||
    (model.output_modalities?.length &&
      !model.output_modalities.includes('text'))
  ) {
    return false
  }

  // ponytail: family fallback for catalogs without modalities; replace with complete capability metadata when available.
  if (
    NON_CHAT_MODEL_PATTERNS.some((pattern) => pattern.test(model.model_name))
  ) {
    return false
  }

  const endpoints = model.supported_endpoint_types
  if (Array.isArray(endpoints) && endpoints.length > 0) {
    // Channel-wide OpenAI endpoints are also attached to image/video/vector models.
    if (endpoints.some((ep) => NON_CHAT_ENDPOINTS.has(ep))) {
      return false
    }
    return endpoints.some((ep) => CHAT_ENDPOINTS.has(ep))
  }

  return isTokenBasedModel(model)
}

/**
 * Get available groups for a model
 */
export function getAvailableGroups(
  model: PricingModel,
  usableGroup: Record<string, { desc: string; ratio: number }>
): string[] {
  const modelEnableGroups = Array.isArray(model.enable_groups)
    ? model.enable_groups
    : []

  return Object.keys(usableGroup)
    .filter((g) => !EXCLUDED_GROUPS.includes(g))
    .filter((g) => modelEnableGroups.includes(g))
}

/**
 * Read a configured group ratio while preserving valid zero ratios.
 */
export function getConfiguredGroupRatio(
  groupRatio: Record<string, number>,
  group: string
): number {
  const ratio = groupRatio[group]
  return typeof ratio === 'number' && Number.isFinite(ratio) && ratio >= 0
    ? ratio
    : 1
}

/**
 * Resolve the group ratio used by model square summary prices.
 *
 * When no specific group is selected, the model square shows the best price
 * available to the viewer. When a group filter is active, it shows that
 * group's price instead.
 */
export function getDisplayGroupRatio(
  model: PricingModel,
  selectedGroup?: string
): number {
  const modelEnableGroups = Array.isArray(model.enable_groups)
    ? model.enable_groups
    : []
  const groupRatio = model.group_ratio || {}

  if (
    selectedGroup &&
    selectedGroup !== FILTER_ALL &&
    modelEnableGroups.includes(selectedGroup)
  ) {
    return getConfiguredGroupRatio(groupRatio, selectedGroup)
  }

  if (modelEnableGroups.length === 0) {
    return 1
  }

  let minRatio = Number.POSITIVE_INFINITY

  for (const group of modelEnableGroups) {
    if (group === FILTER_ALL) continue
    const ratio = Object.hasOwn(groupRatio, group)
      ? groupRatio[group]
      : 1
    if (
      typeof ratio === 'number' &&
      Number.isFinite(ratio) &&
      ratio >= 0 &&
      ratio < minRatio
    ) {
      minRatio = ratio
    }
  }

  return minRatio === Number.POSITIVE_INFINITY ? 1 : minRatio
}

/**
 * Get the lowest effective ratio for discount display.
 * Considers only groups enabled for the model that the current visitor has access to.
 * Excludes negative, infinite, NaN, or non-numeric ratios.
 * Returns null if no valid ratio < 1 exists.
 */
export function getModelLowestEffectiveRatio(
  model: PricingModel,
  usableGroup?: Record<string, unknown>
): number | null {
  const modelEnableGroups = Array.isArray(model.enable_groups)
    ? model.enable_groups
    : []
  if (modelEnableGroups.length === 0) {
    return null
  }

  const effectiveUsable = usableGroup ?? model.usable_group
  const groupRatio = model.group_ratio || {}

  let availableGroups: string[]
  if (effectiveUsable) {
    const usableKeys = Object.keys(effectiveUsable).filter(
      (g) => !EXCLUDED_GROUPS.includes(g)
    )
    if (modelEnableGroups.includes('all')) {
      availableGroups = usableKeys
    } else {
      availableGroups = modelEnableGroups.filter(
        (g) =>
          !EXCLUDED_GROUPS.includes(g) &&
          Object.hasOwn(effectiveUsable, g)
      )
    }
  } else {
    availableGroups = modelEnableGroups.filter(
      (g) => !EXCLUDED_GROUPS.includes(g) && g !== 'all'
    )
  }

  if (availableGroups.length === 0) {
    return null
  }

  let minRatio = Number.POSITIVE_INFINITY
  let hasValid = false

  for (const group of availableGroups) {
    const isConfigured = Object.hasOwn(groupRatio, group)
    const ratio = isConfigured ? groupRatio[group] : 1
    if (typeof ratio === 'number' && Number.isFinite(ratio) && ratio >= 0) {
      hasValid = true
      if (ratio < minRatio) {
        minRatio = ratio
      }
    }
  }

  if (!hasValid || minRatio >= 1) {
    return null
  }

  return minRatio
}

export type ModelDiscountInfo = {
  isFree: boolean
  ratio: number
  discount: number
  isLowest: boolean
}

/**
 * Get discount information for model marketplace display.
 * - When selectedGroup is empty or not provided:
 *   Computes the lowest effective ratio across enabled & usable groups (isLowest: true).
 * - When selectedGroup is specified:
 *   Validates that the selectedGroup is enabled for the model and usable by the visitor,
 *   then uses that group's exact ratio (isLowest: false).
 *   If the model does not support the group or the visitor cannot use it, returns null.
 * - Excludes negative, infinite, NaN, or non-numeric ratios.
 * - Hides discount (returns null) if effective ratio >= 1.
 */
export function getModelDiscount(
  model: PricingModel,
  selectedGroupOrUsable?: string | Record<string, unknown>,
  usableGroup?: Record<string, unknown>
): ModelDiscountInfo | null {
  let selectedGroup: string | undefined
  let usable: Record<string, unknown> | undefined

  if (typeof selectedGroupOrUsable === 'string') {
    selectedGroup = selectedGroupOrUsable
    usable = usableGroup
  } else if (
    selectedGroupOrUsable &&
    typeof selectedGroupOrUsable === 'object'
  ) {
    usable = selectedGroupOrUsable
  }

  const modelEnableGroups = Array.isArray(model.enable_groups)
    ? model.enable_groups
    : []
  if (modelEnableGroups.length === 0) {
    return null
  }

  const effectiveUsable = usable ?? model.usable_group
  const groupRatio = model.group_ratio || {}

  // If a specific group is selected by the user (excluding FILTER_ALL / 'all' sentinel)
  if (
    selectedGroup &&
    selectedGroup.trim() !== '' &&
    selectedGroup.trim() !== FILTER_ALL
  ) {
    const trimmedGroup = selectedGroup.trim()
    if (EXCLUDED_GROUPS.includes(trimmedGroup)) {
      return null
    }

    // 1. Must be enabled for the model
    const isModelEnabled =
      modelEnableGroups.includes('all') ||
      modelEnableGroups.includes(trimmedGroup)
    if (!isModelEnabled) {
      return null
    }

    // 2. Must be usable by the visitor if usable groups are defined
    if (
      effectiveUsable &&
      !Object.hasOwn(effectiveUsable, trimmedGroup)
    ) {
      return null
    }

    // 3. Get group ratio (default 1 if not configured, per getDisplayGroupRatio)
    const isConfigured = Object.hasOwn(groupRatio, trimmedGroup)
    const ratio = isConfigured ? groupRatio[trimmedGroup] : 1

    if (
      typeof ratio !== 'number' ||
      !Number.isFinite(ratio) ||
      ratio < 0 ||
      ratio >= 1
    ) {
      return null
    }

    if (ratio === 0) {
      return {
        isFree: true,
        ratio: 0,
        discount: 0,
        isLowest: false,
      }
    }

    const rawDiscount = ratio * 10
    const precisionDiscount = Number.parseFloat(rawDiscount.toPrecision(12))
    const discount =
      ratio < 1 && precisionDiscount >= 10 ? rawDiscount : precisionDiscount
    return {
      isFree: false,
      ratio,
      discount,
      isLowest: false,
    }
  }

  // Otherwise, all groups: find lowest effective ratio across usable enabled groups
  const lowestRatio = getModelLowestEffectiveRatio(model, usable)
  if (lowestRatio === null) {
    return null
  }

  if (lowestRatio === 0) {
    return {
      isFree: true,
      ratio: 0,
      discount: 0,
      isLowest: true,
    }
  }

  // Calculate exact discount value (ratio * 10) preserving high precision for boundary cases (e.g. 0.0001 -> 0.001, 0.9999 -> 9.999)
  // while eliminating IEEE-754 precision noise (e.g. 0.3 * 10 -> 3 instead of 3.0000000000000004),
  // and ensuring ratios strictly less than 1 never round up to 10 (falling back to raw ratio * 10).
  const rawDiscount = lowestRatio * 10
  const precisionDiscount = Number.parseFloat(rawDiscount.toPrecision(12))
  const discount =
    lowestRatio < 1 && precisionDiscount >= 10 ? rawDiscount : precisionDiscount

  return {
    isFree: false,
    ratio: lowestRatio,
    discount,
    isLowest: true,
  }
}

/**
 * Replace model placeholder in endpoint path
 */
export function replaceModelInPath(path: string, modelName: string): string {
  return path.replaceAll('{model}', modelName)
}

/**
 * Check if model is token-based pricing
 */
export function isTokenBasedModel(model: PricingModel): boolean {
  return model.quota_type === QUOTA_TYPE_VALUES.TOKEN
}

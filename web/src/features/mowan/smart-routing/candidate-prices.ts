import {
  parseTiersFromExpr,
  splitBillingExprAndRequestRules,
} from '@/features/pricing/lib/billing-expr'
import { formatBillingCondition } from '@/features/pricing/lib/billing-expression/condition-display'
import { compileBillingExpression } from '@/features/pricing/lib/billing-expression/parser'
import type { ExpressionNode } from '@/features/pricing/lib/billing-expression/types'
import {
  getDynamicPriceEntries,
  getDynamicPriceUnitLabelKey,
} from '@/features/pricing/lib/dynamic-price'
import { getTaskPricingDisplayTiers } from '@/features/pricing/lib/task-matrix-display'
import {
  taskPriceLabel,
  taskTierConditions,
  taskUsageUnitLabel,
} from '@/features/pricing/lib/task-price-display'

import type { RoutingCandidate } from './types'

function multiplicationFactors(node: ExpressionNode): ExpressionNode[] {
  if (node.kind === 'binary' && node.operator === '*') {
    return [
      ...multiplicationFactors(node.left),
      ...multiplicationFactors(node.right),
    ]
  }
  return [node]
}

export function candidatePriceRows(
  quote: RoutingCandidate['quote'],
  t: (key: string) => string,
  language: string,
  locale: string
) {
  if (quote.reason === 'provider_usage_incompatible') return []
  const expression = quote.expression
  if (expression) {
    const compiled = compileBillingExpression(expression.rule)
    if (compiled.status !== 'ready') return []
    const split = splitBillingExprAndRequestRules(expression.rule)
    // Only multiplication factors at the fee root are additional charges.
    // Traces inside a tier or inside a factor's condition are not price structure.
    const factors = new Set(multiplicationFactors(compiled.ast))
    const requestRules = compiled.requestRules.filter((rule) =>
      factors.has(rule.node)
    )
    // Nested traced rules need the complete expression, not a partial unit price.
    if (
      requestRules.length !== compiled.requestRules.length ||
      (requestRules.length && !split.requestRuleExpr)
    ) {
      return []
    }
    if (split.requestRuleExpr) {
      const removed = compileBillingExpression(split.requestRuleExpr)
      // Every stripped factor needs one displayed root rule, including untraced syntax.
      if (
        removed.status !== 'ready' ||
        !multiplicationFactors(removed.ast).every((node) =>
          removed.requestRules.some((rule) => rule.node === node)
        ) ||
        removed.requestRules.length !== requestRules.length
      ) {
        return []
      }
    }
    const tiers = expression.task
      ? getTaskPricingDisplayTiers(split.billingExpr, expression.usage_schema)
      : parseTiersFromExpr(split.billingExpr)
    const prices = tiers.flatMap((tier) => {
      const condition =
        'unitPrices' in tier && typeof tier.unitPrices === 'object'
          ? taskTierConditions(
              tier as ReturnType<typeof getTaskPricingDisplayTiers>[number],
              expression.usage_schema,
              language,
              t
            )
          : [
              tier.conditions
                .map((c) => ('var' in c ? `${c.var} ${c.op} ${c.value}` : ''))
                .filter(Boolean)
                .join(' · '),
              tier.conditionText
                ? (formatBillingCondition(tier.conditionText, t, language) ??
                  tier.conditionText)
                : '',
            ]
              .filter(Boolean)
              .join(' · ')
      return getDynamicPriceEntries(tier, {
        tokenUnit: 'M',
        groupRatioMultiplier: expression.group_ratio,
        usageSchema: expression.usage_schema,
      }).map((entry) => {
        const label =
          entry.labelKind === 'schema'
            ? taskPriceLabel(entry.description, entry.label, language)
            : t(entry.label)
        const unit = taskUsageUnitLabel(
          entry,
          language,
          t(getDynamicPriceUnitLabelKey(entry) ?? '1M token')
        )
        return {
          label: [
            requestRules.length ? t('Base price') : '',
            tiers.length > 1 ? tier.label : '',
            condition,
            label,
          ]
            .filter(Boolean)
            .join(' · '),
          value: `${entry.formatted} / ${unit}`,
        }
      })
    })
    if (!prices.length) return []
    return [
      ...prices,
      ...requestRules.map((rule) => {
        const condition = compiled.source.slice(
          rule.condition.start,
          rule.condition.end
        )
        const description = formatBillingCondition(condition, t, language)
        const displayCondition =
          description && !description.includes('{{') ? description : condition
        return {
          label: `${t('Request pricing rule')} · ${displayCondition}`,
          value: `×${new Intl.NumberFormat(locale, { maximumSignificantDigits: 21, useGrouping: false }).format(rule.multiplier)} · ${t('Otherwise ×1')}`,
        }
      }),
    ]
  }
  const labels: Record<string, string> = {
    input: 'Input tokens',
    output: 'Output tokens',
    cache_read: 'Cache read price',
    cache_write: 'Cache write',
    cache_write_1h: 'Cache Write (1h)',
    cache_write_12h: 'Cache Write (12h)',
    image_input: 'Image input',
    audio_input: 'Audio input',
    audio_output: 'Audio output',
    request: 'Request',
    request_rule: 'Request pricing rule',
  }
  const units: Record<string, string> = {
    'USD/1M tokens': '1M token',
    'USD/request': 'Request',
    'USD/image': 'Image',
    multiplier: 'Multiplier',
  }
  const format = (value: number, unit: string) =>
    new Intl.NumberFormat(
      locale,
      unit === 'multiplier'
        ? { maximumFractionDigits: 8 }
        : {
            style: 'currency',
            currency: quote.currency,
            maximumFractionDigits: 8,
          }
    ).format(value)
  return quote.prices.flatMap((price) => {
    let value: string | null = null
    if (typeof price.amount === 'number' && Number.isFinite(price.amount)) {
      value = format(price.amount, price.unit)
    } else if (
      typeof price.min === 'number' &&
      typeof price.max === 'number' &&
      Number.isFinite(price.min) &&
      Number.isFinite(price.max)
    ) {
      value = `${format(price.min, price.unit)} – ${format(price.max, price.unit)}`
    }
    return value === null && price.rule
      ? []
      : [
          {
            label: t(labels[price.label] ?? price.label),
            value: `${value ?? t('Unknown')} / ${t(units[price.unit] ?? price.unit)}${price.rule ? ` · ${price.rule}` : ''}`,
          },
        ]
  })
}

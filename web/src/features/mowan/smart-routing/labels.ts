import type { TFunction } from 'i18next'

export function routingReasonLabel(reason: string, t: TFunction): string {
  const unavailable = t('Price unavailable')
  const reasons: Record<string, string> = {
    price_unconfigured: unavailable,
    channel_pricing_ambiguous: t('Prices cannot be compared yet.'),
    group_ratio_invalid: unavailable,
    provider_price_unavailable: unavailable,
    provider_usage_incompatible: t(
      'This provider cannot supply all usage fields required by the pricing rule.'
    ),
    billing_expression_invalid: unavailable,
    price_invalid: unavailable,
    variant_unresolved: unavailable,
    request_usage_unknown: t(
      'Usage is not yet known, so the total cost cannot be estimated.'
    ),
    estimate_unavailable: t(
      'The cost of this request cannot be estimated yet.'
    ),
    estimated_output_limit: t(
      'Estimated at the requested output limit. Actual usage may differ.'
    ),
    same_billing_contract: t('These prices use the same billing rules.'),
    estimated_request: t('Estimated total'),
    mixed_provider_pricing: t('Prices cannot be compared yet.'),
    different_billing_contract: t(
      'Different billing methods prevent a price comparison.'
    ),
    mixed_provider_operation: t(
      'Metrics combine multiple provider operations.'
    ),
    cold_start: t('Too few recent calls; price is used first for now.'),
    insufficient_samples: t(
      'Too few recent calls; price is used first for now.'
    ),
    price_unavailable: t(
      'Prices cannot be compared; use a stable fallback order.'
    ),
    strategy_selected: t('Selected using your routing strategy.'),
    routing_no_candidate: t('No routing candidates'),
  }
  return reasons[reason] ?? t('Unknown')
}

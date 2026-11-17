import type { TFunction } from 'i18next'

export function routingReasonLabel(reason: string, t: TFunction): string {
  const unavailable = t('Price unavailable')
  const reasons: Record<string, string> = {
    price_unconfigured: unavailable,
    channel_pricing_ambiguous: t(
      'Providers use different billing rules; these prices cannot be compared.'
    ),
    group_ratio_invalid: unavailable,
    provider_price_unavailable: unavailable,
    billing_expression_invalid: unavailable,
    price_invalid: unavailable,
    variant_unresolved: unavailable,
    request_usage_unknown: t(
      'Request usage is unknown; a reliable price comparison is unavailable.'
    ),
    estimate_unavailable: t(
      'Request usage is unknown; a reliable price comparison is unavailable.'
    ),
    estimated_output_limit: t(
      'Estimated at the requested output limit. Actual usage may differ.'
    ),
    same_billing_contract: t('These prices use the same billing rules.'),
    estimated_request: t('Estimated total'),
    mixed_provider_pricing: t(
      'Providers use different billing rules; these prices cannot be compared.'
    ),
    different_billing_contract: t(
      'Providers use different billing rules; these prices cannot be compared.'
    ),
    mixed_provider_operation: t(
      'Metrics combine multiple provider operations.'
    ),
    cold_start: t('No recent samples; temporarily rank by price.'),
    insufficient_samples: t(
      'Too few recent samples; temporarily rank by price.'
    ),
    price_unavailable: t(
      'Prices cannot be compared; use a stable fallback order.'
    ),
    strategy_selected: t('Selected using your routing strategy.'),
    routing_no_candidate: t('No routing candidates'),
  }
  return reasons[reason] ?? t('Unknown')
}

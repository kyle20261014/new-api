import {
  routingSchema,
  type RoutingPolicy,
  type CustomRouting,
  type ModelRouting,
  type RoutingCandidate,
} from './types'

export function defaultRouting(): RoutingPolicy {
  return { schema_version: 1, mode: 'smart', strategy: 'balanced' }
}

export function normalizeRouting(policy: RoutingPolicy): RoutingPolicy {
  if (policy.mode === 'fixed') {
    return { schema_version: 1, mode: 'fixed', fixed_group: policy.fixed_group }
  }
  if (policy.strategy === 'custom') {
    return {
      schema_version: 1,
      mode: 'smart',
      strategy: 'custom',
      models: Object.fromEntries(
        Object.entries(policy.models)
          .sort(([a], [b]) => (a < b ? -1 : Number(a > b)))
          .map(([model, config]) => [
            model,
            {
              strategy: config.strategy,
              try_soft_offline_groups: [
                ...config.try_soft_offline_groups,
              ].sort(),
              groups:
                config.strategy === 'manual'
                  ? [...config.groups]
                  : [...config.groups].sort(),
            },
          ])
      ),
    }
  }
  return {
    schema_version: 1,
    mode: 'smart',
    strategy: policy.strategy,
    excluded_models: [...(policy.excluded_models ?? [])].sort(),
    excluded_groups: [...(policy.excluded_groups ?? [])].sort(),
    excluded_model_groups: Object.fromEntries(
      Object.entries(policy.excluded_model_groups ?? {}).map(
        ([model, groups]) => [model, [...groups].sort()]
      )
    ),
  }
}

export function setModelGroups(
  policy: CustomRouting,
  model: string,
  groups: string[]
): CustomRouting {
  const models = { ...policy.models }
  if (groups.length === 0) delete models[model]
  else {
    models[model] = {
      ...(models[model] ?? {
        strategy: Object.values(models)[0]?.strategy ?? 'balanced',
      }),
      groups,
      try_soft_offline_groups: (
        models[model]?.try_soft_offline_groups ?? []
      ).filter((group) => groups.includes(group)),
    }
  }
  return { ...policy, models }
}

export function moveGroup(
  config: ModelRouting,
  index: number,
  direction: 'up' | 'down'
): ModelRouting {
  const target = index + (direction === 'up' ? -1 : 1)
  if (
    index < 0 ||
    index >= config.groups.length ||
    target < 0 ||
    target >= config.groups.length
  ) {
    return config
  }
  const groups = [...config.groups]
  const current = groups[index]
  const next = groups[target]
  if (current === undefined || next === undefined) return config
  groups[index] = next
  groups[target] = current
  return { ...config, groups }
}

export function routingIsValid(
  policy: RoutingPolicy,
  candidates?: RoutingCandidate[]
): boolean {
  if (
    !routingSchema.safeParse(policy).success ||
    new TextEncoder().encode(JSON.stringify(policy)).length > 65536
  ) {
    return false
  }
  if (policy.mode === 'fixed') {
    return (
      policy.fixed_group !== '' &&
      policy.fixed_group !== 'auto' &&
      policy.fixed_group !== 'all'
    )
  }
  if (policy.strategy !== 'custom') return true
  return Object.entries(policy.models).every(
    ([model, config]) =>
      new Set(config.groups).size === config.groups.length &&
      config.groups.every(
        (group) =>
          !candidates ||
          candidates.some(
            (candidate) =>
              candidate.model === model &&
              candidate.group === group &&
              candidate.available
          )
      )
  )
}

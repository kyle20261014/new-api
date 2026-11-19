import { HugeiconsIcon } from '@hugeicons/react'

import { useRoutingStrategies } from './routing-strategies'

export function StrategyBadge({ strategy }: { strategy: string }) {
  const option = useRoutingStrategies().find((item) => item.value === strategy)
  if (!option) return null
  return (
    <span
      className={`inline-flex max-w-full items-center gap-1.5 rounded-full px-2.5 py-1 text-sm font-semibold ${option.color}`}
    >
      <HugeiconsIcon icon={option.icon} size={16} aria-hidden='true' />
      <span className='truncate'>{option.label}</span>
    </span>
  )
}

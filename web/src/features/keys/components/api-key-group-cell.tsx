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
import { useTranslation } from 'react-i18next'

import { BadgeCell, TruncatedCell } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { StatusBadge } from '@/components/status-badge'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import type { RoutingPolicy } from '@/features/mowan/smart-routing/types'
import { useMediaQuery } from '@/hooks'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'

import { GroupRatioBadge, type GroupRatio } from './auto-group-visuals'

type ApiKeyGroupCellProps = {
  crossGroupRetry: boolean
  group: string
  routing?: RoutingPolicy | null
  routingRequiresUpdate?: boolean
  ratio?: GroupRatio
  shouldReduceMotion: boolean
}

export function ApiKeyGroupCell(props: ApiKeyGroupCellProps) {
  const { t, i18n } = useTranslation()
  const isMobile = useMediaQuery('(max-width: 640px)')

  if (props.routingRequiresUpdate) {
    return (
      <StatusBadge
        label={t('Routing needs update')}
        variant='warning'
        copyable={false}
      />
    )
  }

  if (props.routing) {
    const routing = props.routing
    const mode =
      routing.mode === 'fixed' ? t('Fixed group') : t('Smart routing')
    let summary: string
    if (routing.mode === 'fixed') {
      summary = routing.fixed_group || t('Inherited group')
    } else if (routing.strategy === 'custom') {
      const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
      summary = `${t('Custom per model')} · ${t('Models')}: ${formatNumber(Object.keys(routing.models).length, locale)} · ${Object.keys(routing.models).join(', ')}`
    } else {
      const strategies = {
        balanced: t('Balanced'),
        price: t('Price first'),
        speed: t('Speed first'),
        success: t('Success first'),
      }
      summary = strategies[routing.strategy]
    }
    return (
      <TruncatedCell
        className={isMobile ? 'w-full' : 'max-w-50'}
        tabIndex={0}
        tooltipContent={`${mode} · ${summary}`}
        tooltipClassName='break-all'
      >
        <span className='block font-medium'>{mode}</span>
        <span className='text-muted-foreground block truncate text-xs'>
          {summary}
        </span>
      </TruncatedCell>
    )
  }

  const group = props.group?.trim() || ''
  if (group !== 'auto') {
    const ratio =
      group && typeof props.ratio === 'number' ? props.ratio : undefined
    return (
      <TruncatedCell
        className={isMobile ? 'w-full' : 'max-w-50'}
        tabIndex={0}
        tooltipContent={group || t('Follow user group')}
        tooltipClassName='break-all'
      >
        <GroupBadge
          group={group}
          ratio={ratio}
          ratioLabel={group ? undefined : t('Inherited')}
          type='text'
          className='px-0'
          containerClassName={cn('gap-3', isMobile && 'w-full justify-between')}
        />
      </TruncatedCell>
    )
  }

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <BadgeCell
            data-api-key-group-cell='auto'
            tabIndex={0}
            className={cn(
              'ml-0 gap-3 overflow-visible text-xs',
              isMobile ? 'w-full justify-between' : 'max-w-50'
            )}
          />
        }
      >
        <StatusBadge
          label={t('Cross-group')}
          variant='info'
          copyable={false}
          className='px-0'
        />
        <GroupRatioBadge
          ratio={props.ratio}
          isAuto
          shouldReduceMotion={props.shouldReduceMotion}
        />
      </TooltipTrigger>
      <TooltipContent>
        <span className='text-xs'>
          {t(
            'Automatically selects the best available group with circuit breaker mechanism'
          )}
        </span>
      </TooltipContent>
    </Tooltip>
  )
}

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
import { useQuery } from '@tanstack/react-query'
import {
  Activity,
  ArrowDownToLine,
  Info,
  LineChart,
  ReceiptText,
  RotateCw,
  Scale,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { getSiteStats } from '@/features/dashboard/api'
import { StatCard } from '@/features/dashboard/components/ui/stat-card'
import type { SiteStatsPeriodData } from '@/features/dashboard/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatNumber, formatTimestampToDate } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import {
  getServerErrorMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

interface StatPeriodGroupProps {
  title: string
  periodData: SiteStatsPeriodData
  currentBalance: number
  locale?: string
}

function StatPeriodGroup({
  title,
  periodData,
  currentBalance,
  locale,
}: StatPeriodGroupProps) {
  const { t } = useTranslation()

  return (
    <div className='bg-muted/20 rounded-xl border p-3.5 sm:p-4'>
      <h4 className='mb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground'>
        {title}
      </h4>

      <div className='grid grid-cols-2 gap-2.5 sm:gap-3'>
        <div className='bg-card rounded-xl border p-2.5 shadow-2xs sm:p-3'>
          <StatCard
            title={t('Credited top-ups')}
            value={formatQuotaWithCurrency(periodData.top_up_quota, {
              locale,
              digitsLarge: 2,
              digitsSmall: 6,
            })}
            description={t('Wallet credited')}
            icon={ArrowDownToLine}
            iconTone='success'
            compactMobile
          />
        </div>

        <div className='bg-card rounded-xl border p-2.5 shadow-2xs sm:p-3'>
          <StatCard
            title={t('Recognized revenue')}
            value={formatQuotaWithCurrency(periodData.consumed_quota, {
              locale,
              digitsLarge: 2,
              digitsSmall: 6,
            })}
            description={t('User consumption')}
            icon={ReceiptText}
            iconTone='primary'
            compactMobile
          />
        </div>

        <div className='bg-card rounded-xl border p-2.5 shadow-2xs sm:p-3'>
          <StatCard
            title={t('User balance')}
            value={formatQuotaWithCurrency(currentBalance, {
              locale,
              digitsLarge: 2,
              digitsSmall: 6,
            })}
            description={t('Current')}
            icon={Scale}
            iconTone='warning'
            action={
              <Badge variant='outline' className='h-4 px-1 py-0 text-[10px]'>
                {t('Current')}
              </Badge>
            }
            compactMobile
          />
        </div>

        <div className='bg-card rounded-xl border p-2.5 shadow-2xs sm:p-3'>
          <StatCard
            title={t('Request count')}
            value={formatNumber(periodData.request_count, locale)}
            description={t('Logged requests')}
            icon={Activity}
            iconTone='info'
            compactMobile
          />
        </div>
      </div>
    </div>
  )
}

export function SiteStats() {
  const user = useAuthStore((state) => state.auth.user)
  const isAdmin = Boolean(user?.role && user.role >= ROLE.ADMIN)

  useSystemConfigStore((state) => state.config.currency)

  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  const siteStatsQuery = useQuery({
    queryKey: ['dashboard', 'site-stats'],
    queryFn: async () => requireServerSuccess(await getSiteStats()),
    enabled: isAdmin,
    staleTime: 60 * 1000,
  })

  if (!isAdmin) {
    return null
  }

  if (siteStatsQuery.isLoading) {
    return (
      <section
        data-testid='site-stats-loading'
        className='bg-card overflow-hidden rounded-2xl border p-4 shadow-xs sm:p-5'
      >
        <div className='flex items-center justify-between border-b pb-3'>
          <div className='flex items-center gap-2'>
            <Skeleton className='size-8 rounded-lg' />
            <Skeleton className='h-4 w-28' />
          </div>
          <Skeleton className='h-4 w-36' />
        </div>
        <div className='mt-4 grid grid-cols-1 gap-4 lg:grid-cols-2'>
          {[0, 1].map((group) => (
            <div key={group} className='bg-muted/20 rounded-xl border p-3.5 sm:p-4'>
              <Skeleton className='mb-3 h-4 w-16' />
              <div className='grid grid-cols-2 gap-2.5 sm:gap-3'>
                {[0, 1, 2, 3].map((card) => (
                  <div key={card} className='bg-card rounded-xl border p-3'>
                    <Skeleton className='h-4 w-20' />
                    <Skeleton className='mt-2 h-6 w-24' />
                    <Skeleton className='mt-1 h-3 w-16' />
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
      </section>
    )
  }

  if (siteStatsQuery.isError) {
    return (
      <section
        data-testid='site-stats-error'
        className='bg-card overflow-hidden rounded-2xl border p-4 shadow-xs sm:p-5'
      >
        <ErrorState
          title={t('Failed to load site statistics')}
          description={getServerErrorMessage(
            siteStatsQuery.error,
            t('Failed to load site statistics')
          )}
          onRetry={() => siteStatsQuery.refetch()}
        />
      </section>
    )
  }

  const stats = siteStatsQuery.data?.data
  if (!stats) {
    return null
  }

  let formattedUpdatedAt = '-'
  if (stats.updated_at) {
    try {
      formattedUpdatedAt = new Intl.DateTimeFormat(locale, {
        timeZone: stats.timezone || 'Asia/Shanghai',
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
      }).format(new Date(stats.updated_at * 1000))
    } catch {
      formattedUpdatedAt = formatTimestampToDate(stats.updated_at)
    }
  }

  return (
    <section
      data-testid='site-stats'
      className='bg-card overflow-hidden rounded-2xl border shadow-xs'
      aria-labelledby='site-stats-heading'
    >
      <div className='flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3 sm:px-5'>
        <div className='flex min-w-0 items-center gap-2.5'>
          <IconBadge tone='primary' size='sm'>
            <LineChart aria-hidden='true' />
          </IconBadge>
          <div className='min-w-0'>
            <div className='flex items-center gap-2'>
              <h3
                id='site-stats-heading'
                className='truncate text-sm font-semibold'
              >
                {t('Site statistics')}
              </h3>
              <span className='text-muted-foreground hidden text-xs sm:inline'>
                ({stats.timezone})
              </span>
            </div>
            <p className='text-muted-foreground truncate text-xs'>
              {t('Updated at: {{time}}', { time: formattedUpdatedAt })}
            </p>
          </div>
        </div>

        <Button
          variant='ghost'
          size='icon-sm'
          onClick={() => siteStatsQuery.refetch()}
          disabled={siteStatsQuery.isFetching}
          aria-label={t('Retry')}
          title={t('Retry')}
        >
          <RotateCw
            className={cn(
              'size-4 text-muted-foreground transition-transform',
              siteStatsQuery.isFetching && 'animate-spin'
            )}
            aria-hidden='true'
          />
        </Button>
      </div>

      <div className='space-y-4 p-4 sm:p-5'>
        <div className='grid grid-cols-1 gap-4 lg:grid-cols-2'>
          <StatPeriodGroup
            title={t('Today')}
            periodData={stats.today}
            currentBalance={stats.current_balance_quota}
            locale={locale}
          />
          <StatPeriodGroup
            title={t('Current month')}
            periodData={stats.month}
            currentBalance={stats.current_balance_quota}
            locale={locale}
          />
        </div>

        <div className='bg-muted/40 text-muted-foreground flex items-start gap-2 rounded-xl p-3 text-xs leading-relaxed'>
          <Info
            className='mt-0.5 size-4 shrink-0 text-muted-foreground'
            aria-hidden='true'
          />
          <p className='min-w-0 flex-1 break-words'>
            {t(
              'Top-ups reflect actual wallet credits. Revenue and requests are derived from recorded user consumption logs (including zero-charge calls; excludes failed logs) and depend on log retention policies. Balance represents current user wallet totals.'
            )}
          </p>
        </div>
      </div>
    </section>
  )
}

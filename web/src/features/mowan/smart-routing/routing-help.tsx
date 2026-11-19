import { HelpCircleIcon, Settings03Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'

import { useRoutingStrategies } from './routing-strategies'
import { StrategyBadge } from './strategy-badge'

export function RoutingHelp() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const strategies = useRoutingStrategies()
  return (
    <section className='min-w-0 space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h3 className='flex items-center gap-3 font-semibold'>
          <span className='rounded-full bg-amber-500/10 p-2.5 text-amber-700 dark:text-amber-300'>
            <HugeiconsIcon icon={Settings03Icon} size={18} aria-hidden='true' />
          </span>
          {t('Routing strategy')}
        </h3>
        <Dialog
          title={t('How should I choose a routing strategy?')}
          open={open}
          onOpenChange={setOpen}
          trigger={
            <Button type='button' variant='ghost' className='min-h-11'>
              <HugeiconsIcon
                icon={HelpCircleIcon}
                size={16}
                aria-hidden='true'
              />
              {t('How to choose?')}
            </Button>
          }
          contentHeight='min(70dvh, 720px)'
          bodyClassName='space-y-4'
          showCloseButton={false}
          footer={
            <Button
              type='button'
              className='min-h-11'
              onClick={() => setOpen(false)}
            >
              {t('Close')}
            </Button>
          }
        >
          <p className='text-sm leading-relaxed'>
            {t(
              'A routing strategy decides which group to try first. If you are unsure, choose Balanced.'
            )}
          </p>
          <dl className='grid gap-3 text-sm sm:grid-cols-2'>
            {strategies.map((option) => (
              <div key={option.value} className='space-y-1.5'>
                <dt>
                  <StrategyBadge strategy={option.value} />
                </dt>
                <dd className='text-muted-foreground text-xs leading-relaxed'>
                  {option.description}
                </dd>
              </div>
            ))}
            <div>
              <dt className='font-medium'>{t('Fixed group')}</dt>
              <dd>
                {t(
                  'Uses only your chosen group and does not switch groups if it fails.'
                )}
              </dd>
            </div>
          </dl>
          <section className='space-y-3 border-t border-amber-500/20 pt-3 text-xs leading-relaxed'>
            <h4 className='text-sm font-semibold'>
              {t('Which groups will the system use?')}
            </h4>
            <p>
              {t(
                'Smart routing chooses from models and groups you can use and have not excluded. Newly available groups join automatically. The Key’s model, IP and quota limits still apply.'
              )}
            </p>
            <h4 className='text-sm font-semibold'>
              {t('How should I read prices, speed and success rates?')}
            </h4>
            <p>
              {t(
                'Price first compares the estimated cost of this request. Unknown prices do not mean free. Different billing methods may prevent a direct comparison.'
              )}
            </p>
            <p>
              {t(
                'Speed and success rate use call records from the last 24 hours. If records are too few or too old, price is used first.'
              )}
            </p>
            <p>
              {t(
                'Cards show timing from the last hour: when chat starts replying, when a regular request returns a result, or when an image or video task finishes. Submitting a task does not mean it is complete.'
              )}
            </p>
            <h4 className='text-sm font-semibold'>
              {t('How do custom routing and a fixed group differ?')}
            </h4>
            <p>
              {t(
                'Custom routing uses only your selected models and groups, automatically or in your chosen order. If a model has only one selected group, there is no other selected group to try if it fails.'
              )}
            </p>
            <p>
              {t(
                'A fixed group always uses the specified group. Different channels within that group may still be tried, but it will not switch groups.'
              )}
            </p>
          </section>
          <p className='border-t border-amber-500/20 pt-3 text-xs leading-relaxed'>
            {t(
              'These strategies help you choose; they do not guarantee the lowest price, fastest response or success every time. Switching groups after a failure may cost more. Final charges depend on actual usage and the group that succeeds.'
            )}
          </p>
        </Dialog>
      </div>
    </section>
  )
}

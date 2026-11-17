import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'

export function RoutingHelp() {
  const { t } = useTranslation()
  return (
    <Dialog
      title={t('How routing works')}
      trigger={
        <Button type='button' variant='ghost' className='min-h-11'>
          {t('How to choose?')}
        </Button>
      }
    >
      <dl className='space-y-4 text-sm'>
        <div>
          <dt className='font-medium'>{t('Balanced')}</dt>
          <dd>
            {t(
              'Balances effective price, response time and reliability. A good starting point for most requests.'
            )}
          </dd>
        </div>
        <div>
          <dt className='font-medium'>{t('Price first')}</dt>
          <dd>
            {t(
              'Prefers comparable effective prices among eligible candidates. Unknown prices are not treated as free.'
            )}
          </dd>
        </div>
        <div>
          <dt className='font-medium'>{t('Speed first')}</dt>
          <dd>
            {t(
              'Uses response observations from the last hour: first token for streaming, response for synchronous calls, completion for tasks. Submission time is not task completion time.'
            )}
          </dd>
        </div>
        <div>
          <dt className='font-medium'>{t('Success first')}</dt>
          <dd>
            {t(
              'Uses the last 24 hours of outcomes and sample counts. Missing, sparse or stale observations fall back conservatively; a rate is not a guarantee.'
            )}
          </dd>
        </div>
        <div>
          <dt className='font-medium'>{t('Custom per model')}</dt>
          <dd>
            {t(
              'Choose allowed groups per model, then a dynamic strategy or manual order. Manual order may skip unavailable candidates. Temporary health bypass never overrides disabled channels or permissions.'
            )}
          </dd>
        </div>
        <div>
          <dt className='font-medium'>{t('Fixed group')}</dt>
          <dd>
            {t(
              'Stays within one group and does not switch to another group on failure. Existing channel retries still apply.'
            )}
          </dd>
        </div>
      </dl>
      <p className='mt-4 text-sm'>
        {t(
          'Quotes reflect current specifications and effective prices. The first choice is an estimate; retries can switch to a more expensive group. Final billing uses actual usage and the successful group. Routing does not promise the lowest price, fastest response or guaranteed success.'
        )}
      </p>
    </Dialog>
  )
}

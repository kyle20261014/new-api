import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'

import { getRoutingSubmission } from './api'
import { SubmissionView } from './submission-view'

export function SubmissionTracker(props: { id: string }) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['routing-submission', props.id],
    queryFn: () => getRoutingSubmission(props.id),
  })
  if (query.isPending) return <LoadingState />
  if (query.isError) {
    return (
      <ErrorState
        description={t(
          'Could not load submission status. Keep your submission ID and retry this status check.'
        )}
        onRetry={() => void query.refetch()}
      />
    )
  }
  return (
    <div className='space-y-4'>
      <SubmissionView submission={query.data} />
      <Button
        type='button'
        variant='outline'
        className='min-h-11'
        disabled={query.isFetching}
        aria-busy={query.isFetching}
        onClick={() => void query.refetch()}
      >
        {t('Refresh')}
      </Button>
    </div>
  )
}

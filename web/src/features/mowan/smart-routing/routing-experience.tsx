import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { lazy, Suspense, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { getUserGroups } from '@/features/playground/api'
import { useAuthStore } from '@/stores/auth-store'

import { defaultRouting } from './policy'
import { RoutingControl } from './routing-control'
import { RoutingHelp } from './routing-help'
import type { RoutingPolicy } from './types'

const Playground = lazy(async () => ({
  default: (await import('@/features/playground')).Playground,
}))

function MediaRoutingPreview(props: { model?: string }) {
  const { t } = useTranslation()
  const [routing, setRouting] = useState<RoutingPolicy>(defaultRouting)
  const groups = useQuery({
    queryKey: ['playground-groups'],
    queryFn: getUserGroups,
  })
  return (
    <div className='space-y-4'>
      <p>
        {t(
          'This media model uses the existing API workflow. A new creation workspace is not available here.'
        )}
      </p>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Applies only to these requests. No saved key is changed. Actual account billing still applies.'
        )}
      </p>
      <RoutingControl
        value={routing}
        onChange={setRouting}
        model={props.model}
        groups={groups.data?.map((group) => group.value)}
      />
    </div>
  )
}

export function RoutingExperience(props: {
  model: string
  canChat?: boolean
  modelIsDisplayName?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [localOpen, setLocalOpen] = useState(false)
  const open = props.open ?? localOpen
  const onOpenChange = props.onOpenChange ?? setLocalOpen
  return (
    <Dialog
      title={
        props.modelIsDisplayName
          ? t('Choose a model')
          : t('Try {{model}}', { model: props.model })
      }
      open={open}
      onOpenChange={onOpenChange}
      trigger={
        props.open === undefined ? (
          <Button type='button' variant='outline' className='min-h-11'>
            {t('Try model')}
          </Button>
        ) : undefined
      }
      contentClassName='sm:max-w-5xl'
      contentHeight='min(80dvh, 800px)'
    >
      {!user && (
        <div className='space-y-4'>
          <p>
            {t(
              'Sign in to view your eligible groups and try temporary routing. Public model prices remain available in the model catalog.'
            )}
          </p>
          <RoutingHelp />
          <Button render={<Link to='/sign-in' />} className='min-h-11'>
            {t('Sign in')}
          </Button>
        </div>
      )}
      {user && props.canChat && (
        <div className='h-[65dvh] min-h-80'>
          <Suspense fallback={<LoadingState />}>
            <Playground
              initialModel={props.modelIsDisplayName ? undefined : props.model}
            />
          </Suspense>
        </div>
      )}
      {user && !props.canChat && (
        <MediaRoutingPreview
          model={props.modelIsDisplayName ? undefined : props.model}
        />
      )}
    </Dialog>
  )
}

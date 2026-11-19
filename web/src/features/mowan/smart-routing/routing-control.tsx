import { ArrowRight01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { previewRouting } from './api'
import { CandidateCard } from './candidate-card'
import { CustomRoutingDialog } from './custom-routing-dialog'
import { DynamicExclusionsDialog } from './dynamic-exclusions-dialog'
import { routingReasonLabel } from './labels'
import { defaultRouting, normalizeRouting } from './policy'
import { RoutingHelp } from './routing-help'
import { RoutingSelect } from './routing-select'
import { useRoutingStrategies } from './routing-strategies'
import { StrategyBadge } from './strategy-badge'
import type {
  CustomRouting,
  DynamicRouting,
  DynamicStrategy,
  RoutingPolicy,
} from './types'

export function RoutingControl(props: {
  value: RoutingPolicy | null
  onChange: (policy: RoutingPolicy) => void
  groups?: string[]
  legacyGroup?: string | null
  model?: string
  disabled?: boolean
  previewRequest?: Record<string, unknown>
  tokenId?: number
  keyConfiguration?: boolean
}) {
  const { t, i18n } = useTranslation()
  const [customOpen, setCustomOpen] = useState(false)
  const [exclusionsOpen, setExclusionsOpen] = useState(false)
  const [customDraft, setCustomDraft] = useState<CustomRouting>({
    schema_version: 1,
    mode: 'smart',
    strategy: 'custom',
    models: {},
  })
  const [smartDraft, setSmartDraft] = useState<RoutingPolicy>(defaultRouting)
  const [dynamicDraft, setDynamicDraft] = useState<DynamicRouting>({
    schema_version: 1,
    mode: 'smart',
    strategy: 'balanced',
  })
  const [fixedDraft, setFixedDraft] = useState<RoutingPolicy>({
    schema_version: 1,
    mode: 'fixed',
    fixed_group:
      props.legacyGroup && props.legacyGroup !== 'auto'
        ? props.legacyGroup
        : '',
  })
  const [previewModel, setPreviewModel] = useState(props.model ?? '')
  const dynamic =
    props.value?.mode === 'smart' && props.value.strategy !== 'custom'
      ? props.value
      : null
  const preview = useMutation({
    mutationFn: (snapshot: {
      routing: RoutingPolicy
      model: string
      request?: Record<string, unknown>
      tokenId?: number
    }) =>
      previewRouting(
        normalizeRouting(snapshot.routing),
        snapshot.model,
        snapshot.request,
        snapshot.tokenId
      ),
    meta: { errorToast: false },
  })
  const previewIsCurrent =
    preview.variables?.tokenId === props.tokenId &&
    JSON.stringify(preview.variables?.routing) ===
      JSON.stringify(props.value) &&
    preview.variables?.model === (props.model || previewModel) &&
    JSON.stringify(preview.variables?.request) ===
      JSON.stringify(props.previewRequest)
  const runPreview = () => {
    if (props.value) {
      preview.mutate({
        routing: props.value,
        model: props.model || previewModel,
        request: props.previewRequest,
        tokenId: props.tokenId,
      })
    }
  }
  const groupNames = [...new Set(props.groups ?? [])].filter(
    (group) => group !== 'auto' && group !== 'all'
  )
  const strategies = useRoutingStrategies()
  const strategyOptions = strategies.map((item) => ({
    ...item,
    badge: <StrategyBadge strategy={item.value} />,
  }))
  const modeOptions = [
    { value: 'smart', label: t('Smart routing') },
    { value: 'fixed', label: t('Fixed group') },
  ]
  if (!props.value) {
    modeOptions.unshift({
      value: 'legacy',
      label:
        props.legacyGroup === 'auto'
          ? t('Legacy Auto · original behavior')
          : t('Legacy routing · original behavior'),
    })
  }
  const exclusionCount =
    (dynamic?.excluded_models?.length ?? 0) +
    (dynamic?.excluded_groups?.length ?? 0) +
    Object.values(dynamic?.excluded_model_groups ?? {}).reduce(
      (sum, groups) => sum + groups.length,
      0
    )
  return (
    <fieldset
      disabled={props.disabled}
      className='bg-muted/10 min-w-0 space-y-4 rounded-3xl border p-4 [&_button]:min-h-11'
    >
      <RoutingHelp />
      <div className='bg-background/40 space-y-4 rounded-2xl border p-3'>
        <RoutingSelect
          label={t('Routing mode')}
          value={props.value?.mode ?? 'legacy'}
          options={modeOptions}
          onChange={(mode) => {
            if (props.value?.mode === 'smart') setSmartDraft(props.value)
            if (props.value?.mode === 'fixed') setFixedDraft(props.value)
            props.onChange(mode === 'fixed' ? fixedDraft : smartDraft)
          }}
        />
        {!props.value && (
          <p className='text-muted-foreground text-sm break-words'>
            {t(
              'The original settings stay in use until you change the routing.'
            )}{' '}
            · {props.legacyGroup || t('Inherited group')}
          </p>
        )}
        {props.value?.mode === 'fixed' && (
          <>
            <RoutingSelect
              label={t('Fixed group')}
              value={props.value.fixed_group}
              options={[
                ...new Set([props.value.fixed_group, ...groupNames]),
              ].map((group) => ({
                value: group,
                label: group || t('Inherited group'),
              }))}
              onChange={(group) =>
                props.onChange({
                  schema_version: 1,
                  mode: 'fixed',
                  fixed_group: group,
                })
              }
            />
            <p className='text-muted-foreground text-sm leading-relaxed'>
              {t(
                'Uses only your chosen group and does not switch groups if it fails.'
              )}
            </p>
          </>
        )}
        {props.value?.mode === 'smart' && (
          <>
            <RoutingSelect
              label={t('Routing strategy')}
              value={props.value.strategy}
              options={strategyOptions}
              onChange={(strategy) => {
                if (strategy === 'custom') {
                  if (dynamic) setDynamicDraft(dynamic)
                  setCustomOpen(true)
                  return
                }
                if (
                  props.value?.mode === 'smart' &&
                  props.value.strategy === 'custom'
                ) {
                  setCustomDraft(props.value)
                }
                const previous =
                  props.value?.mode === 'smart' &&
                  props.value.strategy !== 'custom'
                    ? props.value
                    : dynamicDraft
                props.onChange({
                  ...previous,
                  strategy: strategy as DynamicStrategy,
                })
              }}
            />
            <p className='text-muted-foreground text-sm leading-relaxed'>
              {
                strategies.find(
                  (item) =>
                    item.value ===
                    (props.value?.mode === 'smart' ? props.value.strategy : '')
                )?.description
              }
            </p>
            {props.value.strategy === 'custom' && (
              <div className='space-y-2 border-t pt-3'>
                <p className='text-muted-foreground text-sm break-words'>
                  {Object.keys(props.value.models).join(' · ')}
                </p>
                <Button
                  type='button'
                  variant='outline'
                  className='w-full justify-between rounded-full'
                  onClick={() => setCustomOpen(true)}
                >
                  {t('Configure models and groups')}
                  <HugeiconsIcon
                    icon={ArrowRight01Icon}
                    size={16}
                    aria-hidden='true'
                  />
                </Button>
              </div>
            )}
          </>
        )}
        {dynamic && (
          <div className='space-y-2 border-t pt-3'>
            <p className='text-sm font-medium'>{t('Routing exclusions')}</p>
            <Button
              type='button'
              variant='outline'
              aria-label={t('Exclusion settings')}
              className='h-auto w-full justify-between rounded-2xl py-3 text-start whitespace-normal'
              onClick={() => setExclusionsOpen(true)}
            >
              <span>
                {exclusionCount
                  ? t('{{count}} exclusions set', {
                      count: formatNumber(
                        exclusionCount,
                        toIntlLocale(i18n.resolvedLanguage || i18n.language)
                      ),
                    })
                  : t(
                      'All available groups are used by default. Exclude any you do not want here.'
                    )}
              </span>
              <HugeiconsIcon
                icon={ArrowRight01Icon}
                size={16}
                aria-hidden='true'
              />
            </Button>
          </div>
        )}
      </div>
      {props.value && (
        <div className='space-y-3 border-t pt-3'>
          {!props.model && (
            <Input
              aria-label={t('Model for preview')}
              placeholder={t('Model for preview')}
              value={previewModel}
              onChange={(event) => {
                setPreviewModel(event.target.value)
                preview.reset()
              }}
              className='min-h-11'
            />
          )}
          <Button
            type='button'
            variant='outline'
            disabled={preview.isPending || !(props.model || previewModel)}
            onClick={runPreview}
          >
            {preview.isPending
              ? t('Loading...')
              : t('Preview routing and prices')}
          </Button>
          {preview.isError && previewIsCurrent && (
            <ErrorState
              description={t(
                'Preview failed. Check the model name and group settings, then retry.'
              )}
              onRetry={runPreview}
            />
          )}
          {preview.data && previewIsCurrent && (
            <div className='space-y-3' role='status'>
              <p className='text-sm break-words'>
                {routingReasonLabel(preview.data.reason, t)}
              </p>
              <p className='text-sm break-all'>
                {t('Group')}: {preview.data.selected_group || t('Unknown')}
              </p>
              {preview.data.items.length === 0 && (
                <EmptyState
                  title={t('No routing candidates')}
                  description={t(
                    'No matches found. Try another keyword or clear the filters.'
                  )}
                />
              )}
              {preview.data.items.map((candidate) => (
                <CandidateCard
                  key={`${candidate.model}:${candidate.group}`}
                  candidate={candidate}
                />
              ))}
            </div>
          )}
        </div>
      )}
      {customOpen && (
        <CustomRoutingDialog
          keyConfiguration={props.keyConfiguration}
          tokenId={props.tokenId}
          value={
            props.value?.mode === 'smart' && props.value.strategy === 'custom'
              ? props.value
              : customDraft
          }
          onClose={() => setCustomOpen(false)}
          onSave={(policy) => {
            setCustomDraft(policy)
            props.onChange(policy)
            setCustomOpen(false)
          }}
        />
      )}
      {exclusionsOpen && dynamic && (
        <DynamicExclusionsDialog
          value={dynamic}
          groups={props.groups}
          tokenId={props.tokenId}
          onClose={() => setExclusionsOpen(false)}
          onSave={(policy) => {
            setDynamicDraft(policy)
            props.onChange(policy)
            setExclusionsOpen(false)
          }}
        />
      )}
    </fieldset>
  )
}

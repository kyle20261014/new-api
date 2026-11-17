import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { getRoutingCandidates, previewRouting } from './api'
import { CandidateCard } from './candidate-card'
import { CustomRoutingDialog } from './custom-routing-dialog'
import { routingReasonLabel } from './labels'
import { defaultRouting, normalizeRouting } from './policy'
import { RoutingHelp } from './routing-help'
import { RoutingSelect } from './routing-select'
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
}) {
  const { t } = useTranslation()
  const [customOpen, setCustomOpen] = useState(false)
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
  const [excludedModel, setExcludedModel] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [previewModel, setPreviewModel] = useState(props.model ?? '')
  const dynamic =
    props.value?.mode === 'smart' && props.value.strategy !== 'custom'
      ? props.value
      : null
  const candidates = useQuery({
    queryKey: ['routing-exclusions', props.tokenId, search, page],
    queryFn: ({ signal }) =>
      getRoutingCandidates(
        { search, p: page, page_size: 100, token_id: props.tokenId },
        signal
      ),
    enabled: !!dynamic,
  })
  const preview = useMutation({
    mutationFn: (snapshot: {
      routing: RoutingPolicy
      model: string
      request?: Record<string, unknown>
    }) =>
      previewRouting(
        normalizeRouting(snapshot.routing),
        snapshot.model,
        snapshot.request,
        props.tokenId
      ),
    meta: { errorToast: false },
  })
  const previewIsCurrent =
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
      })
    }
  }
  const modelNames = [
    ...new Set((candidates.data?.items ?? []).map((item) => item.model)),
  ]
  const groupNames = [
    ...new Set([
      ...(props.groups ?? []),
      ...(candidates.data?.items ?? []).map((item) => item.group),
    ]),
  ].filter((group) => group !== 'auto' && group !== 'all')
  const updateDynamic = (patch: Partial<DynamicRouting>) => {
    if (dynamic) props.onChange({ ...dynamic, ...patch })
  }
  const strategyOptions = [
    { value: 'balanced', label: t('Balanced') },
    { value: 'price', label: t('Price first') },
    { value: 'speed', label: t('Speed first') },
    { value: 'success', label: t('Success first') },
    { value: 'custom', label: t('Custom per model') },
  ]
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

  return (
    <fieldset
      disabled={props.disabled}
      className='min-w-0 space-y-4 [&_button]:min-h-11'
    >
      <div className='flex flex-wrap items-end justify-between gap-2'>
        <div className='min-w-40 flex-1'>
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
        </div>
        <RoutingHelp />
      </div>
      {!props.value && (
        <p className='text-muted-foreground text-sm break-words'>
          {t(
            'This key keeps its original routing until you explicitly change it. Invalid or inherited values are preserved.'
          )}{' '}
          · {props.legacyGroup || t('Inherited group')}
        </p>
      )}
      {props.value?.mode === 'fixed' && (
        <RoutingSelect
          label={t('Fixed group')}
          value={props.value.fixed_group}
          options={[...new Set([props.value.fixed_group, ...groupNames])].map(
            (group) => ({ value: group, label: group || t('Inherited group') })
          )}
          onChange={(group) =>
            props.onChange({
              schema_version: 1,
              mode: 'fixed',
              fixed_group: group,
            })
          }
        />
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
          {props.value.strategy === 'custom' && (
            <div className='space-y-2'>
              <p className='text-muted-foreground text-sm break-words'>
                {Object.keys(props.value.models).join(' · ')}
              </p>
              <Button
                type='button'
                variant='outline'
                className='min-h-11'
                onClick={() => setCustomOpen(true)}
              >
                {t('Configure models and groups')}
              </Button>
            </div>
          )}
        </>
      )}
      {dynamic && (
        <details className='rounded-lg border p-3'>
          <summary className='min-h-11 cursor-pointer text-sm font-medium'>
            {t('Dynamic exclusions')}
          </summary>
          <div className='space-y-3'>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Newly authorized groups participate automatically unless excluded. Existing model, IP and quota limits still apply.'
              )}
            </p>
            <Input
              aria-label={t('Search model name')}
              placeholder={t('Search model name')}
              value={search}
              maxLength={128}
              className='min-h-11'
              onChange={(event) => {
                setSearch(event.target.value)
                setPage(1)
              }}
            />
            {candidates.isPending && <LoadingState />}
            {candidates.isSuccess && candidates.data.items.length === 0 && (
              <EmptyState
                title={t('No routing candidates')}
                description={t(
                  'Change your search or filters to find available groups.'
                )}
              />
            )}
            {candidates.isError && (
              <ErrorState
                description={t('Could not load routing candidates.')}
                onRetry={() => void candidates.refetch()}
              />
            )}
            <div>
              <p className='mb-1 text-sm'>{t('Excluded models')}</p>
              <MultiSelect
                options={modelNames.map((name) => ({
                  value: name,
                  label: name,
                }))}
                selected={dynamic.excluded_models ?? []}
                onChange={(excluded_models) =>
                  updateDynamic({ excluded_models })
                }
                placeholder={t('Excluded models')}
              />
            </div>
            <div>
              <p className='mb-1 text-sm'>{t('Excluded groups')}</p>
              <MultiSelect
                options={groupNames.map((name) => ({
                  value: name,
                  label: name,
                }))}
                selected={dynamic.excluded_groups ?? []}
                onChange={(excluded_groups) =>
                  updateDynamic({ excluded_groups })
                }
                placeholder={t('Excluded groups')}
              />
            </div>
            <RoutingSelect
              label={t('Model-specific exclusions')}
              value={excludedModel}
              options={[
                { value: '', label: t('Choose a model') },
                ...[
                  ...new Set([
                    ...modelNames,
                    ...Object.keys(dynamic.excluded_model_groups ?? {}),
                  ]),
                ].map((name) => ({ value: name, label: name })),
              ]}
              onChange={setExcludedModel}
            />
            {excludedModel && (
              <MultiSelect
                options={groupNames.map((name) => ({
                  value: name,
                  label: name,
                }))}
                selected={dynamic.excluded_model_groups?.[excludedModel] ?? []}
                onChange={(groups) =>
                  updateDynamic({
                    excluded_model_groups: {
                      ...dynamic.excluded_model_groups,
                      [excludedModel]: groups,
                    },
                  })
                }
                placeholder={t('Excluded groups')}
              />
            )}
            <div className='flex gap-2'>
              <Button
                type='button'
                variant='outline'
                className='min-h-11'
                disabled={page === 1 || candidates.isFetching}
                onClick={() => setPage((value) => value - 1)}
              >
                {t('Previous')}
              </Button>
              <Button
                type='button'
                variant='outline'
                className='min-h-11'
                disabled={
                  !candidates.data ||
                  page * candidates.data.page_size >= candidates.data.total ||
                  candidates.isFetching
                }
                onClick={() => setPage((value) => value + 1)}
              >
                {t('Next page')}
              </Button>
            </div>
          </div>
        </details>
      )}
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
            className='min-h-11'
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
                'Could not preview this routing draft. Check the model and selections.'
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
                    'Change your search or filters to find available groups.'
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
    </fieldset>
  )
}

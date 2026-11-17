import { useQuery } from '@tanstack/react-query'
import { Reorder } from 'motion/react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { AutoGroupOrderItem } from '@/components/auto-group-order-item'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

import { getRoutingCandidates, getSelectedRoutingCandidates } from './api'
import { CandidateCard } from './candidate-card'
import { moveGroup, routingIsValid, setModelGroups } from './policy'
import { RoutingSelect } from './routing-select'
import type {
  CustomRouting,
  ModelCategory,
  ModelRouting,
  RoutingHealth,
} from './types'

export function CustomRoutingDialog(props: {
  value: CustomRouting
  onSave: (value: CustomRouting) => void
  onClose: () => void
  tokenId?: number
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState(props.value)
  const [step, setStep] = useState(1)
  const [search, setSearch] = useState('')
  const [category, setCategory] = useState<ModelCategory>('all')
  const [model, setModel] = useState(Object.keys(props.value.models)[0] ?? '')
  const [health, setHealth] = useState<RoutingHealth | 'all'>('all')
  const [page, setPage] = useState(1)
  const selectedModels = Object.keys(draft.models).sort()
  const selectedQuery = useQuery({
    queryKey: ['routing-selected-candidates', props.tokenId, selectedModels],
    queryFn: ({ signal }) =>
      getSelectedRoutingCandidates(selectedModels, signal, props.tokenId),
    enabled: selectedModels.length > 0,
    staleTime: 30_000,
  })
  const query = useQuery({
    queryKey: [
      'routing-candidates',
      props.tokenId,
      search,
      category,
      model,
      page,
    ],
    queryFn: ({ signal }) =>
      getRoutingCandidates(
        {
          search,
          category,
          model: model || undefined,
          p: page,
          page_size: 100,
          token_id: props.tokenId,
        },
        signal
      ),
  })
  const items = query.data?.items ?? []
  const models = [
    ...new Set([
      ...Object.keys(draft.models),
      ...items.map((item) => item.model),
    ]),
  ]
  const currentItems = items.filter(
    (item) =>
      (!model || item.model === model) &&
      (health === 'all' || item.health === health)
  )
  const selected = draft.models[model]?.groups ?? []
  const invalidSelected =
    !!selectedQuery.data && !routingIsValid(draft, selectedQuery.data)
  const valid =
    routingIsValid(draft) &&
    !invalidSelected &&
    !selectedQuery.isPending &&
    !selectedQuery.isError
  const strategyOptions = [
    { value: 'balanced', label: t('Balanced') },
    { value: 'price', label: t('Price first') },
    { value: 'speed', label: t('Speed first') },
    { value: 'success', label: t('Success first') },
    { value: 'manual', label: t('Manual order') },
  ]
  const updateModel = (name: string, config: ModelRouting) =>
    setDraft((value) => ({
      ...value,
      models: { ...value.models, [name]: config },
    }))
  const changeGroups = (name: string, groups: string[]) =>
    setDraft((value) => setModelGroups(value, name, groups))

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Custom per model')}
      description={
        step === 1
          ? t('Step 1 · Choose models and groups')
          : t('Step 2 · Set scheduling per model')
      }
      contentClassName='sm:max-w-3xl'
      contentHeight='min(65dvh, 640px)'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            className='min-h-11'
            onClick={props.onClose}
          >
            {t('Cancel')}
          </Button>
          {step === 2 && (
            <Button
              type='button'
              variant='outline'
              className='min-h-11'
              onClick={() => setStep(1)}
            >
              {t('Back')}
            </Button>
          )}
          <Button
            type='button'
            className='min-h-11'
            disabled={!valid}
            onClick={() => {
              if (step === 1) setStep(2)
              else props.onSave(draft)
            }}
          >
            {step === 1 ? t('Next') : t('Apply to draft')}
          </Button>
        </>
      }
    >
      <div className='space-y-4 [&_button]:min-h-11'>
        {Object.keys(draft.models).length === 0 && (
          <p role='status' className='text-muted-foreground text-sm'>
            {t('Select at least one model and group to continue.')}
          </p>
        )}
        {invalidSelected && (
          <p role='alert' className='text-destructive text-sm'>
            {t(
              'Some selected groups are unavailable. Remove them before saving.'
            )}
          </p>
        )}
        {selectedModels.length > 0 && selectedQuery.isPending && (
          <LoadingState />
        )}
        {selectedQuery.isError && (
          <ErrorState
            description={t('Could not load routing candidates.')}
            onRetry={() => void selectedQuery.refetch()}
          />
        )}
        {step === 1 ? (
          <>
            <Input
              aria-label={t('Search model name')}
              placeholder={t('Search model name')}
              value={search}
              maxLength={128}
              className='min-h-11'
              onChange={(event) => {
                setSearch(event.target.value)
                setModel('')
                setPage(1)
              }}
            />
            <div className='grid gap-3 sm:grid-cols-2'>
              <RoutingSelect
                label={t('Model category')}
                value={category}
                options={[
                  { value: 'all', label: t('All') },
                  { value: 'text', label: t('Text') },
                  { value: 'image', label: t('Image') },
                  { value: 'video', label: t('Video') },
                  { value: 'audio', label: t('Audio') },
                ]}
                onChange={(value) => {
                  setCategory(value as ModelCategory)
                  setModel('')
                  setPage(1)
                }}
              />
              <RoutingSelect
                label={t('Health filter')}
                value={health}
                options={[
                  { value: 'all', label: t('All') },
                  { value: 'normal', label: t('Healthy') },
                  { value: 'congested', label: t('Congested') },
                  { value: 'soft_offline', label: t('Temporarily offline') },
                  { value: 'unknown', label: t('Unknown') },
                  { value: 'degraded', label: t('Degraded') },
                ]}
                onChange={(value) => setHealth(value as RoutingHealth | 'all')}
              />
            </div>
            <div className='flex flex-wrap gap-2' aria-label={t('Models')}>
              {models.map((name) => (
                <Button
                  key={name}
                  type='button'
                  variant={model === name ? 'secondary' : 'outline'}
                  className='h-auto min-h-11 max-w-full break-all whitespace-normal'
                  aria-pressed={model === name}
                  onClick={() => {
                    setModel(name)
                    setPage(1)
                  }}
                >
                  {name}
                  {draft.models[name] &&
                    ` (${draft.models[name].groups.length})`}
                </Button>
              ))}
            </div>
            {model && (
              <div className='flex flex-wrap gap-2'>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() =>
                    changeGroups(model, [
                      ...new Set([
                        ...selected,
                        ...currentItems
                          .filter(
                            (item) => item.available && item.health === 'normal'
                          )
                          .map((item) => item.group),
                      ]),
                    ])
                  }
                >
                  {t('Select healthy on this page')}
                </Button>
                <Button
                  type='button'
                  variant='ghost'
                  disabled={!selected.length}
                  onClick={() => changeGroups(model, [])}
                >
                  {t('Clear current model')}
                </Button>
              </div>
            )}
            {query.isPending && <LoadingState />}
            {query.isError && (
              <ErrorState
                description={t('Could not load routing candidates.')}
                onRetry={() => void query.refetch()}
              />
            )}
            {!query.isPending &&
              !query.isError &&
              currentItems.length === 0 && (
                <EmptyState
                  title={t('No routing candidates')}
                  description={t(
                    'Change your search or filters to find available groups.'
                  )}
                />
              )}
            <div className='grid gap-3 sm:grid-cols-2'>
              {currentItems.map((candidate) => (
                <CandidateCard
                  key={`${candidate.model}:${candidate.group}`}
                  candidate={candidate}
                  selected={
                    !!draft.models[candidate.model]?.groups.includes(
                      candidate.group
                    )
                  }
                  onSelect={(checked) => {
                    setModel(candidate.model)
                    const groups = draft.models[candidate.model]?.groups ?? []
                    changeGroups(
                      candidate.model,
                      checked
                        ? [...new Set([...groups, candidate.group])]
                        : groups.filter((group) => group !== candidate.group)
                    )
                  }}
                />
              ))}
            </div>
            <div className='flex items-center justify-between gap-3'>
              <Button
                type='button'
                variant='outline'
                disabled={page === 1 || query.isFetching}
                onClick={() => setPage((value) => value - 1)}
              >
                {t('Previous')}
              </Button>
              <Button
                type='button'
                variant='outline'
                disabled={
                  !query.data ||
                  query.data.page * query.data.page_size >= query.data.total ||
                  query.isFetching
                }
                onClick={() => setPage((value) => value + 1)}
              >
                {t('Next page')}
              </Button>
            </div>
            <section className='space-y-2'>
              <h3 className='text-sm font-medium'>
                {t('Selected models and groups')}
              </h3>
              {Object.entries(draft.models).map(([name, config]) => (
                <div key={name} className='rounded-lg border p-3'>
                  <p className='text-sm font-medium break-all'>{name}</p>
                  <div className='mt-2 flex flex-wrap gap-2'>
                    {config.groups.map((group) => (
                      <Button
                        type='button'
                        variant='outline'
                        key={group}
                        className='h-auto break-all whitespace-normal'
                        aria-label={t('Remove {{model}} / {{group}}', {
                          model: name,
                          group,
                        })}
                        onClick={() =>
                          changeGroups(
                            name,
                            config.groups.filter((value) => value !== group)
                          )
                        }
                      >
                        {group}
                        {selectedQuery.data &&
                          !selectedQuery.data.some(
                            (item) =>
                              item.model === name &&
                              item.group === group &&
                              item.available
                          ) &&
                          ` · ${t('Unavailable')}`}{' '}
                        ×
                      </Button>
                    ))}
                  </div>
                </div>
              ))}
            </section>
          </>
        ) : (
          <>
            <RoutingSelect
              label={t('Apply scheduling to all models')}
              value=''
              options={[
                { value: '', label: t('Choose a strategy') },
                ...strategyOptions,
              ]}
              onChange={(value) => {
                if (value) {
                  setDraft((current) => ({
                    ...current,
                    models: Object.fromEntries(
                      Object.entries(current.models).map(([name, config]) => [
                        name,
                        {
                          ...config,
                          strategy: value as ModelRouting['strategy'],
                        },
                      ])
                    ),
                  }))
                }
              }}
            />
            {Object.entries(draft.models).map(([name, config]) => (
              <section key={name} className='space-y-3 rounded-lg border p-3'>
                <h3 className='text-sm font-medium break-all'>{name}</h3>
                <RoutingSelect
                  label={t('Scheduling for {{model}}', { model: name })}
                  value={config.strategy}
                  options={strategyOptions}
                  onChange={(value) =>
                    updateModel(name, {
                      ...config,
                      strategy: value as ModelRouting['strategy'],
                    })
                  }
                />
                <label className='flex min-h-11 items-center justify-between gap-3 text-sm'>
                  <span>
                    {t('Try temporarily offline groups')}
                    <span className='text-muted-foreground block text-xs'>
                      {t(
                        'Only within this model’s selection. Disabled channels and permissions still apply.'
                      )}
                    </span>
                  </span>
                  <Switch
                    aria-label={t(
                      'Try temporarily offline groups for {{model}}',
                      { model: name }
                    )}
                    checked={config.try_soft_offline}
                    onCheckedChange={(checked) =>
                      updateModel(name, {
                        ...config,
                        try_soft_offline: checked,
                      })
                    }
                  />
                </label>
                {config.strategy === 'manual' ? (
                  <Reorder.Group
                    axis='y'
                    values={config.groups}
                    onReorder={(groups) =>
                      updateModel(name, { ...config, groups })
                    }
                    className='space-y-2 [&_button]:min-w-11'
                  >
                    {config.groups.map((group, index) => (
                      <AutoGroupOrderItem
                        key={group}
                        group={group}
                        index={index}
                        count={config.groups.length}
                        onMove={(index, direction) =>
                          updateModel(name, moveGroup(config, index, direction))
                        }
                        onRemove={(group) =>
                          changeGroups(
                            name,
                            config.groups.filter((value) => value !== group)
                          )
                        }
                      />
                    ))}
                  </Reorder.Group>
                ) : (
                  <p className='text-muted-foreground text-xs break-all'>
                    {config.groups.join(' · ')}
                  </p>
                )}
              </section>
            ))}
          </>
        )}
      </div>
    </Dialog>
  )
}

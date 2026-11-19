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
import { Switch } from '@/components/ui/switch'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { getRoutingCandidates, getSelectedRoutingCandidates } from './api'
import { CandidateCard } from './candidate-card'
import { CandidateFilters, CandidatePagination } from './candidate-filters'
import { moveGroup, routingIsValid, setModelGroups } from './policy'
import { RoutingSelect } from './routing-select'
import { useRoutingStrategies } from './routing-strategies'
import { StrategyBadge } from './strategy-badge'
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
  keyConfiguration?: boolean
}) {
  const { t, i18n } = useTranslation()
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
    enabled: step === 1,
  })
  const items = query.data?.items ?? []
  const models = [
    ...new Set([model, ...items.map((item) => item.model)]),
  ].filter(Boolean)
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
  const dynamicOptions = useRoutingStrategies()
    .filter((item) => item.value !== 'custom')
    .map((item) => ({
      ...item,
      badge: <StrategyBadge strategy={item.value} />,
    }))
  const strategyOptions = [
    ...dynamicOptions,
    { value: 'manual', label: t('Manual order') },
  ]
  const number = (value: number) =>
    formatNumber(value, toIntlLocale(i18n.resolvedLanguage || i18n.language))
  const selectedCount = Object.values(draft.models).reduce(
    (sum, config) => sum + config.groups.length,
    0
  )
  const updateModel = (name: string, config: ModelRouting) =>
    setDraft((value) => ({
      ...value,
      models: { ...value.models, [name]: config },
    }))
  const changeGroups = (name: string, groups: string[]) =>
    setDraft((value) => setModelGroups(value, name, groups))
  const resetFilters = () => {
    setModel('')
    setPage(1)
  }
  const filtered =
    !!search || category !== 'all' || !!model || health !== 'all' || page > 1
  const clearFilters = () => {
    setSearch('')
    setCategory('all')
    setHealth('all')
    resetFilters()
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Custom per model')}
      description={
        step === 1
          ? t('Step 1: Choose models and groups')
          : t('Step 2: Choose how to use them')
      }
      contentClassName='rounded-3xl sm:max-w-[min(92vw,1360px)] [&_[data-slot=button]]:min-h-11'
      contentHeight='min(66dvh, 720px)'
      footerClassName='flex-row flex-wrap items-center'
      footer={
        <>
          <p
            role='status'
            className='text-muted-foreground mr-auto w-full text-xs sm:w-auto'
          >
            {t('{{models}} models · {{groups}} selected groups', {
              models: number(selectedModels.length),
              groups: number(selectedCount),
            })}
          </p>
          <Button type='button' variant='outline' onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          {step === 2 && (
            <Button type='button' variant='outline' onClick={() => setStep(1)}>
              {t('Previous step')}
            </Button>
          )}
          <Button
            type='button'
            disabled={!valid}
            onClick={() => {
              if (step === 1) setStep(2)
              else props.onSave(draft)
            }}
          >
            {step === 1 ? t('Next') : t('Use this configuration')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        <p className='text-muted-foreground text-sm leading-relaxed'>
          {t('Only models and groups selected here will be used.')}
        </p>
        {props.keyConfiguration && (
          <p className='text-muted-foreground text-xs leading-relaxed'>
            {t(
              'After applying, save the Key for these settings to take effect.'
            )}
          </p>
        )}
        {!selectedModels.length && (
          <p role='status' className='text-muted-foreground text-sm'>
            {t('Choose at least one model and its groups.')}
          </p>
        )}
        {invalidSelected && (
          <p role='alert' className='text-destructive text-sm'>
            {t(
              'Some selected groups are no longer available. Remove them to continue.'
            )}
          </p>
        )}
        {selectedModels.length > 0 && selectedQuery.isPending && (
          <LoadingState />
        )}
        {selectedQuery.isError && (
          <ErrorState
            description={t('Loading failed. Please retry.')}
            onRetry={() => void selectedQuery.refetch()}
          />
        )}
        {step === 1 && (
          <CandidateFilters
            search={search}
            category={category}
            model={model}
            models={models}
            onSearch={(value) => {
              setSearch(value)
              resetFilters()
            }}
            onCategory={(value) => {
              setCategory(value)
              resetFilters()
            }}
            onModel={(value) => {
              setModel(value)
              setPage(1)
            }}
          />
        )}
        <div className='grid min-w-0 gap-5 lg:grid-cols-[minmax(0,1fr)_280px]'>
          <section
            className='min-w-0 space-y-4'
            aria-label={
              step === 1
                ? t('Routing candidates')
                : t('Step 2: Choose how to use them')
            }
          >
            {step === 1 ? (
              <>
                <div className='flex flex-wrap items-end justify-between gap-3'>
                  <div className='w-full sm:w-52'>
                    <RoutingSelect
                      label={t('Health filter')}
                      value={health}
                      options={[
                        { value: 'all', label: t('All') },
                        { value: 'normal', label: t('Healthy') },
                        { value: 'congested', label: t('Congested') },
                        {
                          value: 'soft_offline',
                          label: t('Temporarily offline'),
                        },
                        { value: 'unknown', label: t('Unknown') },
                        { value: 'degraded', label: t('Degraded') },
                      ]}
                      onChange={(value) =>
                        setHealth(value as RoutingHealth | 'all')
                      }
                    />
                  </div>
                  {model && (
                    <div className='flex flex-wrap gap-2'>
                      <Button
                        type='button'
                        variant='outline'
                        disabled={
                          query.isFetching ||
                          !currentItems.some(
                            (item) => item.available && item.health === 'normal'
                          )
                        }
                        onClick={() =>
                          changeGroups(model, [
                            ...new Set([
                              ...selected,
                              ...currentItems
                                .filter(
                                  (item) =>
                                    item.available && item.health === 'normal'
                                )
                                .map((item) => item.group),
                            ]),
                          ])
                        }
                      >
                        {t('Select healthy groups on this page')}
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
                </div>
                {query.isPending && <LoadingState />}
                {query.isError && (
                  <ErrorState
                    description={t('Loading failed. Please retry.')}
                    onRetry={() => void query.refetch()}
                  />
                )}
                {query.isSuccess && !currentItems.length && (
                  <EmptyState
                    title={
                      filtered
                        ? t('No matching candidates')
                        : t('No routing candidates')
                    }
                    description={
                      filtered
                        ? t(
                            'No matches found. Try another keyword or clear the filters.'
                          )
                        : t(
                            'No groups are available for this account or Key. Check access, then refresh.'
                          )
                    }
                    action={
                      <Button
                        type='button'
                        variant='outline'
                        onClick={
                          filtered ? clearFilters : () => void query.refetch()
                        }
                      >
                        {filtered ? t('Clear filters') : t('Reload candidates')}
                      </Button>
                    }
                  />
                )}
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
                <CandidatePagination
                  page={page}
                  hasNext={
                    !!query.data &&
                    page * query.data.page_size < query.data.total
                  }
                  fetching={query.isFetching}
                  onPage={setPage}
                />
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
                          Object.entries(current.models).map(
                            ([name, config]) => [
                              name,
                              {
                                ...config,
                                strategy: value as ModelRouting['strategy'],
                              },
                            ]
                          )
                        ),
                      }))
                    }
                  }}
                />
                {Object.entries(draft.models).map(([name, config]) => (
                  <section
                    key={name}
                    className='space-y-3 rounded-2xl border p-4'
                  >
                    <h3 className='font-semibold break-all'>{name}</h3>
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
                    <div
                      className='text-muted-foreground space-y-1 text-xs leading-relaxed'
                      role='status'
                    >
                      {config.strategy === 'manual' ? (
                        <p>
                          {t(
                            'Tries groups from top to bottom, skipping unavailable ones. Drag or use the up and down buttons to reorder.'
                          )}
                        </p>
                      ) : (
                        <>
                          <p>
                            {t(
                              'Automatically chooses among this model’s selected groups using the chosen strategy.'
                            )}
                          </p>
                          <p>
                            {
                              dynamicOptions.find(
                                (item) => item.value === config.strategy
                              )?.description
                            }
                          </p>
                        </>
                      )}
                    </div>
                    <div className='flex min-h-11 items-center justify-between gap-3 text-sm'>
                      <span>
                        {t('Include selected temporarily offline groups')}
                        <span className='text-muted-foreground block text-xs'>
                          {t(
                            'Also tries selected groups that are temporarily offline. Disabled channels and channels you cannot access are still excluded.'
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
                    </div>
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
                              updateModel(
                                name,
                                moveGroup(config, index, direction)
                              )
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
          </section>
          <aside
            className='min-w-0 space-y-3 border-t pt-4 lg:sticky lg:top-0 lg:max-h-[60dvh] lg:overflow-y-auto lg:border-t-0 lg:border-l lg:pt-0 lg:pl-5'
            aria-label={t('Selected models and groups')}
          >
            <div className='flex items-center justify-between gap-2'>
              <h3 className='text-sm font-semibold'>
                {t('Selected models and groups')}
              </h3>
              <Button
                type='button'
                size='sm'
                variant='ghost'
                disabled={!selectedModels.length}
                onClick={() => setDraft({ ...draft, models: {} })}
              >
                {t('Clear all')}
              </Button>
            </div>
            {!selectedModels.length && (
              <p className='text-muted-foreground py-6 text-center text-sm'>
                {t('Choose at least one model and its groups.')}
              </p>
            )}
            {Object.entries(draft.models).map(([name, config]) => (
              <section key={name} className='space-y-2 border-b pb-3'>
                <div className='flex items-start justify-between gap-2'>
                  <h4 className='pt-3 text-sm font-medium break-all'>{name}</h4>
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    aria-label={t('Remove model {{model}}', { model: name })}
                    onClick={() => changeGroups(name, [])}
                  >
                    ×
                  </Button>
                </div>
                {config.groups.length === 1 && (
                  <p className='text-xs leading-relaxed text-amber-800 dark:text-amber-300'>
                    {t(
                      'Only one group is selected. If it fails, there is no other selected group to try.'
                    )}
                  </p>
                )}
                {config.groups.map((group) => (
                  <Button
                    type='button'
                    variant='outline'
                    key={group}
                    className='h-auto w-full justify-between break-all whitespace-normal'
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
                    <span>
                      {group}
                      {selectedQuery.data &&
                        !selectedQuery.data.some(
                          (item) =>
                            item.model === name &&
                            item.group === group &&
                            item.available
                        ) &&
                        ` · ${t('Unavailable')}`}
                    </span>
                    <span aria-hidden='true'>×</span>
                  </Button>
                ))}
              </section>
            ))}
            <p className='text-muted-foreground text-xs leading-relaxed'>
              {t('Cancel discards changes made in this window.')}
            </p>
          </aside>
        </div>
      </div>
    </Dialog>
  )
}

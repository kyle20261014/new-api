import { CheckListIcon, Key01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery } from '@tanstack/react-query'
import { SlidersHorizontalIcon } from 'lucide-react'
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
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

import {
  getRoutingCandidates,
  getRoutingModels,
  getSelectedRoutingCandidates,
} from './api'
import { CandidateCard } from './candidate-card'
import { CandidatePagination } from './candidate-filters'
import { moveGroup, routingIsValid, setModelGroups } from './policy'
import { RoutingSelect } from './routing-select'
import { useRoutingStrategies } from './routing-strategies'
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
  const strategy = Object.values(draft.models)[0]?.strategy ?? 'balanced'
  const selectedModels = Object.keys(draft.models).sort()
  const modelQuery = useQuery({
    queryKey: ['routing-models', props.tokenId],
    queryFn: ({ signal }) => getRoutingModels(props.tokenId, signal),
    staleTime: 30_000,
  })
  const models = (modelQuery.data ?? []).filter(
    (item) =>
      (category === 'all' || item.category === category) &&
      (!search ||
        `${item.model} ${item.display_name}`
          .toLowerCase()
          .includes(search.trim().toLowerCase()))
  )
  const active = models.find((item) => item.model === model) ?? models[0]
  const activeModel = active?.model ?? ''
  const selectedQuery = useQuery({
    queryKey: ['routing-selected-candidates', props.tokenId, selectedModels],
    queryFn: ({ signal }) =>
      getSelectedRoutingCandidates(selectedModels, signal, props.tokenId),
    enabled: selectedModels.length > 0,
    staleTime: 30_000,
  })
  const query = useQuery({
    queryKey: ['routing-candidates', props.tokenId, activeModel, page],
    queryFn: ({ signal }) =>
      getRoutingCandidates(
        {
          model: activeModel,
          p: page,
          page_size: 100,
          token_id: props.tokenId,
        },
        signal
      ),
    enabled: step === 1 && !!activeModel,
  })
  const items = (query.data?.items ?? []).filter(
    (item) =>
      item.model === activeModel && (health === 'all' || item.health === health)
  )
  const selected = draft.models[activeModel]?.groups ?? []
  const invalidSelected =
    !!selectedQuery.data && !routingIsValid(draft, selectedQuery.data)
  const valid =
    routingIsValid(draft) &&
    !invalidSelected &&
    !selectedQuery.isPending &&
    !selectedQuery.isError
  const strategies = [
    ...useRoutingStrategies().filter((item) => item.value !== 'custom'),
    {
      value: 'manual',
      label: t('Manual order'),
      icon: CheckListIcon,
      description: t(
        'Tries groups from top to bottom, skipping unavailable ones. Drag or use the up and down buttons to reorder.'
      ),
    },
  ]
  const schedulingOrder = ['balanced', 'success', 'speed', 'price', 'manual']
  strategies.sort(
    (left, right) =>
      schedulingOrder.indexOf(left.value) - schedulingOrder.indexOf(right.value)
  )
  const number = (value: number) =>
    formatNumber(value, toIntlLocale(i18n.resolvedLanguage || i18n.language))
  const selectedCount = Object.values(draft.models).reduce(
    (sum, config) => sum + config.groups.length,
    0
  )
  const categories: Array<{ value: ModelCategory; label: string }> = [
    { value: 'all', label: t('All') },
    { value: 'text', label: t('Text') },
    { value: 'image', label: t('Image') },
    { value: 'video', label: t('Video') },
    { value: 'audio', label: t('Audio') },
  ]
  const updateModel = (name: string, config: ModelRouting) =>
    setDraft((value) => ({
      ...value,
      models: { ...value.models, [name]: config },
    }))
  const changeGroups = (name: string, groups: string[]) =>
    setDraft((value) => setModelGroups(value, name, groups))
  const changeStrategy = (strategy: ModelRouting['strategy']) =>
    setDraft((value) => ({
      ...value,
      models: Object.fromEntries(
        Object.entries(value.models).map(([name, config]) => [
          name,
          { ...config, strategy },
        ])
      ),
    }))
  const preference = (name: string, group: string, config: ModelRouting) => (
    <Switch
      checked={config.try_soft_offline_groups.includes(group)}
      aria-label={t('Try {{model}} / {{group}} while temporarily offline', {
        model: name,
        group,
      })}
      className='after:-inset-y-3.5'
      onCheckedChange={(checked) =>
        updateModel(name, {
          ...config,
          try_soft_offline_groups: checked
            ? [...config.try_soft_offline_groups, group]
            : config.try_soft_offline_groups.filter((value) => value !== group),
        })
      }
    />
  )
  const unavailable = (name: string, group: string) =>
    selectedQuery.isSuccess &&
    !selectedQuery.data.some(
      (item) => item.model === name && item.group === group && item.available
    )
  const healthLabel = (name: string, group: string) => {
    const state = selectedQuery.data?.find(
      (item) => item.model === name && item.group === group
    )?.health
    if (state === 'normal') return t('Healthy')
    if (state === 'soft_offline') return t('Temporarily offline')
    if (state === 'congested') return t('Congested')
    if (state === 'degraded') return t('Degraded')
    return t('Unknown')
  }
  const clearFilters = () => {
    setSearch('')
    setCategory('all')
    setHealth('all')
    setPage(1)
  }
  const icon = (name: string) =>
    getLobeIcon(
      modelQuery.data?.find((item) => item.model === name)?.icon ?? '',
      20
    )
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={
        <span className='relative block pl-12'>
          <span
            className='bg-primary/10 text-primary absolute top-0 left-0 flex size-9 items-center justify-center rounded-full'
            aria-hidden='true'
          >
            {step === 1 ? (
              <HugeiconsIcon icon={Key01Icon} size={18} />
            ) : (
              <SlidersHorizontalIcon size={18} />
            )}
          </span>
          {step === 1
            ? t('Custom model groups')
            : t('Scheduling configuration')}
        </span>
      }
      description={
        <span className='block pl-12'>
          {step === 1
            ? t('Step 1: Choose models and groups')
            : t(
                'Selected {{groups}} groups ({{models}} models). Choose how calls select among them.',
                {
                  models: number(selectedModels.length),
                  groups: number(selectedCount),
                }
              )}
        </span>
      }
      contentClassName={cn(
        'gap-0 overflow-hidden rounded-xl p-0 sm:p-0 [&>div:nth-child(2)]:mx-0 [&>div:nth-child(2)]:flex-1 [&>div:nth-child(2)]:overflow-hidden [&_[data-slot=button]]:min-h-11 [&_[data-slot=button]]:min-w-11 [&_[data-slot=dialog-close]]:size-11',
        step === 1
          ? 'h-[min(777px,calc(100dvh-2rem))] sm:max-w-[min(1280px,calc(100vw-2rem))]'
          : 'h-[min(606px,calc(100dvh-2rem))] sm:max-w-[656px]'
      )}
      headerClassName='border-b px-5 py-2 pr-14'
      contentHeight='100%'
      bodyClassName='h-full px-0 py-0'
      footerClassName='mx-0 mb-0 flex-row flex-wrap items-center border-t p-2 sm:mx-0 sm:mb-0 sm:p-2'
      footer={
        <>
          {step === 1 && (
            <>
              <p
                role='status'
                className='text-muted-foreground mr-auto text-xs'
              >
                {t('{{models}} models · {{groups}} selected groups', {
                  models: number(selectedModels.length),
                  groups: number(selectedCount),
                })}
              </p>
              <Button type='button' variant='outline' onClick={props.onClose}>
                {t('Cancel')}
              </Button>
            </>
          )}
          {step === 2 && (
            <Button type='button' variant='outline' onClick={() => setStep(1)}>
              {t('Previous step')}
            </Button>
          )}
          <Button
            type='button'
            disabled={!valid}
            onClick={() => {
              if (!valid) return
              if (step === 1) setStep(2)
              else props.onSave(draft)
            }}
          >
            {step === 1
              ? t('Next: Scheduling configuration')
              : t('Use this configuration')}
          </Button>
        </>
      }
    >
      <div className='flex h-full min-h-0 flex-col'>
        {(step === 1 || props.keyConfiguration) && (
          <div className='text-muted-foreground shrink-0 space-y-1 border-b px-4 py-2 text-xs'>
            {step === 1 && (
              <p>{t('Only models and groups selected here will be used.')}</p>
            )}
            {props.keyConfiguration && (
              <p>
                {t(
                  'After applying, save the Key for these settings to take effect.'
                )}
              </p>
            )}
            {invalidSelected && (
              <p role='alert' className='text-destructive'>
                {t(
                  'Some selected groups are no longer available. Remove them to continue.'
                )}
              </p>
            )}
            {selectedModels.length > 0 && selectedQuery.isPending && (
              <LoadingState inline size='sm' />
            )}
            {selectedQuery.isError && (
              <ErrorState
                description={t('Loading failed. Please retry.')}
                onRetry={() => void selectedQuery.refetch()}
              />
            )}
          </div>
        )}
        {step === 1 ? (
          <div className='flex min-h-0 flex-1 flex-col overflow-y-auto lg:grid lg:grid-cols-[288px_minmax(0,1fr)_280px] lg:overflow-hidden [&>*]:shrink-0'>
            <nav
              aria-label={t('Models')}
              className='flex min-h-0 min-w-0 flex-col border-b lg:border-r lg:border-b-0'
            >
              <div className='space-y-2 border-b p-3'>
                <Input
                  aria-label={t('Search model name')}
                  placeholder={t('Search model name')}
                  maxLength={128}
                  value={search}
                  className='min-h-11'
                  onChange={(event) => {
                    setSearch(event.target.value)
                    setPage(1)
                  }}
                />
                <div
                  role='group'
                  aria-label={t('Model category')}
                  className='flex flex-wrap'
                >
                  {categories.map((item) => (
                    <Button
                      key={item.value}
                      type='button'
                      size='sm'
                      variant={category === item.value ? 'secondary' : 'ghost'}
                      aria-pressed={category === item.value}
                      onClick={() => {
                        setCategory(item.value)
                        setPage(1)
                      }}
                      className='px-2 text-xs'
                    >
                      {item.label}
                    </Button>
                  ))}
                </div>
              </div>
              <div className='max-h-56 space-y-1 overflow-y-auto p-2 lg:max-h-none lg:flex-1'>
                {modelQuery.isPending && <LoadingState />}
                {modelQuery.isError && (
                  <ErrorState
                    description={t('Loading failed. Please retry.')}
                    onRetry={() => void modelQuery.refetch()}
                  />
                )}
                {modelQuery.isSuccess && !models.length && (
                  <EmptyState
                    title={
                      search || category !== 'all'
                        ? t('No models match the selected filters')
                        : t('No routing candidates')
                    }
                    description={t(
                      'No groups are available for this account or Key. Check access, then refresh.'
                    )}
                    action={
                      <Button
                        onClick={
                          search || category !== 'all'
                            ? clearFilters
                            : () => void modelQuery.refetch()
                        }
                      >
                        {search || category !== 'all'
                          ? t('Clear filters')
                          : t('Reload candidates')}
                      </Button>
                    }
                  />
                )}
                {models.map((item) => (
                  <Button
                    key={item.model}
                    type='button'
                    variant={activeModel === item.model ? 'secondary' : 'ghost'}
                    aria-label={item.model}
                    aria-pressed={activeModel === item.model}
                    className='h-auto w-full justify-start gap-2 px-2 py-2 text-start whitespace-normal'
                    onClick={() => {
                      setModel(item.model)
                      setPage(1)
                    }}
                  >
                    <span aria-hidden='true' className='shrink-0'>
                      {getLobeIcon(item.icon ?? '', 24)}
                    </span>
                    <span className='min-w-0 flex-1'>
                      <span className='block truncate' title={item.model}>
                        {item.display_name || item.model}
                      </span>
                      <span className='text-muted-foreground text-xs font-normal'>
                        {
                          categories.find(
                            (entry) => entry.value === item.category
                          )?.label
                        }{' '}
                        · {t('Groups')}: {number(item.group_count)}
                      </span>
                    </span>
                    {!!draft.models[item.model]?.groups.length && (
                      <span className='text-xs'>
                        {number(draft.models[item.model].groups.length)}
                      </span>
                    )}
                  </Button>
                ))}
              </div>
            </nav>
            <section
              aria-label={t('Routing candidates')}
              className='flex min-h-0 min-w-0 flex-col'
            >
              <div className='flex shrink-0 flex-wrap items-center gap-2 border-b p-3'>
                <h3 className='min-w-0 flex-1 truncate text-sm font-semibold'>
                  {active?.display_name || activeModel || t('Choose a model')}
                </h3>
                <Button
                  type='button'
                  size='sm'
                  variant='ghost'
                  disabled={!selected.length}
                  onClick={() => changeGroups(activeModel, [])}
                >
                  {t('Clear current model')}
                </Button>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={
                    query.isFetching ||
                    !items.some(
                      (item) => item.available && item.health === 'normal'
                    )
                  }
                  onClick={() =>
                    changeGroups(activeModel, [
                      ...new Set([
                        ...selected,
                        ...items
                          .filter(
                            (item) => item.available && item.health === 'normal'
                          )
                          .map((item) => item.group),
                      ]),
                    ])
                  }
                >
                  {t('Select healthy groups on this page')}
                </Button>
                <div className='w-full'>
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
              </div>
              <div className='min-h-0 space-y-3 p-3 lg:flex-1 lg:overflow-y-auto'>
                {!!activeModel && query.isPending && <LoadingState />}
                {!!activeModel && query.isError && (
                  <ErrorState
                    description={t('Loading failed. Please retry.')}
                    onRetry={() => void query.refetch()}
                  />
                )}
                {!!activeModel && query.isSuccess && !items.length && (
                  <EmptyState
                    title={
                      search || category !== 'all' || health !== 'all'
                        ? t('No matching candidates')
                        : t('No routing candidates')
                    }
                    description={t(
                      'No matches found. Try another keyword or clear the filters.'
                    )}
                    action={
                      <Button variant='outline' onClick={clearFilters}>
                        {t('Clear filters')}
                      </Button>
                    }
                  />
                )}
                {items.map((candidate) => (
                  <CandidateCard
                    key={`${candidate.model}:${candidate.group}`}
                    candidate={candidate}
                    selected={selected.includes(candidate.group)}
                    onSelect={(checked) =>
                      changeGroups(
                        candidate.model,
                        checked
                          ? [...new Set([...selected, candidate.group])]
                          : selected.filter(
                              (group) => group !== candidate.group
                            )
                      )
                    }
                  />
                ))}
                {!!activeModel && query.isSuccess && (
                  <CandidatePagination
                    page={page}
                    hasNext={page * query.data.page_size < query.data.total}
                    fetching={query.isFetching}
                    onPage={setPage}
                  />
                )}
              </div>
            </section>
            <aside
              aria-label={t('Selected models and groups')}
              className='flex min-h-0 min-w-0 flex-col border-t lg:border-t-0 lg:border-l'
            >
              <div className='flex shrink-0 items-center justify-between border-b px-3 py-2'>
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
              <div className='min-h-0 space-y-3 p-3 lg:flex-1 lg:overflow-y-auto'>
                {!selectedModels.length && (
                  <p className='text-muted-foreground py-6 text-center text-xs'>
                    {t('Choose at least one model and its groups.')}
                  </p>
                )}
                {Object.entries(draft.models).map(([name, config]) => (
                  <section key={name} className='space-y-1 border-b pb-2'>
                    <div className='flex items-center gap-2'>
                      <span aria-hidden='true'>{icon(name)}</span>
                      <h4
                        className='min-w-0 flex-1 truncate text-sm font-medium'
                        title={name}
                      >
                        {name}
                      </h4>
                      <Button
                        type='button'
                        size='icon-sm'
                        variant='ghost'
                        aria-label={t('Remove model {{model}}', {
                          model: name,
                        })}
                        onClick={() => changeGroups(name, [])}
                      >
                        ×
                      </Button>
                    </div>
                    {config.groups.length === 1 && (
                      <p className='text-muted-foreground text-xs'>
                        {t(
                          'Only one group is selected. If it fails, there is no other selected group to try.'
                        )}
                      </p>
                    )}
                    {config.groups.map((group) => (
                      <div key={group} className='rounded border px-2'>
                        <div className='flex min-h-11 items-center justify-between gap-2'>
                          <span
                            className={cn(
                              'min-w-0 text-xs break-all',
                              unavailable(name, group) &&
                                'text-muted-foreground line-through'
                            )}
                          >
                            {group}
                          </span>
                          <Button
                            type='button'
                            size='icon-sm'
                            variant='ghost'
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
                            {unavailable(name, group) ? t('Unavailable') : '×'}
                          </Button>
                        </div>
                        <div className='text-muted-foreground flex min-h-11 items-center justify-between gap-2 text-[11px]'>
                          <span
                            title={t(
                              'Also tries selected groups that are temporarily offline. Disabled channels and channels you cannot access are still excluded.'
                            )}
                          >
                            {t('Try while temporarily offline')}
                          </span>
                          {preference(name, group, config)}
                        </div>
                      </div>
                    ))}
                  </section>
                ))}
              </div>
            </aside>
          </div>
        ) : (
          <section
            aria-label={t('Step 2: Choose how to use them')}
            className='min-h-0 flex-1 space-y-3 overflow-y-auto p-4'
          >
            <h3 className='text-xs font-semibold'>{t('Scheduling method')}</h3>
            <div
              role='group'
              aria-label={t('Apply scheduling to all models')}
              className='bg-muted/30 flex flex-wrap gap-1 rounded-2xl border p-1 sm:rounded-full'
            >
              {strategies.map((option) => (
                <Button
                  key={option.value}
                  type='button'
                  size='sm'
                  className='h-auto min-w-0 flex-auto gap-1 rounded-full px-2 py-1.5 text-xs whitespace-normal'
                  variant={strategy === option.value ? 'default' : 'ghost'}
                  aria-label={option.label}
                  aria-description={
                    option.value === 'balanced' ? t('Recommended') : undefined
                  }
                  aria-pressed={strategy === option.value}
                  onClick={() =>
                    changeStrategy(option.value as ModelRouting['strategy'])
                  }
                >
                  <HugeiconsIcon
                    icon={option.icon}
                    size={14}
                    aria-hidden='true'
                  />
                  {option.label}
                  {option.value === 'balanced' && (
                    <span
                      className={cn(
                        'rounded px-1 text-[10px]',
                        strategy === 'balanced'
                          ? 'bg-primary-foreground/20 text-primary-foreground'
                          : 'bg-primary/10 text-primary'
                      )}
                    >
                      {t('Recommended')}
                    </span>
                  )}
                </Button>
              ))}
            </div>
            <p
              role='status'
              className='border-primary/20 bg-primary/5 text-foreground rounded-lg border px-3 py-2 text-xs leading-relaxed'
            >
              {
                strategies.find((option) => option.value === strategy)
                  ?.description
              }
            </p>
            {Object.entries(draft.models).map(([name, config]) => (
              <section key={name} className='space-y-2 rounded-lg border p-3'>
                <h3 className='flex min-w-0 flex-wrap items-center gap-2 text-sm font-semibold'>
                  <span aria-hidden='true'>{icon(name)}</span>
                  <span className='min-w-0 flex-1 break-all'>{name}</span>
                  <span className='text-muted-foreground text-xs'>
                    {t('{{count}} groups · {{strategy}}', {
                      count: number(config.groups.length),
                      strategy: strategies.find(
                        (option) => option.value === strategy
                      )?.label,
                    })}
                  </span>
                </h3>
                {strategy === 'manual' ? (
                  <Reorder.Group
                    axis='y'
                    values={config.groups}
                    onReorder={(groups) => changeGroups(name, groups)}
                    className='space-y-1'
                  >
                    {config.groups.map((group, index) => (
                      <AutoGroupOrderItem
                        key={group}
                        group={group}
                        index={index}
                        count={config.groups.length}
                        leading={
                          <span className='text-muted-foreground text-xs'>
                            {number(index + 1)}
                          </span>
                        }
                        onMove={(index, direction) =>
                          updateModel(name, moveGroup(config, index, direction))
                        }
                        onRemove={(group) =>
                          changeGroups(
                            name,
                            config.groups.filter((value) => value !== group)
                          )
                        }
                      >
                        <span className='text-muted-foreground text-[11px]'>
                          {healthLabel(name, group)}
                        </span>
                        {preference(name, group, config)}
                      </AutoGroupOrderItem>
                    ))}
                  </Reorder.Group>
                ) : (
                  <div className='flex flex-wrap gap-2'>
                    {config.groups.map((group) => (
                      <span
                        key={group}
                        className={cn(
                          'bg-muted inline-flex items-center gap-2 rounded px-2 text-xs',
                          unavailable(name, group) && 'line-through'
                        )}
                      >
                        <span
                          className='max-w-56 truncate'
                          title={`${group} · ${healthLabel(name, group)}`}
                        >
                          {group}
                        </span>
                        <span className='inline-flex min-h-7 items-center px-1'>
                          {preference(name, group, config)}
                        </span>
                      </span>
                    ))}
                  </div>
                )}
              </section>
            ))}
          </section>
        )}
      </div>
    </Dialog>
  )
}

import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { getRoutingCandidates, getRoutingModels } from './api'
import { CandidateCard } from './candidate-card'
import { CandidateFilters, CandidatePagination } from './candidate-filters'
import { routingIsValid } from './policy'
import { RoutingSelect } from './routing-select'
import type { DynamicRouting, ModelCategory } from './types'

export function DynamicExclusionsDialog(props: {
  value: DynamicRouting
  tokenId?: number
  groups?: string[]
  onSave: (value: DynamicRouting) => void
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const [draft, setDraft] = useState(props.value)
  const [search, setSearch] = useState('')
  const [category, setCategory] = useState<ModelCategory>('all')
  const [model, setModel] = useState('')
  const [page, setPage] = useState(1)
  const modelQuery = useQuery({
    queryKey: ['routing-models', props.tokenId],
    queryFn: ({ signal }) => getRoutingModels(props.tokenId, signal),
    staleTime: 30_000,
  })
  const query = useQuery({
    queryKey: [
      'routing-exclusions',
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
  const number = (value: number) =>
    formatNumber(value, toIntlLocale(i18n.resolvedLanguage || i18n.language))
  const models = (modelQuery.data ?? []).filter(
    (item) =>
      (category === 'all' || item.category === category) &&
      (!search ||
        `${item.model} ${item.display_name}`
          .toLowerCase()
          .includes(search.trim().toLowerCase()))
  )
  const groups = [
    ...new Set([...(props.groups ?? []), ...items.map((item) => item.group)]),
  ].filter((name) => name !== 'auto' && name !== 'all')
  const blockedByRule = (name: string, group: string) =>
    !!draft.excluded_models?.includes(name) ||
    !!draft.excluded_groups?.includes(group)
  const excluded = (name: string, group: string) =>
    blockedByRule(name, group) ||
    !!draft.excluded_model_groups?.[name]?.includes(group)
  const pairCount = Object.values(draft.excluded_model_groups ?? {}).reduce(
    (sum, values) => sum + values.length,
    0
  )
  const count =
    (draft.excluded_models?.length ?? 0) +
    (draft.excluded_groups?.length ?? 0) +
    pairCount
  const updatePairs = (
    pairs: Array<{ model: string; group: string }>,
    remove: boolean
  ) => {
    setDraft((current) => {
      const exclusions = { ...current.excluded_model_groups }
      for (const item of pairs) {
        const old = exclusions[item.model] ?? []
        const next = remove
          ? old.filter((group) => group !== item.group)
          : [...new Set([...old, item.group])]
        if (next.length) exclusions[item.model] = next
        else delete exclusions[item.model]
      }
      return { ...current, excluded_model_groups: exclusions }
    })
  }
  const resetFilters = () => {
    setModel('')
    setPage(1)
  }
  const filtered = !!search || category !== 'all' || !!model || page > 1
  const clearFilters = () => {
    setSearch('')
    setCategory('all')
    resetFilters()
  }
  const restoreModel = (name: string) => {
    setDraft((current) => {
      const pairs = { ...current.excluded_model_groups }
      delete pairs[name]
      return {
        ...current,
        excluded_models: current.excluded_models?.filter(
          (value) => value !== name
        ),
        excluded_model_groups: pairs,
      }
    })
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Exclusion settings')}
      description={t(
        'Exclude models or groups you do not want to use. Available items that are not excluded join automatically.'
      )}
      contentClassName='h-[min(700px,calc(100dvh-2rem))] gap-0 overflow-hidden rounded-xl p-0 sm:max-w-[min(1092px,calc(100vw-2rem))] sm:p-0 [&>div:nth-child(2)]:mx-0 [&>div:nth-child(2)]:flex-1 [&>div:nth-child(2)]:overflow-hidden [&_[data-slot=button]]:min-h-11 [&_[data-slot=button]]:min-w-11 [&_[data-slot=dialog-close]]:size-11'
      headerClassName='border-b px-5 py-4 pr-14'
      contentHeight='100%'
      bodyClassName='h-full px-0 py-0'
      footerClassName='mx-0 mb-0 flex-row flex-wrap items-center border-t p-3 sm:mx-0 sm:mb-0 sm:p-3'
      footer={
        <>
          <p
            role='status'
            className='text-muted-foreground mr-auto w-full text-xs sm:w-auto'
          >
            {t(
              '{{count}} exclusions set; {{kept}} / {{visible}} items kept on this page.',
              {
                count: number(count),
                kept: number(
                  items.filter((item) => !excluded(item.model, item.group))
                    .length
                ),
                visible: number(items.length),
              }
            )}
          </p>
          <Button type='button' variant='outline' onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            disabled={!routingIsValid(draft)}
            onClick={() => props.onSave(draft)}
          >
            {t('Apply exclusions')}
          </Button>
        </>
      }
    >
      <div className='flex h-full min-h-0 flex-col'>
        {modelQuery.isPending && <LoadingState inline size='sm' />}
        {modelQuery.isError && (
          <ErrorState
            description={t('Loading failed. Please retry.')}
            onRetry={() => void modelQuery.refetch()}
          />
        )}
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
        <div className='flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto lg:grid lg:grid-cols-[minmax(0,1fr)_248px] lg:overflow-hidden'>
          <section
            className='min-h-0 min-w-0 shrink-0 space-y-3 p-3 lg:overflow-y-auto'
            aria-label={t('Routing candidates')}
          >
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <h3 className='text-sm font-semibold break-all'>
                {model || t('All models')}
              </h3>
              <div className='flex flex-wrap gap-2'>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={!items.length || query.isFetching}
                  onClick={() => updatePairs(items, true)}
                >
                  {t('Restore this page')}
                </Button>
                <Button
                  type='button'
                  size='sm'
                  variant='outline'
                  disabled={!items.length || query.isFetching}
                  onClick={() =>
                    updatePairs(
                      items.filter(
                        (item) => !blockedByRule(item.model, item.group)
                      ),
                      false
                    )
                  }
                >
                  {t('Exclude this page')}
                </Button>
                {model && (
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    disabled={
                      !draft.excluded_models?.includes(model) &&
                      !draft.excluded_model_groups?.[model]?.length
                    }
                    onClick={() => restoreModel(model)}
                  >
                    {t('Restore this entire model')}
                  </Button>
                )}
                {model && (
                  <Button
                    type='button'
                    size='sm'
                    variant='ghost'
                    disabled={draft.excluded_models?.includes(model)}
                    onClick={() =>
                      setDraft((current) => ({
                        ...current,
                        excluded_models: [
                          ...new Set([
                            ...(current.excluded_models ?? []),
                            model,
                          ]),
                        ],
                      }))
                    }
                  >
                    {t('Exclude entire model')}
                  </Button>
                )}
              </div>
            </div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Page actions affect only the items shown here. Other pages stay unchanged.'
              )}
            </p>
            {query.isPending && <LoadingState />}
            {query.isError && (
              <ErrorState
                description={t('Loading failed. Please retry.')}
                onRetry={() => void query.refetch()}
              />
            )}
            {query.isSuccess && !items.length && (
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
            {items.length > 0 &&
              items.every((item) => excluded(item.model, item.group)) && (
                <p
                  role='alert'
                  className='rounded-xl border border-amber-500/30 bg-amber-500/5 p-3 text-sm'
                >
                  {t(
                    'Everything on this page is excluded. Requests cannot complete if no other groups are available.'
                  )}
                </p>
              )}
            {items.map((candidate) => (
              <CandidateCard
                key={`${candidate.model}:${candidate.group}`}
                candidate={candidate}
                selected={!excluded(candidate.model, candidate.group)}
                allowUnavailableSelection
                selectionDisabled={blockedByRule(
                  candidate.model,
                  candidate.group
                )}
                selectionHint={
                  blockedByRule(candidate.model, candidate.group)
                    ? t(
                        'This item is blocked by a model-wide or all-model exclusion. Restore it in the exclusion list first.'
                      )
                    : undefined
                }
                onSelect={(checked) => updatePairs([candidate], checked)}
              />
            ))}
            <CandidatePagination
              page={page}
              hasNext={
                !!query.data && page * query.data.page_size < query.data.total
              }
              fetching={query.isFetching}
              onPage={setPage}
            />
          </section>
          <aside
            className='min-h-0 min-w-0 shrink-0 space-y-3 border-t p-3 lg:overflow-y-auto lg:border-t-0 lg:border-l'
            aria-label={t('Exclusion list')}
          >
            {!!draft.excluded_groups?.length && (
              <p className='text-muted-foreground text-xs leading-relaxed'>
                {t(
                  'Restoring a model clears its exclusions across all pages. Group exclusions that apply to all models stay in place.'
                )}
              </p>
            )}
            <div className='flex items-center justify-between gap-2'>
              <h3 className='font-semibold'>{t('Exclusion list')}</h3>
              <Button
                type='button'
                size='sm'
                variant='ghost'
                disabled={!count}
                onClick={() =>
                  setDraft({
                    ...draft,
                    excluded_models: [],
                    excluded_groups: [],
                    excluded_model_groups: {},
                  })
                }
              >
                {t('Restore all')}
              </Button>
            </div>
            {!count && (
              <p className='text-muted-foreground py-6 text-center text-sm'>
                {t('Nothing is excluded. All available groups can be used.')}
              </p>
            )}
            {!!draft.excluded_models?.length && (
              <section className='space-y-2'>
                <h4 className='text-muted-foreground text-xs'>
                  {t('Models not to use')}
                </h4>
                {draft.excluded_models?.map((name) => (
                  <Button
                    key={name}
                    type='button'
                    variant='outline'
                    className='h-auto w-full justify-between break-all whitespace-normal'
                    aria-label={t('Restore model {{model}}', { model: name })}
                    onClick={() => restoreModel(name)}
                  >
                    {name}
                    <span aria-hidden='true'>×</span>
                  </Button>
                ))}
              </section>
            )}
            <section className='space-y-2'>
              {!!draft.excluded_groups?.length && (
                <h4 className='text-muted-foreground text-xs'>
                  {t('Groups not to use for any model')}
                </h4>
              )}
              <RoutingSelect
                label={t('Exclude group everywhere')}
                value=''
                options={[
                  { value: '', label: t('Choose a group') },
                  ...groups
                    .filter((name) => !draft.excluded_groups?.includes(name))
                    .map((name) => ({ value: name, label: name })),
                ]}
                onChange={(value) => {
                  if (value) {
                    setDraft({
                      ...draft,
                      excluded_groups: [
                        ...(draft.excluded_groups ?? []),
                        value,
                      ],
                    })
                  }
                }}
              />
              {draft.excluded_groups?.map((name) => (
                <Button
                  key={name}
                  type='button'
                  variant='outline'
                  className='h-auto w-full justify-between break-all whitespace-normal'
                  aria-label={t('Restore global group {{group}}', {
                    group: name,
                  })}
                  onClick={() =>
                    setDraft({
                      ...draft,
                      excluded_groups: draft.excluded_groups?.filter(
                        (value) => value !== name
                      ),
                    })
                  }
                >
                  {name}
                  <span aria-hidden='true'>×</span>
                </Button>
              ))}
            </section>
            {!!pairCount && (
              <section className='space-y-3'>
                <h4 className='text-muted-foreground text-xs'>
                  {t('Groups not to use for a specific model')}
                </h4>
                {Object.entries(draft.excluded_model_groups ?? {}).map(
                  ([name, values]) => (
                    <div key={name} className='space-y-2'>
                      <div className='flex flex-wrap items-center justify-between gap-2'>
                        <p className='text-xs font-medium break-all'>{name}</p>
                        <Button
                          type='button'
                          variant='ghost'
                          size='sm'
                          aria-label={t(
                            'Restore all exclusions for {{model}}',
                            {
                              model: name,
                            }
                          )}
                          onClick={() => restoreModel(name)}
                        >
                          {t('Restore this entire model')}
                        </Button>
                      </div>
                      {values.map((group) => (
                        <Button
                          key={group}
                          type='button'
                          variant='outline'
                          className='h-auto w-full justify-between break-all whitespace-normal'
                          aria-label={t('Restore {{model}} / {{group}}', {
                            model: name,
                            group,
                          })}
                          onClick={() =>
                            updatePairs([{ model: name, group }], true)
                          }
                        >
                          {group}
                          <span aria-hidden='true'>×</span>
                        </Button>
                      ))}
                    </div>
                  )
                )}
              </section>
            )}
          </aside>
        </div>
      </div>
    </Dialog>
  )
}

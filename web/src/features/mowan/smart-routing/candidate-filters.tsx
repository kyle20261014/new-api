import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import type { ModelCategory } from './types'

export function CandidateFilters(props: {
  search: string
  category: ModelCategory
  model: string
  models: string[]
  onSearch: (value: string) => void
  onCategory: (value: ModelCategory) => void
  onModel: (value: string) => void
}) {
  const { t } = useTranslation()
  const categories = [
    { value: 'all' as const, label: t('All') },
    { value: 'text' as const, label: t('Text') },
    { value: 'image' as const, label: t('Image') },
    { value: 'video' as const, label: t('Video') },
    { value: 'audio' as const, label: t('Audio') },
  ]
  return (
    <div className='space-y-3 border-b pb-4'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div
          role='group'
          aria-label={t('Model category')}
          className='flex flex-wrap gap-1'
        >
          {categories.map((item) => (
            <Button
              key={item.value}
              type='button'
              size='sm'
              variant={props.category === item.value ? 'secondary' : 'ghost'}
              aria-pressed={props.category === item.value}
              className='rounded-full'
              onClick={() => props.onCategory(item.value)}
            >
              {item.label}
            </Button>
          ))}
        </div>
        <Input
          aria-label={t('Search model name')}
          placeholder={t('Search model name')}
          value={props.search}
          maxLength={128}
          className='min-h-11 w-full rounded-full sm:w-64'
          onChange={(event) => props.onSearch(event.target.value)}
        />
      </div>
      <div
        className='flex max-h-32 flex-wrap gap-2 overflow-y-auto'
        role='group'
        aria-label={t('Models')}
      >
        <Button
          type='button'
          variant={!props.model ? 'secondary' : 'outline'}
          className='h-auto min-h-11 rounded-full'
          aria-pressed={!props.model}
          onClick={() => props.onModel('')}
        >
          {t('All models')}
        </Button>
        {props.models.map((name) => (
          <Button
            key={name}
            type='button'
            variant={props.model === name ? 'secondary' : 'outline'}
            className='h-auto min-h-11 max-w-full rounded-full break-all whitespace-normal'
            aria-pressed={props.model === name}
            onClick={() => props.onModel(name)}
          >
            {name}
          </Button>
        ))}
      </div>
    </div>
  )
}

export function CandidatePagination(props: {
  page: number
  hasNext: boolean
  fetching: boolean
  onPage: (page: number) => void
}) {
  const { t } = useTranslation()
  return (
    <div className='flex items-center justify-between gap-3 border-t pt-3'>
      <Button
        type='button'
        variant='outline'
        disabled={props.page === 1 || props.fetching}
        onClick={() => props.onPage(props.page - 1)}
      >
        {t('Previous')}
      </Button>
      <Button
        type='button'
        variant='outline'
        disabled={!props.hasNext || props.fetching}
        onClick={() => props.onPage(props.page + 1)}
      >
        {t('Next page')}
      </Button>
    </div>
  )
}

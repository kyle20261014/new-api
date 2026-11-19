/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { ArrowRight } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { RoutingExperience } from '@/features/mowan/smart-routing/routing-experience'
import { getLobeIcon } from '@/lib/lobe-icon'

import { useHomepageModels } from './model-catalog'
import type { ModelCapsule } from './types'

export interface ModelsSectionProps {
  onExploreClick?: () => void
}

function ModelChip(props: {
  model: ModelCapsule
  isDuplicate?: boolean
  onClick?: (model: ModelCapsule) => void
}) {
  const model = props.model

  return (
    <Button
      variant='ghost'
      type='button'
      tabIndex={props.isDuplicate ? -1 : 0}
      aria-hidden={props.isDuplicate ? 'true' : undefined}
      onClick={() => props.onClick?.(model)}
      className='xc-chip h-auto shrink-0'
    >
      <span className='xc-model-icon'>{getLobeIcon(model.icon, 26)}</span>
      <span className='xc-model-name whitespace-nowrap'>{model.name}</span>
      {model.tag && (
        <span className='rounded bg-white/10 px-1.5 py-0.5 text-[10px] font-normal text-slate-400'>
          {model.tag}
        </span>
      )}
    </Button>
  )
}

export function ModelsSection(props: ModelsSectionProps) {
  const { t } = useTranslation()
  const catalog = useHomepageModels()
  const models = catalog.data ?? []
  const rows = [
    models.filter((_, i) => i % 2 === 0),
    models.filter((_, i) => i % 2 === 1),
  ]
  const [selectedModel, setSelectedModel] = useState<ModelCapsule | null>(null)

  const handleScrollTo = (id: string) => {
    const el = document.querySelector<HTMLElement>(`#${id}`)
    if (el) {
      el.scrollIntoView({ behavior: 'smooth' })
    }
  }

  const handleModelClick = (model: ModelCapsule) => {
    setSelectedModel(model)
  }

  return (
    <section
      id='models'
      data-xc-section='models'
      className='xc-section xc-models'
    >
      {/* Section number tag */}
      <div className='xc-eyebrow justify-center'>
        <span className='xc-eyebrow-dot' />
        {t('mowan.models.sectionTag', '03 / 模型')}
      </div>

      {/* Section Title */}
      <h2>{t('mowan.models.title', '不同的想法，各有答案')}</h2>

      {/* Subtitle */}
      <p className='xc-lead mx-auto'>
        {t(
          'mowan.models.subtitle',
          '为每一次创作，找到适合的模型。一个平台，连接多种可能。'
        )}
      </p>

      {models.length > 0 && (
        <p className='mb-4 text-sm text-slate-400'>
          {t('mowan.models.catalogOrder')}
        </p>
      )}
      {catalog.isLoading && <LoadingState />}
      {catalog.isError && (
        <ErrorState
          title={t('mowan.models.catalogError')}
          description={
            catalog.data ? t('mowan.models.catalogCached') : undefined
          }
          onRetry={() => void catalog.refetch()}
          className='min-h-0 py-4'
        />
      )}
      {!catalog.isLoading && !catalog.isError && models.length === 0 && (
        <EmptyState
          title={t('mowan.models.catalogEmpty')}
          className='min-h-0 py-4'
        />
      )}

      {/* Marquee Container with reference styling & keyboard accessibility */}
      {models.length > 0 && (
        <div className='xc-model-marquee'>
          {rows.map(
            (row, rowIndex) =>
              row.length > 0 && (
                <div
                  key={row[0].id}
                  className={`xc-model-lane no-scrollbar${rowIndex === 1 ? ' xc-reverse' : ''}`}
                  tabIndex={0}
                  role='region'
                  aria-label={`${t('mowan.nav.models', '模型')} ${rowIndex + 1}`}
                >
                  <div className='xc-model-track'>
                    <div className='xc-model-group'>
                      {row.map((model) => (
                        <ModelChip
                          key={`r1-${model.id}`}
                          model={model}
                          onClick={handleModelClick}
                        />
                      ))}
                    </div>
                    <div className='xc-model-group' aria-hidden='true'>
                      {row.map((model) => (
                        <ModelChip
                          key={`r1-dup-${model.id}`}
                          model={model}
                          isDuplicate
                          onClick={handleModelClick}
                        />
                      ))}
                    </div>
                  </div>
                </div>
              )
          )}
        </div>
      )}

      {/* Placeholder Feedback */}
      {selectedModel && (
        <RoutingExperience
          key={selectedModel.id}
          model={selectedModel.id}
          canChat={selectedModel.canChat}
          open
          onOpenChange={(open) => {
            if (!open) setSelectedModel(null)
          }}
        />
      )}

      {/* CTA Button */}
      <div className='mt-8 flex justify-center'>
        <Button
          type='button'
          onClick={() =>
            props.onExploreClick
              ? props.onExploreClick()
              : handleScrollTo('create')
          }
          className='xc-outline'
        >
          <span>{t('mowan.models.cta', '探索全部模型')}</span>
          <ArrowRight className='size-4' />
        </Button>
      </div>
    </section>
  )
}

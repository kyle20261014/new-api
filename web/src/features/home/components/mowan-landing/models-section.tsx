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

import { Button } from '@/components/ui/button'
import { getLobeIcon } from '@/lib/lobe-icon'

import { MODELS_ROW_1, MODELS_ROW_2 } from './data'
import type { ModelCapsule } from './types'

export interface ModelsSectionProps {
  onExploreClick?: () => void
}

function ModelChip(props: {
  model: ModelCapsule
  isDuplicate?: boolean
  onClick?: (model: ModelCapsule) => void
}) {
  const { t } = useTranslation()
  const model = props.model
  const displayTag = model.tagKey ? t(model.tagKey, model.tag || '') : model.tag

  return (
    <Button
      variant='ghost'
      type='button'
      tabIndex={props.isDuplicate ? -1 : 0}
      aria-hidden={props.isDuplicate ? 'true' : undefined}
      onClick={() => props.onClick?.(model)}
      className='xc-chip h-auto shrink-0'
    >
      <span className='xc-model-icon'>
        {typeof model.icon === 'string'
          ? getLobeIcon(model.icon, 26)
          : model.icon || (
              <span
                className='xc-mark size-2.5 shrink-0 rounded-full'
                style={{ backgroundColor: model.color || '#38bdf8' }}
              />
            )}
      </span>
      <span className='xc-model-name whitespace-nowrap'>{model.name}</span>
      {displayTag && (
        <span className='rounded bg-white/10 px-1.5 py-0.5 text-[10px] font-normal text-slate-400'>
          {displayTag}
        </span>
      )}
    </Button>
  )
}

export function ModelsSection(props: ModelsSectionProps) {
  const { t } = useTranslation()
  const [feedbackMessage, setFeedbackMessage] = useState<string | null>(null)

  const handleScrollTo = (id: string) => {
    const el = document.querySelector<HTMLElement>(`#${id}`)
    if (el) {
      el.scrollIntoView({ behavior: 'smooth' })
    }
  }

  const handleModelClick = (model: ModelCapsule) => {
    setFeedbackMessage(
      t('mowan.placeholder.notice', {
        title: model.name,
        defaultValue: `${model.name}功能即将推出，敬请期待！`,
      })
    )
    setTimeout(() => setFeedbackMessage(null), 2500)
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

      {/* Marquee Container with reference styling & keyboard accessibility */}
      <div className='xc-model-marquee'>
        {/* Row 1: Scrolling Left */}
        <div
          className='xc-model-lane no-scrollbar'
          tabIndex={0}
          role='region'
          aria-label={`${t('mowan.nav.models', '模型')} 1`}
        >
          <div className='xc-model-track'>
            <div className='xc-model-group'>
              {MODELS_ROW_1.map((model) => (
                <ModelChip
                  key={`r1-${model.id}`}
                  model={model}
                  onClick={handleModelClick}
                />
              ))}
            </div>
            <div className='xc-model-group' aria-hidden='true'>
              {MODELS_ROW_1.map((model) => (
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

        {/* Row 2: Scrolling Right (Reverse) */}
        <div
          className='xc-model-lane xc-reverse no-scrollbar'
          tabIndex={0}
          role='region'
          aria-label={`${t('mowan.nav.models', '模型')} 2`}
        >
          <div className='xc-model-track'>
            <div className='xc-model-group'>
              {MODELS_ROW_2.map((model) => (
                <ModelChip
                  key={`r2-${model.id}`}
                  model={model}
                  onClick={handleModelClick}
                />
              ))}
            </div>
            <div className='xc-model-group' aria-hidden='true'>
              {MODELS_ROW_2.map((model) => (
                <ModelChip
                  key={`r2-dup-${model.id}`}
                  model={model}
                  isDuplicate
                  onClick={handleModelClick}
                />
              ))}
            </div>
          </div>
        </div>
      </div>

      {/* Placeholder Feedback */}
      {feedbackMessage && (
        <div
          role='status'
          aria-live='polite'
          className='animate-fade-in mx-auto mb-6 inline-block rounded-full border border-cyan-500/40 bg-cyan-950/90 px-5 py-2 text-xs font-medium text-cyan-200 shadow-xl backdrop-blur-md sm:text-sm'
        >
          {feedbackMessage}
        </div>
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

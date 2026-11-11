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
import { Link } from '@tanstack/react-router'
import { ArrowRight, ChevronDown, Gift } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { getLobeIcon } from '@/lib/lobe-icon'

import { HERO_FLOATING_MODELS } from './data'

export interface HeroSectionProps {
  onExploreClick?: () => void
  onCreateClick?: () => void
}

const HERO_CHIP_COORDS = [
  { x: 16, y: 18 },
  { x: 58, y: 8 },
  { x: 80, y: 22 },
  { x: 6, y: 48 },
  { x: 86, y: 50 },
  { x: 14, y: 78 },
  { x: 46, y: 90 },
  { x: 78, y: 82 },
  { x: 2, y: 32 },
  { x: 70, y: 66 },
  { x: 36, y: 40 },
]

export function HeroSection(props: HeroSectionProps) {
  const { t } = useTranslation()

  const handleScrollTo = (id: string) => {
    const el = document.querySelector<HTMLElement>(`#${id}`)
    if (el) {
      el.scrollIntoView({
        behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches
          ? 'auto'
          : 'smooth',
      })
    }
  }

  return (
    <section id='hero' data-xc-section='hero' className='xc-section xc-hero'>
      {/* Left Column: Headline and Call-to-actions */}
      <div className='xc-hero-copy'>
        {/* Eyebrow subtitle */}
        <div className='xc-eyebrow'>
          {t('mowan.hero.eyebrow', '• AI 聚合平台 · 对话 / 图像 / 视频 / 声音')}
        </div>

        {/* Giant Title matching reference source (regular first line + gradient span second line) */}
        <h1>
          {t('mowan.hero.title1', '一个入口')}
          <br />
          {t('mowan.hero.title2', '让想象连接')} <span>AI</span>
        </h1>

        {/* Subtitle description */}
        <p className='xc-lead'>
          {t(
            'mowan.hero.subtitle',
            '从一句话到一幅画，从一个想法到一段影像。汇聚多种 AI 创作能力，让灵感在这里成为作品。'
          )}
        </p>

        {/* CTAs button group reusing project Button with exact styles */}
        <div className='xc-buttons'>
          <Button
            type='button'
            onClick={() =>
              props.onCreateClick
                ? props.onCreateClick()
                : handleScrollTo('create')
            }
            className='xc-outline'
          >
            <span>{t('mowan.hero.cta.start', '开始创作')}</span>
            <ArrowRight className='size-4' />
          </Button>

          <Button
            type='button'
            variant='ghost'
            onClick={() =>
              props.onExploreClick
                ? props.onExploreClick()
                : handleScrollTo('models')
            }
            className='xc-glass'
          >
            {t('mowan.hero.cta.explore', '看看它能做什么')}
          </Button>
        </div>

        {/* Free Gift Promo Link with SVG Gift icon (no raw emoji) */}
        <div>
          <Link to='/sign-up' className='xc-signup-gift group'>
            <Gift className='wzs-gift size-4 shrink-0 text-cyan-400' />
            <span>
              {t(
                'mowan.hero.giftPromo',
                '注册领新人免费大礼包，AI 绘画 / 视频 / 对话免费开用'
              )}
            </span>
            <ArrowRight className='wzs-arrow size-3.5 shrink-0 transition-transform group-hover:translate-x-0.5' />
          </Link>
        </div>
      </div>

      {/* Right Column: Cosmic Star Orb & 11 Floating Model Chips matching reference template (807-845) */}
      <div className='xc-orbit'>
        {HERO_FLOATING_MODELS.slice(0, 11).map((model, idx) => {
          const coord = HERO_CHIP_COORDS[idx] || { x: 50, y: 50 }
          return (
            <Button
              key={model.id}
              variant='ghost'
              type='button'
              onClick={() =>
                props.onCreateClick
                  ? props.onCreateClick()
                  : handleScrollTo('create')
              }
              title={model.name}
              className='xc-chip xc-floating'
              style={
                {
                  left: `${coord.x}%`,
                  top: `${coord.y}%`,
                  '--xc-delay': `${-idx * 0.37}s`,
                } as React.CSSProperties
              }
            >
              <span className='xc-model-icon'>
                {typeof model.icon === 'string'
                  ? getLobeIcon(model.icon, 26)
                  : model.icon || (
                      <span
                        className='xc-mark size-2.5 shrink-0 rounded-full'
                        style={{ backgroundColor: model.color || '#4fd1ff' }}
                      />
                    )}
              </span>
              <span className='xc-model-name'>{model.name}</span>
            </Button>
          )
        })}
      </div>

      {/* Downward Scroll Cue positioned at bottom-left */}
      <button
        type='button'
        onClick={() => handleScrollTo('create')}
        aria-label={t('mowan.hero.scrollDown', '向下探索')}
        className='xc-scroll'
      >
        <span>{t('mowan.hero.scrollDown', '向下探索')}</span>
        <ChevronDown className='size-3.5 motion-safe:animate-bounce' />
      </button>
    </section>
  )
}

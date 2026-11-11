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
import { ArrowRight } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import { INSPIRATION_GALLERY } from './data'
import type { GalleryItem } from './types'

function GalleryCard(props: {
  item: GalleryItem
  onClick?: (item: GalleryItem) => void
}) {
  const { t } = useTranslation()
  const item = props.item

  return (
    <Button
      variant='ghost'
      type='button'
      onClick={() => props.onClick?.(item)}
      aria-label={t(item.titleKey, item.titleFallback)}
      className='xc-pubu-card group'
    >
      {/* Artwork Image Container with Lazy Loading */}
      <img
        src={item.image}
        alt={t(item.titleKey, item.titleFallback)}
        loading='lazy'
        width={item.width}
        height={item.height}
        className='xc-pubu-img'
      />

      {/* Info Footer on Card */}
      <div className='xc-pubu-info'>
        <h3
          className='xc-pubu-title'
          title={t(item.titleKey, item.titleFallback)}
        >
          {t(item.titleKey, item.titleFallback)}
        </h3>
        <div className='xc-pubu-tags'>
          <span
            className={cn(
              'xc-pubu-tag',
              item.generated && 'xc-pubu-tag-original'
            )}
          >
            {t(item.tagKey, item.generated ? '魔丸原创' : '精选社区')}
          </span>
          <span className='xc-pubu-tag'>
            {t(item.subKey, item.generated ? 'Mowan AI' : 'Community')}
          </span>
        </div>
      </div>
    </Button>
  )
}

function getResponsiveColumnCount(): number {
  if (typeof window === 'undefined') return 5
  const w = window.innerWidth
  if (w >= 1536) return 6
  if (w >= 1152) return 5
  if (w >= 832) return 4
  if (w >= 576) return 3
  return 2
}

export function InspirationSection() {
  const { t } = useTranslation()
  const [columns, setColumns] = useState<number>(5)
  const [feedbackMessage, setFeedbackMessage] = useState<string | null>(null)

  const handleCardClick = (item: GalleryItem) => {
    const title = t(item.titleKey, item.titleFallback)
    setFeedbackMessage(
      t('mowan.placeholder.notice', {
        title,
        defaultValue: `${title}功能即将推出，敬请期待！`,
      })
    )
    setTimeout(() => setFeedbackMessage(null), 2500)
  }

  useEffect(() => {
    const handleResize = () => {
      setColumns(getResponsiveColumnCount())
    }
    handleResize()
    window.addEventListener('resize', handleResize)
    return () => window.removeEventListener('resize', handleResize)
  }, [])

  // Floor to exact multiple of columns and round-robin distribute across columns
  const { columnGroups } = useMemo(() => {
    const totalToDisplay =
      Math.floor(INSPIRATION_GALLERY.length / columns) * columns
    const displayedItems = INSPIRATION_GALLERY.slice(0, totalToDisplay)
    const groups = Array.from({ length: columns }, () => [] as GalleryItem[])
    displayedItems.forEach((item, idx) => {
      groups[idx % columns].push(item)
    })
    return {
      totalToDisplay,
      columnGroups: groups.map((items, colIdx) => ({
        id: `col-${colIdx}`,
        items,
      })),
    }
  }, [columns])

  return (
    <section
      id='inspiration'
      data-xc-section='inspiration'
      className='xc-section xc-inspire relative overflow-hidden'
    >
      <div
        className='xc-pubu-container relative z-10'
        style={
          {
            '--xc-columns': columns,
          } as React.CSSProperties
        }
      >
        {/* Section Header */}
        <div className='mx-auto mb-[44px] max-w-3xl text-center'>
          <div className='xc-eyebrow justify-center'>
            {t('mowan.inspiration.sectionTag', '05 / 灵感')}
          </div>
          <h2>{t('mowan.inspiration.title', '灵感从不止于想象')}</h2>
          <p className='xc-lead mx-auto'>
            {t(
              'mowan.inspiration.subtitle',
              '探索海量由 AI 创作生成的精美作品，点燃你的无限灵感。'
            )}
          </p>
        </div>

        {/* Real Reference Waterfall Columns */}
        <div className='xc-pubu-columns'>
          {columnGroups.map((group) => (
            <div key={group.id} className='xc-pubu-column'>
              {group.items.map((item) => (
                <GalleryCard
                  key={item.id}
                  item={item}
                  onClick={handleCardClick}
                />
              ))}
            </div>
          ))}
        </div>

        {/* Feedback message banner for card interaction */}
        {feedbackMessage && (
          <div
            role='status'
            aria-live='polite'
            className='animate-fade-in fixed bottom-24 left-1/2 z-50 -translate-x-1/2 rounded-full border border-cyan-500/40 bg-slate-900/95 px-5 py-2 text-xs font-medium text-cyan-200 shadow-2xl backdrop-blur-md sm:text-sm'
          >
            {feedbackMessage}
          </div>
        )}

        {/* Gallery Bottom Login Prompt with Curved Stardust Orbit */}
        <div className='xc-pubu-more relative mt-16 pt-12 pb-6 text-center'>
          {/* Curved purple stardust trail effect */}
          <div className='pointer-events-none absolute inset-x-0 top-0 h-24 overflow-hidden'>
            <div className='mowan-pulse-glow mx-auto size-[600px] rounded-[50%] border-t border-purple-400/30 opacity-60' />
          </div>

          <p className='relative z-10 mb-4 text-sm font-medium text-slate-300 sm:text-base'>
            {t(
              'mowan.inspiration.unlockText',
              '想看更多灵感？登录即可解锁全部内容'
            )}
          </p>

          <div className='relative z-10 flex justify-center'>
            <Button
              className='h-auto rounded-full bg-cyan-500 px-6 py-3.5 text-sm font-semibold text-slate-950 shadow-lg shadow-cyan-500/25 transition-all duration-200 hover:scale-[1.02] hover:bg-cyan-400 hover:shadow-cyan-400/35'
              render={<Link to='/sign-in' />}
            >
              <span>{t('mowan.inspiration.unlockBtn', '立即登录查看')}</span>
              <ArrowRight className='ml-1.5 size-4' />
            </Button>
          </div>
        </div>
      </div>
    </section>
  )
}

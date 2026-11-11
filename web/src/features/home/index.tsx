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
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { RichContent } from '@/components/rich-content'
import { useTheme } from '@/context/theme-provider'
import { useMinimumLoadingTime } from '@/hooks/use-minimum-loading-time'
import { isLikelyHtml } from '@/lib/content-format'
import { cn } from '@/lib/utils'

import { MowanLanding } from './components/mowan-landing'
import { ParticleRibbon } from './components/mowan-landing/loader-particle-ribbon'
import { useHomePageContent } from './hooks'

import './components/mowan-landing/styles.css'

export function Home() {
  const { i18n, t } = useTranslation()
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const { resolvedTheme } = useTheme()
  const { content, isLoaded, isUrl } = useHomePageContent()
  const showLoading = useMinimumLoadingTime(!isLoaded, 1000)
  const [overlayVisible, setOverlayVisible] = useState(showLoading)
  const isOverlayPresent = showLoading || overlayVisible
  const isExiting = !showLoading && overlayVisible

  useEffect(() => {
    if (showLoading || !overlayVisible) return
    const reduced = window.matchMedia(
      '(prefers-reduced-motion: reduce)'
    ).matches
    const timer = window.setTimeout(
      () => setOverlayVisible(false),
      reduced ? 150 : 350
    )
    return () => window.clearTimeout(timer)
  }, [showLoading, overlayVisible])

  useEffect(() => {
    if (!isOverlayPresent) return
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = previousOverflow
    }
  }, [isOverlayPresent])

  const syncIframePreferences = useCallback(() => {
    try {
      iframeRef.current?.contentWindow?.postMessage(
        { themeMode: resolvedTheme },
        '*'
      )
      iframeRef.current?.contentWindow?.postMessage(
        { lang: i18n.language },
        '*'
      )
    } catch {
      // Cross-origin frames may reject access while navigating.
    }
  }, [i18n.language, resolvedTheme])

  useEffect(() => {
    if (isUrl) syncIframePreferences()
  }, [isUrl, syncIframePreferences])

  let backgroundContent: React.ReactNode = null
  if (isLoaded) {
    if (content && isUrl) {
      backgroundContent = (
        <PublicLayout showMainContainer={false}>
          <iframe
            ref={iframeRef}
            src={content}
            className='h-screen w-full border-none'
            title={t('Custom Home Page')}
            sandbox='allow-forms allow-popups allow-popups-to-escape-sandbox allow-scripts allow-top-navigation-by-user-activation'
            onLoad={syncIframePreferences}
          />
        </PublicLayout>
      )
    } else if (content && isLikelyHtml(content)) {
      backgroundContent = (
        <PublicLayout showMainContainer={false}>
          <RichContent
            mode='html'
            htmlVariant='isolated'
            content={content}
            className='custom-home-content'
          />
        </PublicLayout>
      )
    } else if (content) {
      backgroundContent = (
        <PublicLayout>
          <div className='mx-auto max-w-6xl px-4 py-8'>
            <RichContent
              mode='markdown'
              content={content}
              className='custom-home-content'
            />
          </div>
        </PublicLayout>
      )
    } else {
      backgroundContent = <MowanLanding />
    }
  }

  return (
    <>
      <div
        className={cn(
          isOverlayPresent &&
            'transition-opacity duration-350 ease-out motion-reduce:duration-150',
          isOverlayPresent && !isExiting ? 'opacity-0' : 'opacity-100'
        )}
        inert={isOverlayPresent || undefined}
        aria-hidden={isOverlayPresent || undefined}
      >
        {backgroundContent}
      </div>
      {isOverlayPresent && (
        <div
          role='status'
          aria-busy='true'
          className={cn(
            'mowan-home-loader-screen fixed inset-0 z-[1000] flex items-center justify-center overflow-hidden px-6 text-[#eef0fa]',
            'transition-opacity duration-350 ease-out motion-reduce:duration-150',
            isExiting && 'opacity-0'
          )}
        >
          <div
            className={cn(
              'flex flex-col items-center gap-7 transition-[opacity,transform] duration-350 ease-out motion-reduce:duration-150',
              isExiting && 'scale-95 opacity-0 motion-reduce:scale-100'
            )}
          >
            <ParticleRibbon>
              <div className='mowan-loader-core-flame pointer-events-none absolute size-20 rounded-full bg-gradient-to-tr from-red-600/35 via-rose-500/25 to-amber-500/15 blur-lg' />
              <img
                src='/images/mowan/mowan-icon.png'
                alt=''
                className='relative z-10 size-12 object-contain drop-shadow-[0_0_12px_rgba(244,63,94,0.35)]'
              />
            </ParticleRibbon>
            <span className='mowan-loader-brand-text text-sm font-semibold tracking-[0.25em] text-slate-200'>
              {t('mowan.brand.name', '魔丸AI')}
            </span>
            <span className='sr-only'>{t('Loading...')}</span>
          </div>
        </div>
      )}
    </>
  )
}

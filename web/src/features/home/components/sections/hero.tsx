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
import { CherryStudio } from '@lobehub/icons'
import { Link } from '@tanstack/react-router'
import { ArrowUpRight, BookOpen, Sparkles } from 'lucide-react'
import { useCallback, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useStatus } from '@/hooks/use-status'
import { getLobeIcon } from '@/lib/lobe-icon'

interface HeroProps {
  className?: string
  isAuthenticated?: boolean
}

interface CounterProps {
  end: number
  suffix?: string
  prefix?: string
  duration?: number
  decimals?: number
}

function Counter(props: CounterProps) {
  const { end, suffix = '', prefix = '', duration = 1800, decimals = 0 } = props
  const ref = useRef<HTMLSpanElement>(null)
  const startedRef = useRef(false)

  const formatValue = useCallback(
    (v: number) =>
      decimals > 0 ? v.toFixed(decimals) : Math.round(v).toLocaleString(),
    [decimals]
  )

  const animate = useCallback(() => {
    const el = ref.current
    if (!el) return
    const start = performance.now()
    const step = (now: number) => {
      const progress = Math.min((now - start) / duration, 1)
      const eased = 1 - Math.pow(1 - progress, 3)
      el.textContent = `${prefix}${formatValue(eased * end)}${suffix}`
      if (progress < 1) requestAnimationFrame(step)
    }
    requestAnimationFrame(step)
  }, [end, duration, prefix, suffix, formatValue])

  useEffect(() => {
    const el = ref.current
    if (!el) return

    const mq = window.matchMedia('(prefers-reduced-motion: reduce)')
    if (mq.matches) {
      el.textContent = `${prefix}${formatValue(end)}${suffix}`
      return
    }

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting && !startedRef.current) {
          startedRef.current = true
          animate()
          observer.unobserve(el)
        }
      },
      { threshold: 0.3 }
    )

    observer.observe(el)
    return () => observer.disconnect()
  }, [animate, end, prefix, suffix, formatValue])

  return (
    <span ref={ref} className='tabular-nums'>
      {prefix}0{suffix}
    </span>
  )
}

export function Hero(props: HeroProps) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const docsUrl =
    (status?.docs_link as string | undefined) || 'https://docs.newapi.pro'

  const renderDocsButton = () => {
    const isExternal = docsUrl.startsWith('http')
    const buttonClass =
      'group inline-flex h-12 items-center gap-2 rounded-xl border border-slate-300/80 bg-white/80 px-5 text-sm font-medium text-slate-800 backdrop-blur-md transition-all duration-200 hover:bg-slate-100 hover:text-slate-950 dark:border-purple-500/20 dark:bg-purple-500/5 dark:text-white/80 dark:hover:border-purple-400/40 dark:hover:bg-purple-500/15 dark:hover:text-white'

    if (isExternal) {
      return (
        <Button
          variant='outline'
          className={buttonClass}
          render={
            <a href={docsUrl} target='_blank' rel='noopener noreferrer' />
          }
        >
          <BookOpen className='size-4 text-purple-600 transition-colors group-hover:text-purple-700 dark:text-purple-400 dark:group-hover:text-purple-300' />
          <span>{t('Docs')}</span>
        </Button>
      )
    }
    return (
      <Button
        variant='outline'
        className={buttonClass}
        render={<Link to={docsUrl} />}
      >
        <BookOpen className='size-4 text-purple-600 transition-colors group-hover:text-purple-700 dark:text-purple-400 dark:group-hover:text-purple-300' />
        <span>{t('Docs')}</span>
      </Button>
    )
  }

  const modelEcosystem = [
    { name: 'OpenAI', icon: getLobeIcon('OpenAI', 24) },
    { name: 'Claude', icon: getLobeIcon('Claude.Color', 24) },
    { name: 'DeepSeek', icon: getLobeIcon('DeepSeek.Color', 24) },
    { name: 'Gemini', icon: getLobeIcon('Gemini.Color', 24) },
    { name: 'Meta LLaMA', icon: getLobeIcon('Meta.Color', 24) },
    { name: 'Mistral', icon: getLobeIcon('Mistral.Color', 24) },
    { name: 'Qwen', icon: getLobeIcon('Qwen.Color', 24) },
  ]

  const heroStats = [
    {
      end: 500,
      suffix: '+',
      label: t('Integrated AI Models'),
    },
    {
      end: 100000,
      suffix: '+',
      label: t('Developers & Enterprises'),
    },
    {
      end: 99.9,
      suffix: '%',
      decimals: 1,
      label: t('Enterprise SLA Availability'),
    },
    {
      end: 150,
      suffix: 'ms',
      label: t('Ultra-low Streaming Latency'),
    },
  ]

  return (
    <section className='relative z-10 overflow-hidden px-4 pt-20 pb-8 sm:px-6 md:pt-24 md:pb-10 lg:pt-28 lg:pb-12'>
      {/* Aurora Atmospheric Glow Background */}
      <div
        aria-hidden
        className='pointer-events-none absolute inset-0 -z-10'
        style={{
          background: [
            'radial-gradient(circle 800px at 70% 30%, rgba(108, 40, 246, 0.35) 0%, transparent 70%)',
            'radial-gradient(circle 600px at 20% 20%, rgba(147, 51, 234, 0.25) 0%, transparent 70%)',
            'radial-gradient(ellipse 1000px 500px at 50% 90%, rgba(59, 130, 246, 0.15) 0%, transparent 75%)',
          ].join(', '),
        }}
      />

      {/* Cybernetic Tech Grid Matrix */}
      <div
        aria-hidden
        className='pointer-events-none absolute inset-0 -z-10 bg-[linear-gradient(to_right,rgba(219,172,255,0.06)_1px,transparent_1px),linear-gradient(to_bottom,rgba(219,172,255,0.06)_1px,transparent_1px)] bg-[size:3.5rem_3.5rem] [mask-image:radial-gradient(ellipse_80%_60%_at_50%_40%,black_30%,transparent_100%)]'
      />

      {/* Subtle Aurora Wave Ribbon at the bottom left */}
      <div
        aria-hidden
        className='pointer-events-none absolute -bottom-24 -left-32 -z-10 h-96 w-96 rounded-full bg-gradient-to-tr from-purple-600/30 via-indigo-500/20 to-transparent blur-3xl'
      />

      <div className='mx-auto max-w-7xl'>
        {/* Main 2-column Hero Layout */}
        <div className='grid grid-cols-1 items-center gap-12 lg:grid-cols-12 lg:gap-8'>
          {/* Left Column: Headlines & Actions */}
          <div className='flex flex-col items-start text-left lg:col-span-7 xl:col-span-6'>
            {/* Top Pill Badge */}
            <div className='landing-animate-fade-up inline-flex items-center gap-2 rounded-full border border-purple-500/20 bg-purple-500/10 px-4 py-1.5 text-xs font-semibold text-purple-700 shadow-sm backdrop-blur-md md:text-sm dark:border-purple-400/30 dark:bg-purple-500/10 dark:text-purple-200 dark:shadow-[0_0_20px_rgba(108,40,246,0.3)]'>
              <Sparkles className='size-3.5 text-purple-600 dark:text-purple-300' />
              <span>
                {t('Enterprise AI Model Gateway · Deployment Choice')}
              </span>
            </div>

            {/* Impactful Heading */}
            <h1 className='landing-animate-fade-up mt-5 text-[clamp(1.85rem,4.5vw,3.25rem)] font-black leading-[1.18] tracking-tight text-slate-900 dark:text-white'>
              <span className='block'>{t('Aggregate 500+ AI Models')}</span>
              <span className='mt-1.5 block bg-gradient-to-r from-[#6C28F6] via-[#9810FA] to-[#ec4899] bg-clip-text text-transparent drop-shadow-sm dark:from-[#7776ff] dark:via-[#b475ff] dark:to-[#ec4899]'>
                {t('Powering Enterprise AI Operations')}
              </span>
            </h1>

            {/* Description */}
            <p className='landing-animate-fade-up mt-6 max-w-xl text-base leading-relaxed text-slate-600 sm:text-lg dark:text-white/70'>
              {t(
                'Standard OpenAI, Claude & Gemini cross-protocol translation. Multi-tenant quota management, smart load balancing and enterprise compliance auditing in one platform.'
              )}
            </p>

            {/* Action Buttons */}
            <div className='landing-animate-fade-up mt-8 flex flex-wrap items-center gap-4'>
              <Button
                className='group inline-flex h-12 items-center justify-center gap-2 rounded-xl bg-gradient-to-r from-[#6C28F6] via-[#8130f8] to-[#9810FA] px-7 text-base font-bold text-white shadow-[0_8px_28px_rgba(108,40,246,0.45)] transition-all duration-300 hover:scale-[1.03] hover:shadow-[0_12px_36px_rgba(108,40,246,0.65)] hover:brightness-110'
                render={
                  <Link
                    to={props.isAuthenticated ? '/dashboard' : '/sign-up'}
                  />
                }
              >
                <span>
                  {props.isAuthenticated
                    ? t('Go to Dashboard')
                    : t('Get Started Now')}
                </span>
                <ArrowUpRight className='size-5 transition-transform duration-200 group-hover:translate-x-0.5 group-hover:-translate-y-0.5' />
              </Button>

              <Button
                variant='outline'
                className='inline-flex h-12 items-center justify-center rounded-xl border border-slate-300/80 bg-white/80 px-6 text-base font-semibold text-slate-800 backdrop-blur-md transition-all duration-200 hover:bg-slate-100 hover:text-slate-950 dark:border-purple-500/30 dark:bg-purple-500/10 dark:text-white/90 dark:hover:border-purple-400/50 dark:hover:bg-purple-500/20 dark:hover:text-white'
                render={<Link to='/pricing' />}
              >
                <span>{t('Explore Models')}</span>
              </Button>

              {renderDocsButton()}
            </div>

            {/* Supported Applications Ecosystem */}
            <div className='landing-animate-fade-up mt-10 w-full max-w-xl'>
              <span className='text-[11px] font-bold tracking-[0.16em] text-slate-500 uppercase dark:text-purple-300/60'>
                {t('Supported Applications')}
              </span>
              <div className='mt-3 flex flex-wrap items-center gap-3'>
                <a
                  href='https://cherry-ai.com'
                  target='_blank'
                  rel='noopener noreferrer'
                  className='group flex items-center gap-2.5 rounded-full border border-slate-200 bg-white/80 px-4 py-2 text-xs font-medium text-slate-700 shadow-sm backdrop-blur-md transition-all duration-300 hover:scale-105 hover:border-purple-400/40 hover:text-purple-700 dark:border-purple-500/20 dark:bg-purple-950/20 dark:text-white/80 dark:hover:border-purple-400/40 dark:hover:bg-purple-900/30 dark:hover:text-white'
                >
                  <CherryStudio.Color size={20} className='shrink-0' />
                  <span>Cherry Studio</span>
                </a>

                <a
                  href='https://ccswitch.io'
                  target='_blank'
                  rel='noopener noreferrer'
                  className='group flex items-center gap-2.5 rounded-full border border-slate-200 bg-white/80 px-4 py-2 text-xs font-medium text-slate-700 shadow-sm backdrop-blur-md transition-all duration-300 hover:scale-105 hover:border-purple-400/40 hover:text-purple-700 dark:border-purple-500/20 dark:bg-purple-950/20 dark:text-white/80 dark:hover:border-purple-400/40 dark:hover:bg-purple-900/30 dark:hover:text-white'
                >
                  <img
                    src='https://ccswitch.io/favicon.png'
                    alt='CC Switch'
                    className='size-5 shrink-0 rounded-md object-contain'
                    onError={(e) => {
                      e.currentTarget.style.display = 'none'
                      const fallback = e.currentTarget
                        .nextSibling as HTMLElement
                      if (fallback) fallback.style.display = 'flex'
                    }}
                  />
                  <span
                    style={{ display: 'none' }}
                    className='size-5 shrink-0 items-center justify-center rounded-md bg-blue-500/20 text-[10px] font-bold text-blue-300'
                  >
                    CC
                  </span>
                  <span>CC Switch</span>
                </a>

                <div className='flex items-center gap-2 rounded-full border border-slate-200 bg-white/60 px-4 py-2 text-xs font-medium text-slate-500 backdrop-blur-md dark:border-purple-500/20 dark:bg-purple-950/20 dark:text-white/60'>
                  <span>NextChat</span>
                </div>

                <div className='flex items-center gap-2 rounded-full border border-slate-200 bg-white/60 px-4 py-2 text-xs font-medium text-slate-500 backdrop-blur-md dark:border-purple-500/20 dark:bg-purple-950/20 dark:text-white/60'>
                  <span>Chatbox</span>
                </div>
              </div>
            </div>
          </div>

          {/* Right Column: 3D Holographic AI Gateway Showcase */}
          <div className='relative flex w-full items-center justify-center lg:col-span-5 xl:col-span-6'>
            {/* Ambient Radial Spotlight behind 3D render */}
            <div
              aria-hidden
              className='pointer-events-none absolute -inset-4 -z-10 rounded-full bg-gradient-to-r from-purple-500/20 via-violet-400/15 to-pink-400/10 blur-3xl dark:from-purple-600/30 dark:via-violet-500/25 dark:to-pink-500/15'
            />

            {/* Central 3D Visual Composition Container */}
            <div className='relative w-full max-w-[560px]'>
              {/* Floating Top HUD Card */}
              <div className='animate-bounce-subtle absolute -top-3 left-4 z-20 flex items-center gap-2.5 rounded-full border border-purple-200/80 bg-white/90 px-4 py-1.5 shadow-lg backdrop-blur-xl dark:border-purple-400/25 dark:bg-[#0e0822]/85'>
                <span className='size-2 rounded-full bg-emerald-500 shadow-[0_0_8px_#10b981] animate-pulse dark:bg-emerald-400 dark:shadow-[0_0_8px_#34d399]' />
                <span className='text-xs font-medium text-slate-800 dark:text-white/90'>
                  {t('Smart Multi-Route Scheduling · Auto-Failover Enabled')}
                </span>
              </div>

              {/* 3D AI Gateway Neural Core */}
              <div className='relative transition-transform duration-700 hover:scale-[1.02]'>
                {/* Light Mode 3D Render */}
                <img
                  src='/hero-ai-hub-light.webp'
                  alt='AI Gateway Architecture'
                  className='block h-auto w-full object-contain drop-shadow-[0_20px_40px_rgba(108,40,246,0.15)] dark:hidden'
                />
                {/* Dark Mode 3D Render */}
                <img
                  src='/hero-ai-hub.webp'
                  alt='AI Gateway Architecture'
                  className='hidden h-auto w-full object-contain drop-shadow-[0_20px_40px_rgba(108,40,246,0.35)] dark:block'
                />
              </div>

              {/* Floating Model Brand Dock at Bottom */}
              <div className='absolute -bottom-4 left-1/2 z-20 flex -translate-x-1/2 items-center gap-3.5 rounded-2xl border border-slate-200/90 bg-white/95 px-5 py-2.5 shadow-xl backdrop-blur-2xl transition-all duration-300 hover:border-purple-400/40 hover:bg-white dark:border-purple-400/20 dark:bg-[#0f0925]/85 dark:shadow-[0_12px_36px_rgba(0,0,0,0.5)] dark:hover:bg-[#0f0925]/95'>
                {modelEcosystem.map((m) => (
                  <div
                    key={m.name}
                    title={m.name}
                    className='group relative flex items-center justify-center transition-transform duration-200 hover:scale-125'
                  >
                    {m.icon}
                  </div>
                ))}
              </div>
            </div>
          </div>
        </div>

        {/* Integrated Stats Bar at the Bottom of the Hero Stage */}
        <div className='mt-12 border-t border-slate-200/80 pt-8 sm:mt-16 md:pt-10 dark:border-purple-500/15'>
          <div className='grid grid-cols-2 gap-6 sm:grid-cols-4 sm:gap-8'>
            {heroStats.map((s) => (
              <div
                key={s.label}
                className='flex flex-col items-center text-center'
              >
                <div
                  className='text-3xl font-black tracking-tight text-slate-900 sm:text-4xl lg:text-[2.6rem] dark:text-white'
                  style={{
                    fontFamily:
                      'Audiowide, ui-monospace, SFMono-Regular, Menlo, monospace',
                  }}
                >
                  <Counter
                    end={s.end}
                    suffix={s.suffix}
                    decimals={s.decimals}
                  />
                </div>
                <div className='mt-2 text-xs font-semibold text-slate-500 sm:text-sm dark:text-white/60'>
                  {s.label}
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  )
}

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
import { ShieldCheck, Users, Zap } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { useSystemConfig } from '@/hooks/use-system-config'

type AuthLayoutProps = {
  children: React.ReactNode
}

export function AuthLayout({ children }: AuthLayoutProps) {
  const { t } = useTranslation()
  const { systemName, logo, loading } = useSystemConfig()

  const valueProps = [
    {
      icon: <Zap className='size-5 text-purple-400' />,
      title: t('5-Minute Rapid Integration'),
      description: t(
        'Standard OpenAI & Anthropic protocol compatibility for instant model routing.'
      ),
    },
    {
      icon: <Users className='size-5 text-indigo-400' />,
      title: t('Multi-Tier Governance'),
      description: t(
        'Fine-grained quota controls and role-based permissions across organizations.'
      ),
    },
    {
      icon: <ShieldCheck className='size-5 text-emerald-400' />,
      title: t('Enterprise Compliance & Audit'),
      description: t(
        'Comprehensive request tracing, rate limiting, and content guardrails.'
      ),
    },
  ]

  const modelChips = ['OpenAI', 'Claude', 'DeepSeek', 'Gemini', 'Qwen']

  return (
    <div className='relative flex min-h-svh w-full flex-col lg:flex-row'>
      {/* Left Branding Showcase (Desktop) */}
      <div className='relative hidden w-full lg:flex lg:w-1/2 flex-col justify-between overflow-hidden border-r border-purple-500/15 bg-gradient-to-br from-[#0b0718] via-[#120a26] to-[#1a0e38] p-12 text-white xl:p-16'>
        {/* Glow Effects */}
        <div
          aria-hidden
          className='pointer-events-none absolute -top-24 -left-24 h-96 w-96 rounded-full bg-purple-600/30 blur-3xl'
        />
        <div
          aria-hidden
          className='pointer-events-none absolute -bottom-24 -right-24 h-96 w-96 rounded-full bg-indigo-600/20 blur-3xl'
        />
        <div
          aria-hidden
          className='absolute inset-0 bg-[linear-gradient(to_right,rgba(255,255,255,0.04)_1px,transparent_1px),linear-gradient(to_bottom,rgba(255,255,255,0.04)_1px,transparent_1px)] bg-[size:3.5rem_3.5rem]'
        />

        {/* Top Header */}
        <div className='relative z-10'>
          <Link
            to='/'
            className='inline-flex items-center gap-3 transition-opacity hover:opacity-85'
          >
            <div className='relative size-10'>
              {loading ? (
                <Skeleton className='absolute inset-0 rounded-full bg-white/20' />
              ) : (
                <img
                  src={logo}
                  alt={t('Logo')}
                  className='size-10 rounded-xl border border-white/20 object-cover shadow-lg'
                />
              )}
            </div>
            {loading ? (
              <Skeleton className='h-7 w-28 bg-white/20' />
            ) : (
              <span className='text-2xl font-bold tracking-tight text-white'>
                {systemName}
              </span>
            )}
          </Link>
          <div className='mt-3 inline-flex items-center gap-1.5 rounded-full border border-purple-400/30 bg-purple-500/15 px-3 py-1 text-xs font-medium text-purple-300'>
            <span>{t('Enterprise AI Management Platform')}</span>
          </div>
        </div>

        {/* Value Propositions */}
        <div className='relative z-10 my-auto py-10 space-y-6'>
          <div>
            <h2 className='text-3xl font-extrabold tracking-tight text-white xl:text-4xl'>
              {t('The Central Hub for')}
              <br />
              <span className='bg-gradient-to-r from-purple-300 via-violet-200 to-indigo-300 bg-clip-text text-transparent'>
                {t('Enterprise AI Acceleration')}
              </span>
            </h2>
            <p className='mt-3 max-w-lg text-sm leading-relaxed text-purple-200/70'>
              {t(
                'Aggregate 500+ AI foundation models with enterprise-grade security, routing, and cost control.'
              )}
            </p>
          </div>

          <div className='space-y-4 pt-2'>
            {valueProps.map((item) => (
              <div
                key={item.title}
                className='flex items-start gap-4 rounded-xl border border-white/10 bg-white/[0.04] p-4 backdrop-blur-md transition-all duration-300 hover:border-purple-400/30 hover:bg-white/[0.07]'
              >
                <div className='flex size-10 shrink-0 items-center justify-center rounded-lg border border-purple-500/30 bg-purple-500/20'>
                  {item.icon}
                </div>
                <div>
                  <h3 className='text-sm font-semibold text-white'>
                    {item.title}
                  </h3>
                  <p className='mt-1 text-xs leading-relaxed text-purple-200/70'>
                    {item.description}
                  </p>
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* Footer Chips */}
        <div className='relative z-10 flex flex-wrap items-center gap-2 pt-4 border-t border-white/10 text-xs text-purple-200/60'>
          <span className='mr-1'>{t('Supported Ecosystem')}:</span>
          {modelChips.map((name) => (
            <span
              key={name}
              className='rounded-md border border-white/10 bg-white/5 px-2.5 py-1 text-[11px] font-medium text-purple-200/80'
            >
              {name}
            </span>
          ))}
        </div>
      </div>

      {/* Right Column: Form Container */}
      <div className='relative flex flex-1 flex-col items-center justify-center p-6 sm:p-10 lg:p-12'>
        {/* Subtle background glow */}
        <div
          aria-hidden
          className='pointer-events-none absolute inset-0 -z-10 opacity-30 dark:opacity-20'
          style={{
            background:
              'radial-gradient(ellipse 60% 60% at 50% 50%, rgba(108, 40, 246, 0.25) 0%, transparent 70%)',
          }}
        />

        {/* Top Mobile Brand Bar */}
        <div className='mb-6 flex w-full max-w-md items-center justify-between lg:hidden'>
          <Link
            to='/'
            className='flex items-center gap-2.5 transition-opacity hover:opacity-85'
          >
            <div className='relative size-8'>
              {loading ? (
                <Skeleton className='absolute inset-0 rounded-full' />
              ) : (
                <img
                  src={logo}
                  alt={t('Logo')}
                  className='size-8 rounded-lg object-cover'
                />
              )}
            </div>
            {loading ? (
              <Skeleton className='h-6 w-24' />
            ) : (
              <span className='text-lg font-bold tracking-tight'>
                {systemName}
              </span>
            )}
          </Link>
        </div>

        {/* Glassmorphic Form Card */}
        <div className='w-full max-w-md rounded-2xl border border-purple-500/20 bg-card/85 p-6 shadow-2xl backdrop-blur-xl transition-all sm:p-8 dark:border-purple-500/25 dark:bg-[#151027]/90 dark:shadow-[0_20px_50px_-15px_rgba(108,40,246,0.3)]'>
          {children}
        </div>
      </div>
    </div>
  )
}

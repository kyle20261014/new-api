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
import { Menu, X, Monitor, ChevronRight } from 'lucide-react'
import { useState, useCallback, useEffect } from 'react'
import { useTranslation } from 'react-i18next'

import { LanguageSwitcher } from '@/components/language-switcher'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'

export interface MowanHeaderProps {
  activeSection?: string
  onNavigate?: (sectionId: string) => void
}

export function MowanHeader(props: MowanHeaderProps) {
  const { t } = useTranslation()
  const { auth } = useAuthStore()
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false)
  const [scrolled, setScrolled] = useState(false)

  const isAuthenticated = !!auth.user

  useEffect(() => {
    const handleScroll = () => {
      setScrolled(window.scrollY > 20)
    }
    window.addEventListener('scroll', handleScroll, { passive: true })
    return () => window.removeEventListener('scroll', handleScroll)
  }, [])

  const handleNavClick = useCallback(
    (e: React.MouseEvent<HTMLAnchorElement>, sectionId: string) => {
      e.preventDefault()
      setMobileMenuOpen(false)
      if (props.onNavigate) {
        props.onNavigate(sectionId)
      } else {
        const el = document.querySelector<HTMLElement>(`#${sectionId}`)
        if (el) {
          el.scrollIntoView({ behavior: 'smooth' })
        }
      }
    },
    [props]
  )

  return (
    <header className={`xc-header ${scrolled ? 'xc-solid' : ''}`}>
      {/* Brand Logo & Name */}
      <a
        href='#hero'
        onClick={(e) => handleNavClick(e, 'hero')}
        className='xc-brand'
        aria-label={t('mowan.brand.name', '魔丸AI')}
      >
        <img
          src='/images/mowan/mowan-icon.png'
          alt=''
          className='size-8 object-contain'
        />
        <span>{t('mowan.brand.name', '魔丸AI')}</span>
      </a>

      {/* Mobile Menu Hamburger (beside Brand in mobile, matching reference) */}
      <Button
        variant='ghost'
        size='icon'
        type='button'
        onClick={() => setMobileMenuOpen((prev) => !prev)}
        aria-label={
          mobileMenuOpen
            ? t('mowan.nav.closeMenu', '关闭菜单')
            : t('mowan.nav.openMenu', '打开菜单')
        }
        aria-expanded={mobileMenuOpen}
        className='h-8 w-8 text-slate-300 hover:bg-white/10 hover:text-white max-[1100px]:flex min-[1101px]:hidden'
      >
        {mobileMenuOpen ? (
          <X className='size-5' />
        ) : (
          <Menu className='size-5' />
        )}
      </Button>

      {/* Desktop Navigation Links */}
      <nav className='xc-menu' aria-label={t('mowan.nav.label', '主要导航')}>
        <a
          href='#models'
          onClick={(e) => handleNavClick(e, 'models')}
          className={props.activeSection === 'models' ? 'active' : ''}
        >
          {t('mowan.nav.models', '模型')}
        </a>
        <a
          href='#create'
          onClick={(e) => handleNavClick(e, 'create')}
          className={props.activeSection === 'create' ? 'active' : ''}
        >
          {t('mowan.nav.create', '开始创作')}
        </a>
        <a
          href='#inspiration'
          onClick={(e) => handleNavClick(e, 'inspiration')}
          className={props.activeSection === 'inspiration' ? 'active' : ''}
        >
          {t('mowan.nav.inspiration', '灵感')}
        </a>
        <Link to='/dashboard'>{t('mowan.nav.api', 'API开放平台')}</Link>
      </nav>

      {/* Right Section: Language, Download Client, Account */}
      <div className='xc-actions'>
        <div className='hidden sm:block'>
          <LanguageSwitcher />
        </div>

        {/* Download Client CTA pill */}
        <a
          href='#create'
          onClick={(e) => handleNavClick(e, 'create')}
          className='hidden items-center gap-1.5 rounded-full border border-cyan-500/40 bg-cyan-950/30 px-3.5 py-1 text-xs font-medium text-cyan-300 transition-all hover:border-cyan-400 hover:bg-cyan-900/50 hover:text-cyan-200 sm:inline-flex'
        >
          <Monitor className='size-3.5' />
          <span>{t('mowan.nav.client', '下载客户端')}</span>
        </a>

        {/* User Auth Buttons or Profile */}
        {isAuthenticated ? (
          <div className='flex items-center gap-2'>
            <Button
              variant='outline'
              size='sm'
              className='hidden h-8 rounded-full border-white/20 bg-white/5 text-xs text-white hover:bg-white/10 sm:inline-flex'
              render={<Link to='/dashboard' />}
            >
              {t('mowan.nav.console', '控制台')}
            </Button>
            <ProfileDropdown />
          </div>
        ) : (
          <div className='flex items-center gap-2'>
            <Button
              variant='ghost'
              size='sm'
              className='h-8 rounded-full px-3 text-xs font-medium text-slate-300 hover:bg-white/10 hover:text-white'
              render={<Link to='/sign-in' />}
            >
              {t('mowan.nav.signIn', '登录')}
            </Button>
            <Button
              size='sm'
              className='h-8 rounded-full bg-cyan-500 px-3.5 text-xs font-medium text-slate-950 shadow-sm shadow-cyan-500/30 hover:bg-cyan-400'
              render={<Link to='/sign-up' />}
            >
              {t('mowan.nav.signUp', '免费注册')}
            </Button>
          </div>
        )}
      </div>

      {/* Mobile Dropdown Drawer Menu */}
      {mobileMenuOpen && (
        <div className='xc-mobile-menu absolute top-full right-0 left-0 border-b border-white/10 bg-[#0b0d14]/95 px-4 pt-3 pb-6 shadow-2xl backdrop-blur-xl max-[1100px]:block min-[1101px]:hidden'>
          <nav className='flex flex-col space-y-1'>
            <a
              href='#hero'
              onClick={(e) => handleNavClick(e, 'hero')}
              className='flex items-center justify-between rounded-lg px-3 py-2.5 text-base font-medium text-slate-200 hover:bg-white/10 hover:text-white'
            >
              <span>{t('mowan.nav.home', '首页')}</span>
              <ChevronRight className='size-4 text-slate-500' />
            </a>
            <a
              href='#create'
              onClick={(e) => handleNavClick(e, 'create')}
              className='flex items-center justify-between rounded-lg px-3 py-2.5 text-base font-medium text-slate-200 hover:bg-white/10 hover:text-white'
            >
              <span>{t('mowan.nav.create', '开始创作')}</span>
              <ChevronRight className='size-4 text-slate-500' />
            </a>
            <a
              href='#models'
              onClick={(e) => handleNavClick(e, 'models')}
              className='flex items-center justify-between rounded-lg px-3 py-2.5 text-base font-medium text-slate-200 hover:bg-white/10 hover:text-white'
            >
              <span>{t('mowan.nav.models', '模型')}</span>
              <ChevronRight className='size-4 text-slate-500' />
            </a>
            <a
              href='#voice'
              onClick={(e) => handleNavClick(e, 'voice')}
              className='flex items-center justify-between rounded-lg px-3 py-2.5 text-base font-medium text-slate-200 hover:bg-white/10 hover:text-white'
            >
              <span>{t('mowan.nav.voice', '语音合成')}</span>
              <ChevronRight className='size-4 text-slate-500' />
            </a>
            <a
              href='#inspiration'
              onClick={(e) => handleNavClick(e, 'inspiration')}
              className='flex items-center justify-between rounded-lg px-3 py-2.5 text-base font-medium text-slate-200 hover:bg-white/10 hover:text-white'
            >
              <span>{t('mowan.nav.inspiration', '灵感')}</span>
              <ChevronRight className='size-4 text-slate-500' />
            </a>
            <Link
              to='/dashboard'
              onClick={() => setMobileMenuOpen(false)}
              className='flex items-center justify-between rounded-lg px-3 py-2.5 text-base font-medium text-slate-200 hover:bg-white/10 hover:text-white'
            >
              <span>{t('mowan.nav.api', 'API开放平台')}</span>
              <ChevronRight className='size-4 text-slate-500' />
            </Link>
          </nav>

          <div className='mt-4 flex flex-col gap-3 border-t border-white/10 pt-4'>
            <div className='flex items-center justify-between px-3'>
              <span className='text-xs text-slate-400'>
                {t('common.language', '语言')}
              </span>
              <LanguageSwitcher />
            </div>

            {!isAuthenticated && (
              <div className='grid grid-cols-2 gap-2 px-3 pt-2'>
                <Button
                  variant='outline'
                  size='sm'
                  className='rounded-full border-white/20 bg-white/5 text-xs text-white'
                  render={<Link to='/sign-in' />}
                >
                  {t('mowan.nav.signIn', '登录')}
                </Button>
                <Button
                  size='sm'
                  className='rounded-full bg-cyan-500 text-xs font-medium text-slate-950'
                  render={<Link to='/sign-up' />}
                >
                  {t('mowan.nav.signUp', '免费注册')}
                </Button>
              </div>
            )}
          </div>
        </div>
      )}
    </header>
  )
}

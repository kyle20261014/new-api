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
import fs from 'node:fs'
import path from 'node:path'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import i18next from 'i18next'
import { useState } from 'react'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import * as THREE from 'three'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { useMinimumLoadingTime } from '@/hooks/use-minimum-loading-time'
import i18n from '@/i18n/config'
import enLocale from '@/i18n/locales/en.json'
import frLocale from '@/i18n/locales/fr.json'
import jaLocale from '@/i18n/locales/ja.json'
import ruLocale from '@/i18n/locales/ru.json'
import viLocale from '@/i18n/locales/vi.json'
import zhTWLocale from '@/i18n/locales/zh-TW.json'
import zhLocale from '@/i18n/locales/zh.json'

import { CreateSection } from '../components/mowan-landing/create-section'
import { INSPIRATION_GALLERY } from '../components/mowan-landing/data'
import { FooterSection } from '../components/mowan-landing/footer-section'
import { ParticleRibbon } from '../components/mowan-landing/loader-particle-ribbon'
import { createParticleScene } from '../components/mowan-landing/particle-scene'
import { VoiceSection } from '../components/mowan-landing/voice-section'
import * as hooks from '../hooks'
import { Home } from '../index'

const mockToast = vi.fn()
vi.mock('sonner', () => ({
  toast: (...args: unknown[]) => mockToast(...args),
}))

vi.mock('@/lib/lobe-icon', () => ({
  getLobeIcon: (name: string, size?: number) => (
    <span
      data-testid={`lobe-icon-${name}`}
      className='xc-model-icon'
      style={{ width: size, height: size }}
    />
  ),
  getLobeIconNames: () => [],
}))

const globalMockDispose = vi.fn()
const globalMockRender = vi.fn()

vi.mock('three', async (importOriginal) => {
  const actual = await importOriginal<typeof import('three')>()
  return {
    ...actual,
    WebGLRenderer: class MockWebGLRenderer {
      domElement: HTMLCanvasElement
      debug = {}
      constructor(params?: { canvas?: HTMLCanvasElement }) {
        this.domElement = params?.canvas || document.createElement('canvas')
      }
      getContext() {
        return { getExtension: () => null }
      }
      setPixelRatio() {}
      getPixelRatio() {
        return 1
      }
      setSize() {}
      setClearColor() {}
      dispose() {
        globalMockDispose()
      }
      render(scene: THREE.Scene) {
        globalMockRender(scene)
      }
      capabilities = { isWebGL2: true }
    },
  }
})

vi.mock('three/examples/jsm/postprocessing/EffectComposer.js', () => ({
  EffectComposer: class MockEffectComposer {
    renderer: { render: (scene: THREE.Scene, camera: THREE.Camera) => void }
    passes: Array<{ scene?: THREE.Scene; camera?: THREE.Camera }> = []
    constructor(renderer: {
      render: (scene: THREE.Scene, camera: THREE.Camera) => void
    }) {
      this.renderer = renderer
    }
    addPass(pass: { scene?: THREE.Scene; camera?: THREE.Camera }) {
      this.passes.push(pass)
    }
    render() {
      const renderPass = this.passes.find((p) => p && p.scene)
      if (renderPass && renderPass.scene && renderPass.camera) {
        this.renderer.render(renderPass.scene, renderPass.camera)
      }
    }
    dispose() {}
  },
}))

vi.mock('three/examples/jsm/postprocessing/UnrealBloomPass.js', () => ({
  UnrealBloomPass: class MockUnrealBloomPass {
    dispose() {}
  },
}))

vi.mock('three/examples/jsm/postprocessing/OutputPass.js', () => ({
  OutputPass: class MockOutputPass {
    dispose() {}
  },
}))

vi.mock('../hooks', () => ({
  useHomePageContent: vi.fn(),
}))

function renderWithProviders(
  ui: React.ReactElement,
  options?: { i18nInstance?: typeof i18n }
) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={options?.i18nInstance || i18n}>
        {ui}
      </I18nextProvider>
    </QueryClientProvider>
  )
}

function createTestRouter(homeComponent = Home) {
  const rootRoute = createRootRoute({
    component: Outlet,
  })
  const homeRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: homeComponent,
  })
  const signInRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/sign-in',
    component: () => <div>Sign In Page</div>,
  })
  const signUpRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/sign-up',
    component: () => <div>Sign Up Page</div>,
  })
  const consoleRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/console',
    component: () => <div>Console Page</div>,
  })

  return createRouter({
    routeTree: rootRoute.addChildren([
      homeRoute,
      signInRoute,
      signUpRoute,
      consoleRoute,
      createRoute({
        getParentRoute: () => rootRoute,
        path: '/dashboard',
        component: () => <div>Dashboard Page</div>,
      }),
    ]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
}

async function createIsolatedI18n() {
  const instance = i18next.createInstance()
  await instance.use(initReactI18next).init({
    lng: 'zh',
    fallbackLng: 'en',
    resources: {
      zh: zhLocale,
      en: enLocale,
      'zh-TW': zhTWLocale,
      fr: frLocale,
      ja: jaLocale,
      ru: ruLocale,
      vi: viLocale,
    },
    interpolation: { escapeValue: false },
  })
  return instance
}

describe('Home Page & Mowan AI Landing Feature', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockToast.mockClear()
    globalMockDispose.mockClear()
    globalMockRender.mockClear()
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
    if (typeof window !== 'undefined' && window.HTMLElement) {
      Object.defineProperty(window.HTMLElement.prototype, 'scrollIntoView', {
        writable: true,
        configurable: true,
        value: vi.fn(),
      })
    }
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  test('renders default Mowan AI landing page with all 5 sections when no custom content is configured', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })

    const router = createTestRouter()
    renderWithProviders(<RouterProvider router={router} />)

    // Brand & Logo
    const brandLogos = await screen.findAllByAltText(/魔丸AI|Mowan AI/i)
    expect(brandLogos.length).toBeGreaterThan(0)
    expect(brandLogos[0]).toHaveAttribute('src', '/images/mowan/mowan-icon.png')

    // Hero Section
    const heroEntries = screen.getAllByText(/一个入口/i)
    expect(heroEntries.length).toBeGreaterThan(0)
    expect(screen.getByText(/让想象连接/i)).toBeInTheDocument()
    const createTexts = screen.getAllByText(/开始创作/i)
    expect(createTexts.length).toBeGreaterThan(0)
    const whatItDoesTexts = screen.getAllByText(/看看它能做什么/i)
    expect(whatItDoesTexts.length).toBeGreaterThan(0)

    // Create Section (02)
    expect(screen.getByText(/说出你想要的/i)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: /进入工作台，写下你的灵感/i })
    ).toBeInTheDocument()
    expect(screen.getByText('✦')).toBeInTheDocument()

    // Models Section (03)
    expect(screen.getByText(/不同的想法，各有答案/i)).toBeInTheDocument()

    // Voice Section (04)
    expect(screen.getByText(/声音，也能被创造/i)).toBeInTheDocument()

    // Inspiration Section (05)
    expect(screen.getByText(/灵感从不止于想象/i)).toBeInTheDocument()

    // Desktop section navigation dots
    expect(screen.getByLabelText(/分节导航/i)).toBeInTheDocument()
  })

  test('supports interactive placeholder feedback on create capsule and category clicks', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })

    const router = createTestRouter()
    renderWithProviders(<RouterProvider router={router} />)

    // Click on create section capsule button
    const capsuleBtn = await screen.findByRole('button', {
      name: /进入工作台，写下你的灵感/i,
    })
    fireEvent.click(capsuleBtn)

    // Placeholder mode feedback appears
    expect(
      await screen.findByText(/功能即将推出，敬请期待！/i)
    ).toBeInTheDocument()
  })

  test('shows an accessible standalone loader without navigation while homepage content is pending', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: false,
      isUrl: false,
    })

    const router = createTestRouter()
    renderWithProviders(<RouterProvider router={router} />)

    expect(await screen.findByText(/Loading\.\.\./i)).toBeInTheDocument()
    expect(screen.queryByRole('banner')).not.toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByText('魔丸AI')).toBeInTheDocument()
    expect(screen.getByTestId('mowan-loader-stage')).toBeInTheDocument()
    expect(screen.getByTestId('mowan-loader-ribbon-back')).toBeInTheDocument()
    expect(screen.getByTestId('mowan-loader-ribbon-front')).toBeInTheDocument()
    expect(screen.queryByText(/让想象连接/i)).not.toBeInTheDocument()
  })

  test.each([
    { reduced: false, duration: 350 },
    { reduced: true, duration: 350 },
  ])(
    'prepares the homepage under the loader and unlocks it after $duration ms exit with reduced motion=$reduced',
    async ({ reduced, duration }) => {
      vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
      vi.spyOn(window, 'matchMedia').mockImplementation(
        (query) =>
          ({
            matches: reduced,
            media: query,
            addEventListener: vi.fn(),
            removeEventListener: vi.fn(),
          }) as unknown as MediaQueryList
      )
      const originalOverflow = document.body.style.overflow
      const contentState = { content: '', isLoaded: false, isUrl: false }
      vi.mocked(hooks.useHomePageContent).mockImplementation(() => contentState)
      let refresh = () => {}
      function HomeHarness() {
        const [, setRevision] = useState(0)
        refresh = () => setRevision((value) => value + 1)
        return <Home />
      }
      const router = createTestRouter(HomeHarness)
      try {
        const { container, unmount } = renderWithProviders(
          <RouterProvider router={router} />
        )
        await act(async () => {
          await router.load()
        })
        expect(screen.getByRole('status')).toBeInTheDocument()
        expect(document.body.style.overflow).toBe('hidden')
        act(() => {
          vi.advanceTimersByTime(100)
          contentState.isLoaded = true
          refresh()
        })
        const landing = container.querySelector('.mowan-landing')
        expect(landing).toBeInTheDocument()
        expect(landing?.parentElement).toHaveAttribute('inert')
        expect(landing?.parentElement).toHaveAttribute('aria-hidden', 'true')
        expect(
          screen.queryByRole('heading', { level: 1 })
        ).not.toBeInTheDocument()
        act(() => vi.advanceTimersByTime(900))
        expect(screen.getByRole('status')).toBeInTheDocument()
        act(() => vi.advanceTimersByTime(duration - 1))
        expect(screen.getByRole('status')).toBeInTheDocument()
        act(() => vi.advanceTimersByTime(1))
        expect(screen.queryByRole('status')).not.toBeInTheDocument()
        expect(screen.getByRole('heading', { level: 1 })).toBeInTheDocument()
        expect(landing?.parentElement).not.toHaveAttribute('inert')
        expect(document.body.style.overflow).toBe(originalOverflow)
        expect(
          screen.queryByTestId('mowan-loader-stage')
        ).not.toBeInTheDocument()
        unmount()
      } finally {
        cleanup()
        vi.useRealTimers()
      }
    }
  )

  test('forced particle ribbon keeps moving with reduced motion and cleans up on unmount', () => {
    const frames: FrameRequestCallback[] = []
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      frames.push(callback)
      return frames.length
    })
    vi.spyOn(performance, 'now').mockReturnValue(0)
    vi.spyOn(window, 'matchMedia').mockReturnValue({
      matches: true,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    } as unknown as MediaQueryList)
    const mockCtx = {
      setTransform: vi.fn(),
      clearRect: vi.fn(),
      beginPath: vi.fn(),
      moveTo: vi.fn(),
      lineTo: vi.fn(),
      stroke: vi.fn(),
      arc: vi.fn(),
      fill: vi.fn(),
    }
    const getContextSpy = vi
      .spyOn(HTMLCanvasElement.prototype, 'getContext')
      .mockReturnValue(mockCtx as unknown as RenderingContext)
    const cancelSpy = vi.spyOn(window, 'cancelAnimationFrame')

    const { getByTestId, unmount } = render(
      <ParticleRibbon>
        <span data-testid='center-logo'>LOGO</span>
      </ParticleRibbon>
    )

    expect(getByTestId('mowan-loader-stage')).toBeInTheDocument()
    expect(getByTestId('mowan-loader-ribbon-back')).toBeInTheDocument()
    expect(getByTestId('mowan-loader-ribbon-front')).toBeInTheDocument()
    expect(getByTestId('center-logo')).toBeInTheDocument()

    expect(frames).toHaveLength(1)
    act(() => frames[0](0))
    expect(mockCtx.arc.mock.calls.length).toBeGreaterThan(100)
    const initialPositions = [...mockCtx.arc.mock.calls]
    mockCtx.arc.mockClear()
    act(() => frames[1](800))
    expect(mockCtx.arc.mock.calls).not.toEqual(initialPositions)
    expect(mockCtx.stroke).toHaveBeenCalled()

    expect(frames).toHaveLength(3)

    unmount()
    expect(cancelSpy).toHaveBeenCalledWith(3)
    cancelSpy.mockRestore()
    getContextSpy.mockRestore()
  })

  test.each([
    { requestTime: 100, remaining: 900 },
    { requestTime: 1000, remaining: 0 },
    { requestTime: 1500, remaining: 0 },
  ])(
    'loading waits only for the remainder of 1000ms minimum display time after a $requestTime ms request without compounding',
    ({ requestTime, remaining }) => {
      vi.useFakeTimers()
      try {
        const { result, rerender, unmount } = renderHook(
          ({ loading }) => useMinimumLoadingTime(loading, 1000),
          { initialProps: { loading: true } }
        )
        act(() => vi.advanceTimersByTime(requestTime))
        expect(result.current).toBe(true)
        rerender({ loading: false })
        if (remaining > 0) {
          act(() => vi.advanceTimersByTime(remaining - 1))
          expect(result.current).toBe(true)
          act(() => vi.advanceTimersByTime(1))
        }
        expect(result.current).toBe(false)
        unmount()
        expect(vi.getTimerCount()).toBe(0)
      } finally {
        vi.useRealTimers()
      }
    }
  )

  test('preserves custom URL iframe branch when admin configures external URL', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: 'https://example.com/custom-home',
      isLoaded: true,
      isUrl: true,
    })

    const router = createTestRouter()
    renderWithProviders(<RouterProvider router={router} />)

    const iframe = await screen.findByTitle(/Custom Home Page/i)
    expect(iframe).toBeInTheDocument()
    expect(iframe).toHaveAttribute('src', 'https://example.com/custom-home')
    expect(screen.queryByText(/让想象连接/i)).not.toBeInTheDocument()
  })

  test('preserves custom Markdown content branch when configured', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '# 欢迎来到自定义首页\n这是管理员配置的 Markdown 内容。',
      isLoaded: true,
      isUrl: false,
    })

    const router = createTestRouter()
    renderWithProviders(<RouterProvider router={router} />)

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: /欢迎来到自定义首页/i,
      })
    ).toBeInTheDocument()
    expect(
      screen.getByText(/这是管理员配置的 Markdown 内容。/i)
    ).toBeInTheDocument()
    expect(screen.queryByText(/让想象连接/i)).not.toBeInTheDocument()
  })

  test('toggles mobile menu and provides safe navigation without external reference hops', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })

    const router = createTestRouter()
    const { container } = renderWithProviders(
      <RouterProvider router={router} />
    )
    await screen.findByRole('heading', { level: 1 })

    // 1. Initial collapsed state
    const openBtn = screen.getByLabelText(/打开菜单/i)
    expect(openBtn).toHaveAttribute('aria-expanded', 'false')

    // 2. Click to open drawer
    fireEvent.click(openBtn)
    const closeBtn = screen.getByLabelText(/关闭菜单/i)
    expect(closeBtn).toHaveAttribute('aria-expanded', 'true')

    // 3. Click nav link inside mobile drawer closes drawer and resets aria-expanded
    const mobileMenu = container.querySelector('.xc-mobile-menu')
    expect(mobileMenu).toBeInTheDocument()
    const modelsLink = within(mobileMenu as HTMLElement).getByRole('link', {
      name: /模型/i,
    })
    fireEvent.click(modelsLink)
    expect(screen.getByLabelText(/打开菜单/i)).toHaveAttribute(
      'aria-expanded',
      'false'
    )
  })

  test('API platform links open the dashboard from desktop and mobile navigation', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })
    const router = createTestRouter()
    const navigationI18n = await createIsolatedI18n()
    renderWithProviders(<RouterProvider router={router} />, {
      i18nInstance: navigationI18n,
    })
    const apiName = navigationI18n.t('mowan.nav.api')
    expect(await screen.findByRole('link', { name: apiName })).toHaveAttribute(
      'href',
      '/dashboard'
    )
    fireEvent.click(screen.getByLabelText(/打开菜单/i))
    const apiLinks = screen.getAllByRole('link', { name: apiName })
    expect(apiLinks).toHaveLength(2)
    for (const link of apiLinks) {
      expect(link).toHaveAttribute('href', '/dashboard')
    }
    fireEvent.click(apiLinks[1])
    expect(await screen.findByText('Dashboard Page')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/dashboard')
  })

  test('validates inspiration gallery dataset contract: strictly 40 slots, exactly 8 original replacements at precise indices', () => {
    expect(INSPIRATION_GALLERY).toHaveLength(40)

    const expectedOriginalSlots = [
      {
        index: 0,
        id: 'item-0',
        replacement: 'neon-portrait.webp',
        width: 640,
        height: 1137,
      },
      {
        index: 3,
        id: 'item-3',
        replacement: 'lunar-cat.webp',
        width: 640,
        height: 960,
      },
      {
        index: 6,
        id: 'item-6',
        replacement: 'celestial-temple.webp',
        width: 640,
        height: 1127,
      },
      {
        index: 12,
        id: 'item-12',
        replacement: 'cosmic-explorer.webp',
        width: 640,
        height: 349,
      },
      {
        index: 24,
        id: 'item-24',
        replacement: 'flame-phoenix.webp',
        width: 640,
        height: 1128,
      },
      {
        index: 25,
        id: 'item-25',
        replacement: 'coastal-museum.webp',
        width: 640,
        height: 1147,
      },
      {
        index: 31,
        id: 'item-31',
        replacement: 'glass-koi.webp',
        width: 640,
        height: 640,
      },
      {
        index: 37,
        id: 'item-37',
        replacement: 'crystal-butterfly.webp',
        width: 640,
        height: 640,
      },
    ]

    expectedOriginalSlots.forEach((slot) => {
      const card = INSPIRATION_GALLERY[slot.index]
      expect(card).toBeDefined()
      expect(card.id).toBe(slot.id)
      expect(card.generated).toBe(true)
      expect(card.replacement).toBe(slot.replacement)
      expect(card.width).toBe(slot.width)
      expect(card.height).toBe(slot.height)
    })

    const originalCount = INSPIRATION_GALLERY.filter((c) => c.generated).length
    expect(originalCount).toBe(8)
  })

  test('renders inspiration gallery into 5 balanced columns of 8 cards in desktop viewport', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })

    window.innerWidth = 1440
    window.innerHeight = 900

    const router = createTestRouter()
    const { container } = renderWithProviders(
      <RouterProvider router={router} />
    )

    await screen.findByText(/灵感从不止于想象/i)

    const columns = container.querySelectorAll('.xc-pubu-column')
    expect(columns).toHaveLength(5)
    columns.forEach((col) => {
      const cards = col.querySelectorAll('.xc-pubu-card')
      expect(cards).toHaveLength(8)
    })

    // Gallery cards are interactive buttons with accessible names and placeholder feedback
    const allCards =
      container.querySelectorAll<HTMLButtonElement>('.xc-pubu-card')
    expect(allCards).toHaveLength(40)
    allCards.forEach((card) => {
      expect(card.tagName).toBe('BUTTON')
      expect(card.getAttribute('aria-label')).toBeTruthy()
    })

    fireEvent.click(allCards[0])
    expect(await screen.findByRole('status')).toBeInTheDocument()
  })

  test('adapts inspiration gallery column counts and balanced cards across responsive viewports', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })

    // 1. Mobile viewport (390px): 2 columns of 20 cards
    window.innerWidth = 390
    window.innerHeight = 844

    const mobileRouter = createTestRouter()
    const { container: mobileContainer, unmount: unmountMobile } =
      renderWithProviders(<RouterProvider router={mobileRouter} />)
    await screen.findByText(/灵感从不止于想象/i)

    const mobileColumns = mobileContainer.querySelectorAll('.xc-pubu-column')
    expect(mobileColumns).toHaveLength(2)
    mobileColumns.forEach((col) => {
      expect(col.querySelectorAll('.xc-pubu-card')).toHaveLength(20)
    })
    unmountMobile()

    // 2. Wide desktop viewport (1920px): 6 columns of 6 cards = 36 cards
    window.innerWidth = 1920
    window.innerHeight = 1080

    const wideRouter = createTestRouter()
    const { container: wideContainer } = renderWithProviders(
      <RouterProvider router={wideRouter} />
    )
    await screen.findByText(/灵感从不止于想象/i)

    const wideColumns = wideContainer.querySelectorAll('.xc-pubu-column')
    expect(wideColumns).toHaveLength(6)
    wideColumns.forEach((col) => {
      expect(col.querySelectorAll('.xc-pubu-card')).toHaveLength(6)
    })
  })

  test('handles wheel scrolling, target offsets, cosine easing, and user interrupt cancellation', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })

    window.innerHeight = 800
    window.scrollY = 0

    const matchMediaMock = vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }))
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      configurable: true,
      value: matchMediaMock,
    })

    Object.defineProperty(document.documentElement, 'scrollHeight', {
      value: 5000,
      configurable: true,
    })

    const cancelAnimSpy = vi.spyOn(window, 'cancelAnimationFrame')

    const router = createTestRouter()
    const { container } = renderWithProviders(
      <RouterProvider router={router} />
    )
    await screen.findByRole('heading', { level: 1 })

    const sections = [
      ...container.querySelectorAll<HTMLElement>('[data-xc-section]'),
      container.querySelector<HTMLElement>('.xc-footer'),
    ].filter(Boolean) as HTMLElement[]

    sections.forEach((sec, i) => {
      Object.defineProperty(sec, 'offsetHeight', {
        value: 800,
        configurable: true,
      })
      vi.spyOn(sec, 'getBoundingClientRect').mockReturnValue({
        top: i * 800,
        bottom: (i + 1) * 800,
        left: 0,
        right: 1440,
        width: 1440,
        height: 800,
        x: 0,
        y: i * 800,
        toJSON: () => {},
      })
    })

    const landingContainer = container.querySelector('.mowan-landing')
    expect(landingContainer).toBeInTheDocument()

    // 1. Dispatch wheel downwards from hero section
    const wheelDown = new WheelEvent('wheel', {
      deltaY: 100,
      bubbles: true,
      cancelable: true,
    })
    landingContainer?.dispatchEvent(wheelDown)
    expect(wheelDown.defaultPrevented).toBe(true)

    // 2. User interrupt via pointerdown calls cancelAnimationFrame
    window.dispatchEvent(new Event('pointerdown'))
    expect(cancelAnimSpy).toHaveBeenCalled()

    // 3. Jitter threshold: tiny deltaY (< 5) is ignored
    const jitterWheel = new WheelEvent('wheel', {
      deltaY: 2,
      bubbles: true,
      cancelable: true,
    })
    landingContainer?.dispatchEvent(jitterWheel)
    expect(jitterWheel.defaultPrevented).toBe(false)
  })

  test('forced effects render particles and smoothly scroll even with reduced motion', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })

    window.innerHeight = 800
    window.scrollY = 0
    vi.spyOn(document.documentElement, 'scrollHeight', 'get').mockReturnValue(
      4800
    )

    const scrollToSpy = vi
      .spyOn(window, 'scrollTo')
      .mockImplementation(() => {})
    const matchMediaMock = vi.fn().mockImplementation((query: string) => ({
      matches: query.includes('prefers-reduced-motion'),
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }))
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      configurable: true,
      value: matchMediaMock,
    })

    const motionI18n = await createIsolatedI18n()
    const router = createTestRouter()
    const { container } = renderWithProviders(
      <RouterProvider router={router} />,
      { i18nInstance: motionI18n }
    )
    await screen.findByRole('heading', { level: 1 })
    await waitFor(() =>
      expect(container.querySelector('.xc-beijing canvas')).toHaveClass(
        'xc-ready'
      )
    )

    const sections = [
      ...container.querySelectorAll<HTMLElement>('[data-xc-section]'),
      container.querySelector<HTMLElement>('.xc-footer'),
    ].filter(Boolean) as HTMLElement[]

    sections.forEach((sec, i) => {
      Object.defineProperty(sec, 'offsetHeight', {
        value: 800,
        configurable: true,
      })
      vi.spyOn(sec, 'getBoundingClientRect').mockReturnValue({
        top: i * 800,
        bottom: (i + 1) * 800,
        left: 0,
        right: 1440,
        width: 1440,
        height: 800,
        x: 0,
        y: i * 800,
        toJSON: () => {},
      })
    })

    const frames: FrameRequestCallback[] = []
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      frames.push(callback)
      return frames.length
    })
    vi.spyOn(performance, 'now').mockReturnValue(0)
    scrollToSpy.mockClear()

    const landingContainer = container.querySelector('.mowan-landing')
    const wheelDown = new WheelEvent('wheel', {
      deltaY: 100,
      bubbles: true,
      cancelable: true,
    })
    landingContainer?.dispatchEvent(wheelDown)
    expect(wheelDown.defaultPrevented).toBe(true)
    expect(scrollToSpy).not.toHaveBeenCalled()
    expect(frames).toHaveLength(1)
    act(() => frames[0](400))
    expect(scrollToSpy).toHaveBeenLastCalledWith({
      top: expect.closeTo(400),
      behavior: 'instant',
    })
    act(() => frames[1](800))
    expect(scrollToSpy).toHaveBeenLastCalledWith({
      top: 800,
      behavior: 'instant',
    })

    const scrollIntoViewSpy = vi.spyOn(sections[1], 'scrollIntoView')
    fireEvent.click(
      screen.getByRole('button', {
        name: motionI18n.t('mowan.hero.scrollDown'),
      })
    )
    expect(scrollIntoViewSpy).toHaveBeenCalledWith({ behavior: 'smooth' })
  })

  test('supports capability clicks in create section and displays localized status feedback', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })

    const router = createTestRouter()
    const { container } = renderWithProviders(
      <RouterProvider router={router} />
    )
    await screen.findByRole('heading', { level: 1 })

    const createSection = container.querySelector('#create')
    expect(createSection).toBeInTheDocument()

    const capabilityButtons = within(createSection as HTMLElement).getAllByRole(
      'button'
    )
    expect(capabilityButtons.length).toBeGreaterThan(1)

    // Click on capability button
    fireEvent.click(capabilityButtons[1])
    expect(await screen.findByRole('status')).toBeInTheDocument()
  })

  test('verifies header single-layer fixed layout, semantic structure, and hero visual hierarchy', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })

    const router = createTestRouter()
    const { container } = renderWithProviders(
      <RouterProvider router={router} />
    )
    await screen.findByRole('heading', { level: 1 })

    // Header structure
    const header = container.querySelector('header')
    expect(header).toHaveClass('xc-header')
    expect(header?.querySelector('.xc-brand')).toBeInTheDocument()
    expect(header?.querySelector('.xc-menu')).toBeInTheDocument()
    expect(header?.querySelector('.xc-actions')).toBeInTheDocument()

    // Hero section structure & floating chips
    const hero = container.querySelector('#hero')
    expect(hero).toHaveAttribute('data-xc-section', 'hero')
    expect(hero).toHaveClass('xc-section', 'xc-hero')
    expect(hero?.querySelector('.xc-hero-copy')).toBeInTheDocument()
    expect(hero?.querySelector('.xc-orbit')).toBeInTheDocument()
    expect(hero?.querySelector('.xc-scroll')).toBeInTheDocument()

    const floatingChips = hero?.querySelectorAll('.xc-chip.xc-floating')
    expect(floatingChips?.length).toBe(11)
  })

  test('validates 7-language translation completeness for all Mowan AI landing keys', () => {
    const locales = ['zh', 'en', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
    const localesDir = path.resolve(process.cwd(), 'src/i18n/locales')

    const requiredKeys = [
      'mowan.brand.name',
      'mowan.nav.models',
      'mowan.nav.create',
      'mowan.nav.inspiration',
      'mowan.nav.api',
      'mowan.hero.title1',
      'mowan.hero.title2',
      'mowan.hero.subtitle',
      'mowan.hero.cta.start',
      'mowan.hero.cta.explore',
      'mowan.hero.giftPromo',
      'mowan.hero.scrollDown',
      'mowan.inspiration.unlockBtn',
      'mowan.create.feedback',
      'mowan.create.placeholder',
      'mowan.footer.copyright',
      'common.language',
      'mowan.hero.eyebrow',
    ]

    for (const loc of locales) {
      const filePath = path.join(localesDir, `${loc}.json`)
      const raw = fs.readFileSync(filePath, 'utf8')
      const json = JSON.parse(raw)
      const trans = json.translation || {}

      for (const key of requiredKeys) {
        expect(trans[key], `Missing key ${key} in ${loc}.json`).toBeDefined()
        expect(typeof trans[key]).toBe('string')
        expect(trans[key].trim().length).toBeGreaterThan(0)
      }

      // Check all 40 cards' titleKey, tagKey, subKey
      for (const card of INSPIRATION_GALLERY) {
        expect(
          trans[card.titleKey],
          `Missing card titleKey ${card.titleKey} in ${loc}`
        ).toBeDefined()
        expect(
          trans[card.tagKey],
          `Missing card tagKey ${card.tagKey} in ${loc}`
        ).toBeDefined()
        expect(
          trans[card.subKey],
          `Missing card subKey ${card.subKey} in ${loc}`
        ).toBeDefined()
      }
    }
  })

  test('validates mowan.placeholder.notice interpolation across all 7 locales through real i18n runtime', async () => {
    const isolatedI18n = await createIsolatedI18n()
    const supportedLocales = ['zh', 'en', 'zh-TW', 'fr', 'ja', 'ru', 'vi']

    for (const loc of supportedLocales) {
      await isolatedI18n.changeLanguage(loc)

      // 1. CreateSection capability button click
      const { container, unmount } = render(
        <I18nextProvider i18n={isolatedI18n}>
          <CreateSection />
        </I18nextProvider>
      )
      const buttons = container.querySelectorAll('.xc-capabilities button')
      expect(buttons.length).toBeGreaterThan(0)

      fireEvent.click(buttons[0])

      const feedback = await screen.findByRole('status')
      expect(feedback).toBeInTheDocument()
      expect(feedback.textContent).not.toContain('{{')
      expect(feedback.textContent).not.toContain('}}')
      expect(feedback.textContent?.trim().length).toBeGreaterThan(0)
      unmount()

      // 2. Footer link placeholder click
      mockToast.mockClear()
      const { unmount: unmountFooter } = render(
        <I18nextProvider i18n={isolatedI18n}>
          <FooterSection />
        </I18nextProvider>
      )
      const privacyLink = screen.getByRole('link', {
        name: isolatedI18n.t('mowan.footer.privacy'),
      })
      fireEvent.click(privacyLink)

      expect(mockToast).toHaveBeenCalled()
      const toastMessage = String(mockToast.mock.calls[0][0])
      expect(toastMessage).not.toContain('{{')
      expect(toastMessage).not.toContain('}}')
      expect(toastMessage).toContain(isolatedI18n.t('mowan.footer.privacy'))
      unmountFooter()
    }
  })

  test('verifies model marquee keyboard accessibility, duplicate clickability, and pause on focus', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })

    const router = createTestRouter()
    const { container } = renderWithProviders(
      <RouterProvider router={router} />
    )
    await screen.findByRole('heading', { level: 1 })

    const marquee = container.querySelector('.xc-model-marquee')
    expect(marquee).toBeInTheDocument()

    const lanes = container.querySelectorAll('.xc-model-lane')
    expect(lanes.length).toBe(2)

    // Primary group chips are focusable (tabIndex 0)
    const primaryChips = lanes[0].querySelectorAll(
      '.xc-model-group:not([aria-hidden="true"]) button'
    )
    expect(primaryChips.length).toBeGreaterThan(0)
    expect(primaryChips[0]).toHaveAttribute('tabIndex', '0')

    // Duplicate group chips are aria-hidden with tabIndex -1
    const duplicateGroups = container.querySelectorAll(
      '.xc-model-group[aria-hidden="true"]'
    )
    expect(duplicateGroups.length).toBe(2)
    const duplicateChips = duplicateGroups[0].querySelectorAll('button')
    expect(duplicateChips[0]).toHaveAttribute('tabIndex', '-1')
    expect(duplicateChips[0]).toHaveAttribute('aria-hidden', 'true')

    // Duplicate chips open the same model experience as the visible chips.
    fireEvent.click(duplicateChips[0])
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument()
  })

  test('validates particle-scene lifecycle, 3-layer Points generation, shader stages, hidden pause, and disposal', () => {
    let capturedChildren: THREE.Object3D[] = []
    globalMockRender.mockImplementation((scene: THREE.Scene) => {
      capturedChildren = [...scene.children]
    })

    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    Object.defineProperty(document, 'hidden', {
      value: false,
      configurable: true,
      writable: true,
    })

    const originalInnerWidth = window.innerWidth
    const originalInnerHeight = window.innerHeight
    window.innerWidth = 1440
    window.innerHeight = 900

    const canvas = document.createElement('canvas')
    const onFailure = vi.fn()
    const removeEventListenerSpy = vi.spyOn(window, 'removeEventListener')

    const cleanup = createParticleScene(canvas, () => 0, onFailure)
    expect(cleanup).toBeTypeOf('function')

    // 1. Scene has exactly 3 Points layers (main + 2 ambient)
    const pointsLayers = capturedChildren.filter(
      (c) => c instanceof THREE.Points
    ) as THREE.Points[]
    expect(pointsLayers.length).toBe(3)

    // 2. Vertex shaders contain gl_Position and NOT gl_FragColor; Fragment shaders contain gl_FragColor and NOT gl_Position
    for (const pts of pointsLayers) {
      const mat = pts.material as THREE.ShaderMaterial
      expect(mat.vertexShader).toContain('gl_Position')
      expect(mat.vertexShader).not.toContain('gl_FragColor')
      expect(mat.fragmentShader).toContain('gl_FragColor')
      expect(mat.fragmentShader).not.toContain('gl_Position')
    }

    const desktopPillHalfWidth = (
      pointsLayers[0].material as THREE.ShaderMaterial
    ).uniforms.uPillHalfW.value as number
    // Expand an undersized halo; preserve an already-wide large-screen halo.
    for (const [width, height, capsuleWidth, capsuleHeight] of [
      [584, 842, 500, 68],
      [1241, 773, 800, 84],
      [1920, 1080, 800, 84],
    ]) {
      const capsule = document.createElement('button')
      vi.spyOn(capsule, 'getBoundingClientRect').mockReturnValue(
        new DOMRect(42, 387, capsuleWidth, capsuleHeight)
      )
      window.innerWidth = width
      window.innerHeight = height
      const renderCapsule = createParticleScene(
        canvas,
        () => 1,
        onFailure,
        undefined,
        capsule
      )
      try {
        const points = capturedChildren.find(
          (child) => child instanceof THREE.Points
        )
        if (!points) throw new Error('Missing particle layer')
        const outline = points.geometry.getAttribute('aPos1')
        const worldPerPixel = (2 * 22 * Math.tan((25 * Math.PI) / 180)) / height
        let halfWidth = 0
        let halfHeight = 0
        for (let i = 0; i < Math.floor(outline.count * 0.56); i++) {
          halfWidth = Math.max(halfWidth, Math.abs(outline.getX(i)))
          halfHeight = Math.max(halfHeight, Math.abs(outline.getY(i)))
        }
        if (width === 1920) {
          expect((2 * halfWidth) / worldPerPixel).toBeCloseTo(
            (2 * desktopPillHalfWidth) / worldPerPixel,
            -1
          )
        } else {
          expect(
            (2 * halfWidth) / worldPerPixel,
            `Halo width at ${width}x${height}`
          ).toBeCloseTo(capsuleWidth + 24, -1)
          expect(
            (2 * halfHeight) / worldPerPixel,
            `Halo height at ${width}x${height}`
          ).toBeCloseTo(capsuleHeight + 24, -1)
        }
      } finally {
        renderCapsule?.()
      }
    }

    // 3. Document hidden pause: when hidden, renderFrame does not render
    globalMockRender.mockClear()
    Object.defineProperty(document, 'hidden', {
      value: true,
      configurable: true,
      writable: true,
    })
    document.dispatchEvent(new Event('visibilitychange'))
    expect(globalMockRender).not.toHaveBeenCalled()

    // 4. Cleanup and disposal
    if (cleanup) {
      cleanup()
    }
    expect(globalMockDispose).toHaveBeenCalled()
    expect(removeEventListenerSpy).toHaveBeenCalled()
    expect(onFailure).not.toHaveBeenCalled()

    // Restore
    if (originalHidden) {
      Object.defineProperty(document, 'hidden', originalHidden)
    }
    window.innerWidth = originalInnerWidth
    window.innerHeight = originalInnerHeight
  })

  test('translates model tags in models section across locales without hardcoded Chinese', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })

    const testI18n = await createIsolatedI18n()
    await testI18n.changeLanguage('zh')

    const router = createTestRouter()
    const { unmount } = renderWithProviders(
      <RouterProvider router={router} />,
      { i18nInstance: testI18n }
    )
    await screen.findByRole('heading', { level: 1 })

    // Under zh locale, tag displays "推理"
    expect(screen.getAllByText('推理').length).toBeGreaterThan(0)
    expect(screen.getAllByText('旗舰').length).toBeGreaterThan(0)
    unmount()

    // Switch i18n to en
    await testI18n.changeLanguage('en')
    const enRouter = createTestRouter()
    const { unmount: unmountEn } = renderWithProviders(
      <RouterProvider router={enRouter} />,
      { i18nInstance: testI18n }
    )
    await screen.findByRole('heading', { level: 1 })

    // Under en locale, tags display as English translations
    expect(screen.getAllByText('Reasoning').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Flagship').length).toBeGreaterThan(0)
    expect(screen.queryByText('推理')).not.toBeInTheDocument()

    unmountEn()
  })

  test('renders the translated music label when the voice section language changes', async () => {
    const instance = await createIsolatedI18n()
    renderWithProviders(<VoiceSection />, { i18nInstance: instance })

    for (const [locale, label] of [
      ['en', 'Music Generation'],
      ['zh', '音乐生成'],
      ['zh-TW', '音樂生成'],
      ['fr', 'Génération musicale'],
      ['ja', '音楽生成'],
      ['ru', 'Генерация музыки'],
      ['vi', 'Tạo âm nhạc'],
    ]) {
      await act(async () => {
        await instance.changeLanguage(locale)
      })
      expect(screen.getByText(label)).toBeInTheDocument()
    }
  })

  test('forced particles stay active when the motion preference changes and clean up on unmount', async () => {
    vi.mocked(hooks.useHomePageContent).mockReturnValue({
      content: '',
      isLoaded: true,
      isUrl: false,
    })

    let reduced = false
    const motionQuery = new EventTarget()
    Object.defineProperty(motionQuery, 'matches', {
      get: () => reduced,
    })
    vi.spyOn(window, 'matchMedia').mockReturnValue(
      motionQuery as MediaQueryList
    )

    const router = createTestRouter()
    const { container, unmount } = renderWithProviders(
      <RouterProvider router={router} />
    )
    await screen.findByRole('heading', { level: 1 })
    const canvas = container.querySelector('.xc-beijing canvas')
    await waitFor(() => expect(canvas).toHaveClass('xc-ready'))

    const beforeChange = globalMockDispose.mock.calls.length
    act(() => {
      reduced = true
      const change = new Event('change')
      Object.defineProperty(change, 'matches', { value: true })
      motionQuery.dispatchEvent(change)
    })
    expect(canvas).toHaveClass('xc-ready')
    expect(globalMockDispose.mock.calls.length).toBe(beforeChange)

    unmount()
    expect(globalMockDispose.mock.calls.length).toBeGreaterThan(beforeChange)
  })
})

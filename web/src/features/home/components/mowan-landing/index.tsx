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

import { CreateSection } from './create-section'
import { NAV_SECTIONS } from './data'
import { FooterSection } from './footer-section'
import { MowanHeader } from './header'
import { HeroSection } from './hero-section'
import { InspirationSection } from './inspiration-section'
import { ModelsSection } from './models-section'
import { createParticleScene } from './particle-scene'
import { SectionNav } from './section-nav'
import { VoiceSection } from './voice-section'

import './styles.css'

export function MowanLanding() {
  const [activeSection, setActiveSection] = useState('hero')
  const [isParticleReady, setIsParticleReady] = useState(false)

  const containerRef = useRef<HTMLDivElement>(null)
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const progressRef = useRef(0)
  const sphereCenterRef = useRef<{ x: number; y: number } | undefined>(
    undefined
  )
  const animFrameRef = useRef<number>(0)

  // Document title branding with cleanup on unmount
  useEffect(() => {
    const prevTitle = document.title
    document.title = '魔丸 AI - Mowan AI'
    return () => {
      document.title = prevTitle
    }
  }, [])

  // Smooth scroll with cosine ease matching reference site 800ms
  const cancelScrollAnim = useCallback(() => {
    if (animFrameRef.current) {
      cancelAnimationFrame(animFrameRef.current)
      animFrameRef.current = 0
    }
  }, [])

  const smoothScrollTo = useCallback(
    (targetY: number) => {
      cancelScrollAnim()
      const maxScroll = Math.max(
        0,
        document.documentElement.scrollHeight - window.innerHeight
      )
      const boundedY = Math.max(0, Math.min(targetY, maxScroll))
      const startY = window.scrollY

      if (Math.abs(boundedY - startY) < 1) {
        window.scrollTo({ top: boundedY, behavior: 'instant' })
        return
      }

      const startTime = performance.now()
      function step(now: number) {
        const progress = Math.min(1, (now - startTime) / 800)
        const eased = (1 - Math.cos(Math.PI * progress)) / 2
        window.scrollTo({
          top: startY + (boundedY - startY) * eased,
          behavior: 'instant',
        })
        if (progress < 1) {
          animFrameRef.current = requestAnimationFrame(step)
        } else {
          animFrameRef.current = 0
        }
      }
      animFrameRef.current = requestAnimationFrame(step)
    },
    [cancelScrollAnim]
  )

  // Navigate to section
  const handleNavigate = useCallback(
    (sectionId: string) => {
      setActiveSection(sectionId)
      const element = document.querySelector<HTMLElement>(`#${sectionId}`)
      if (element) {
        smoothScrollTo(element.getBoundingClientRect().top + window.scrollY)
      }
    },
    [smoothScrollTo]
  )

  // Update particle layout metrics (sphere center & scroll progress K)
  const updateMetrics = useCallback(() => {
    const container = containerRef.current
    if (!container) return

    const orbit = container.querySelector<HTMLElement>('.xc-orbit')
    if (orbit) {
      const rect = orbit.getBoundingClientRect()
      sphereCenterRef.current = {
        x: ((rect.left + rect.width / 2) / window.innerWidth) * 2 - 1,
        y: 1 - ((rect.top + rect.height / 2) / window.innerHeight) * 2,
      }
    }

    const sections = [
      ...container.querySelectorAll<HTMLElement>('[data-xc-section]'),
    ]
    if (sections.length > 1) {
      const progress = sections
        .slice(1)
        .reduce(
          (acc, sec) =>
            acc +
            Math.min(
              1,
              Math.max(
                0,
                1 - sec.getBoundingClientRect().top / window.innerHeight
              )
            ),
          0
        )
      progressRef.current = progress
    }
  }, [])

  // Initialize WebGL particle scene with real reference shader and parameters
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    let cleanupScene: (() => void) | null = null
    let isMounted = true

    try {
      cleanupScene = createParticleScene(
        canvas,
        () => progressRef.current,
        () => {
          if (isMounted) setIsParticleReady(false)
        },
        () => sphereCenterRef.current,
        containerRef.current?.querySelector<HTMLElement>('.xc-capsule')
      )
      if (cleanupScene) {
        requestAnimationFrame(() => {
          if (isMounted) setIsParticleReady(true)
        })
      }
    } catch {
      // Retain fallback stars background on WebGL initialization failure
    }

    return () => {
      isMounted = false
      cleanupScene?.()
    }
  }, [])

  // Track active section and listen to wheel, scroll, resize
  useEffect(() => {
    const container = containerRef.current
    if (!container) return

    const handleScroll = () => {
      updateMetrics()
      const activeIdx = Math.min(
        NAV_SECTIONS.length - 1,
        Math.max(0, Math.round(progressRef.current))
      )
      if (NAV_SECTIONS[activeIdx]) {
        setActiveSection(NAV_SECTIONS[activeIdx].id)
      }
    }

    // Reference site wheel algorithm with cosine 800ms easing and gallery natural scroll
    const handleWheel = (e: WheelEvent) => {
      if (
        e.ctrlKey ||
        e.metaKey ||
        e.shiftKey ||
        Math.abs(e.deltaX) > Math.abs(e.deltaY) ||
        !e.deltaY ||
        (e.target as HTMLElement)?.closest(
          'input, textarea, select, [contenteditable="true"], details[open]'
        ) ||
        document.body.style.overflow === 'hidden' ||
        document.documentElement.style.overflow === 'hidden' ||
        Math.abs(e.deltaY) < (e.deltaMode ? 1 : 5)
      ) {
        return
      }

      if (animFrameRef.current) {
        e.preventDefault()
        return
      }

      const sections = [
        ...container.querySelectorAll<HTMLElement>('[data-xc-section]'),
        container.querySelector<HTMLElement>('.xc-footer'),
      ].filter(Boolean) as HTMLElement[]

      if (sections.length === 0) return

      const offsets = sections.map(
        (el) => el.getBoundingClientRect().top + window.scrollY
      )
      const dir = e.deltaY > 0 ? 1 : -1
      let currentIdx = 0
      for (let i = 0; i < offsets.length; i++) {
        if (offsets[i] <= window.scrollY + 80) currentIdx = i
      }

      // Natural free scrolling exception for gallery (index 4)
      if (
        currentIdx >= 4 &&
        (dir > 0 || currentIdx > 4 || window.scrollY > offsets[4] + 8)
      ) {
        return
      }

      const currentBottom =
        offsets[currentIdx] + sections[currentIdx].offsetHeight
      let targetY: number

      if (
        dir > 0 &&
        currentBottom - window.scrollY - window.innerHeight > 140
      ) {
        targetY = Math.min(
          window.scrollY + window.innerHeight * 0.8,
          currentBottom - window.innerHeight
        )
      } else if (dir < 0 && window.scrollY - offsets[currentIdx] > 140) {
        targetY = Math.max(
          offsets[currentIdx],
          window.scrollY - window.innerHeight * 0.8
        )
      } else {
        const nextIdx = Math.max(
          0,
          Math.min(sections.length - 1, currentIdx + dir)
        )
        if (nextIdx === currentIdx) return
        targetY = offsets[nextIdx]
      }

      e.preventDefault()
      smoothScrollTo(targetY)
    }

    // Cancel smooth scroll animation on explicit user interaction
    const handleUserInterrupt = () => {
      cancelScrollAnim()
    }

    container.addEventListener('wheel', handleWheel, { passive: false })
    window.addEventListener('scroll', handleScroll, { passive: true })
    window.addEventListener('resize', handleScroll, { passive: true })
    window.addEventListener('pointerdown', handleUserInterrupt, {
      passive: true,
    })
    window.addEventListener('touchstart', handleUserInterrupt, {
      passive: true,
    })
    window.addEventListener('keydown', handleUserInterrupt)

    const resizeObserver = new ResizeObserver(handleScroll)
    resizeObserver.observe(container)

    handleScroll()

    return () => {
      cancelScrollAnim()
      container.removeEventListener('wheel', handleWheel)
      window.removeEventListener('scroll', handleScroll)
      window.removeEventListener('resize', handleScroll)
      window.removeEventListener('pointerdown', handleUserInterrupt)
      window.removeEventListener('touchstart', handleUserInterrupt)
      window.removeEventListener('keydown', handleUserInterrupt)
      resizeObserver.disconnect()
    }
  }, [cancelScrollAnim, smoothScrollTo, updateMetrics])

  return (
    <div
      ref={containerRef}
      className='mowan-landing min-h-screen text-slate-100 selection:bg-cyan-500/30 selection:text-cyan-200'
    >
      {/* Fixed Fullscreen Single Canvas WebGL Particle Scene Background */}
      <div className='xc-beijing' aria-hidden='true'>
        <canvas ref={canvasRef} className={isParticleReady ? 'xc-ready' : ''} />
      </div>

      {/* Fixed Header */}
      <MowanHeader activeSection={activeSection} onNavigate={handleNavigate} />

      {/* Main Content Sections */}
      <main id='main-content'>
        <HeroSection
          onExploreClick={() => handleNavigate('models')}
          onCreateClick={() => handleNavigate('create')}
        />
        <CreateSection />
        <ModelsSection onExploreClick={() => handleNavigate('create')} />
        <VoiceSection onCreateClick={() => handleNavigate('create')} />
        <InspirationSection />
      </main>

      {/* Full Page Footer with Support FAB */}
      <FooterSection />

      {/* Desktop Floating Section Navigation Dots */}
      <SectionNav
        activeSection={activeSection}
        onSectionClick={handleNavigate}
      />
    </div>
  )
}

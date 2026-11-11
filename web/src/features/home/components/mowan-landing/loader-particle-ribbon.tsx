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
import { useEffect, useRef } from 'react'

import { cn } from '@/lib/utils'

export interface ParticleRibbonProps {
  children?: React.ReactNode
  className?: string
}

interface RibbonParticle {
  angle: number
  radius: number
  height: number
  speed: number
  size: number
  tailLength: number
  r: number
  g: number
  b: number
  alpha: number
  twinkleSpeed: number
  twinklePhase: number
}

const CORE_RADIUS = 53
const RIBBON_WIDTH_SCALE = 1.2
const ROTATION_SPEED_SCALE = 1.5

function createPrng(seed = 1337) {
  let s = seed
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0
    return s / 4294967296
  }
}

function randGaussian(rng: () => number) {
  let u = 0
  let v = 0
  while (u === 0) u = rng()
  while (v === 0) v = rng()
  return Math.sqrt(-2.0 * Math.log(u)) * Math.cos(2.0 * Math.PI * v)
}

function generateParticles(): RibbonParticle[] {
  const rng = createPrng(1337)
  const particles: RibbonParticle[] = []

  // Stream 1: Core high-density stardust ribbon (330 particles)
  for (let i = 0; i < 330; i++) {
    const angle = rng() * Math.PI * 2
    const radius = CORE_RADIUS + randGaussian(rng) * 2.8
    const height = randGaussian(rng) * 1.6
    const speed = 0.95 + (rng() - 0.5) * 0.15
    const size = 0.75 + rng() * 0.75
    const tailLength = 2.5 + rng() * 2.0
    const colorRoll = rng()
    let r = 79
    let g = 209
    let b = 255
    if (colorRoll < 0.3) {
      r = 240
      g = 249
      b = 255
    } else if (colorRoll < 0.5) {
      r = 91
      g = 124
      b = 255
    }
    particles.push({
      angle,
      radius,
      height,
      speed,
      size,
      tailLength,
      r,
      g,
      b,
      alpha: 0.45 + rng() * 0.5,
      twinkleSpeed: 2.0 + rng() * 3.0,
      twinklePhase: rng() * Math.PI * 2,
    })
  }

  // Stream 2: Outer filament companion trail (150 particles)
  for (let i = 0; i < 150; i++) {
    const angle = rng() * Math.PI * 2
    const radius = 63 + randGaussian(rng) * 3.6
    const height = randGaussian(rng) * 2.2
    const speed = 0.78 + (rng() - 0.5) * 0.12
    const size = 0.6 + rng() * 0.6
    const tailLength = 2.0 + rng() * 1.8
    const colorRoll = rng()
    let r = 91
    let g = 124
    let b = 255
    if (colorRoll < 0.4) {
      r = 79
      g = 209
      b = 255
    } else if (colorRoll < 0.6) {
      r = 155
      g = 107
      b = 255
    }
    particles.push({
      angle,
      radius,
      height,
      speed,
      size,
      tailLength,
      r,
      g,
      b,
      alpha: 0.3 + rng() * 0.45,
      twinkleSpeed: 1.5 + rng() * 2.5,
      twinklePhase: rng() * Math.PI * 2,
    })
  }

  // Stream 3: Inner filament accent glow (60 particles)
  for (let i = 0; i < 60; i++) {
    const angle = rng() * Math.PI * 2
    const radius = 45 + randGaussian(rng) * 2.0
    const height = randGaussian(rng) * 1.4
    const speed = 1.12 + (rng() - 0.5) * 0.18
    const size = 0.6 + rng() * 0.5
    const tailLength = 1.8 + rng() * 1.5
    const colorRoll = rng()
    let r = 79
    let g = 209
    let b = 255
    if (colorRoll < 0.3) {
      r = 255
      g = 255
      b = 255
    } else if (colorRoll < 0.5) {
      r = 251
      g = 191
      b = 36
    }
    particles.push({
      angle,
      radius,
      height,
      speed,
      size,
      tailLength,
      r,
      g,
      b,
      alpha: 0.35 + rng() * 0.35,
      twinkleSpeed: 2.2 + rng() * 3.5,
      twinklePhase: rng() * Math.PI * 2,
    })
  }

  return particles
}

const PARTICLES = generateParticles()

// 3D Orbital Projection constants
const TILT_ALPHA = 1.1519 // ~66 deg around X axis
const COS_ALPHA = Math.cos(TILT_ALPHA)
const SIN_ALPHA = Math.sin(TILT_ALPHA)

const INCLINE_BETA = -0.3491 // -20 deg in screen plane
const COS_BETA = Math.cos(INCLINE_BETA)
const SIN_BETA = Math.sin(INCLINE_BETA)

const CSS_SIZE = 176

export function ParticleRibbon(props: ParticleRibbonProps) {
  const backCanvasRef = useRef<HTMLCanvasElement>(null)
  const frontCanvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const backCanvas = backCanvasRef.current
    const frontCanvas = frontCanvasRef.current
    if (!backCanvas || !frontCanvas) return

    const backCtx = backCanvas.getContext('2d')
    const frontCtx = frontCanvas.getContext('2d')
    if (!backCtx || !frontCtx) return

    const dpr = Math.min(window.devicePixelRatio || 1, 2)
    const pixelSize = CSS_SIZE * dpr

    backCanvas.width = pixelSize
    backCanvas.height = pixelSize
    frontCanvas.width = pixelSize
    frontCanvas.height = pixelSize

    const cx = CSS_SIZE / 2
    const cy = CSS_SIZE / 2

    const render = (time: number) => {
      backCtx.setTransform(dpr, 0, 0, dpr, 0, 0)
      frontCtx.setTransform(dpr, 0, 0, dpr, 0, 0)

      backCtx.clearRect(0, 0, CSS_SIZE, CSS_SIZE)
      frontCtx.clearRect(0, 0, CSS_SIZE, CSS_SIZE)

      backCtx.globalCompositeOperation = 'lighter'
      frontCtx.globalCompositeOperation = 'lighter'

      for (let i = 0; i < PARTICLES.length; i++) {
        const p = PARTICLES[i]
        const theta = p.angle + p.speed * time * ROTATION_SPEED_SCALE

        // Widen the ribbon around its core orbit without scaling the logo.
        const radius =
          CORE_RADIUS + (p.radius - CORE_RADIUS) * RIBBON_WIDTH_SCALE
        const x0 = radius * Math.cos(theta)
        const y0 = p.height * RIBBON_WIDTH_SCALE
        const z0 = -radius * Math.sin(theta)

        // 3D Tilt around X axis
        const x1 = x0
        const y1 = y0 * COS_ALPHA - z0 * SIN_ALPHA
        const z1 = y0 * SIN_ALPHA + z0 * COS_ALPHA

        // Screen plane inclination
        const sx = cx + x1 * COS_BETA - y1 * SIN_BETA
        const sy = cy + x1 * SIN_BETA + y1 * COS_BETA
        const sz = z1

        // Tangent vector for micro-tail
        const tx0 = -Math.sin(theta)
        const tz0 = -Math.cos(theta)

        const tx1 = tx0
        const ty1 = -tz0 * SIN_ALPHA

        const screenTx = tx1 * COS_BETA - ty1 * SIN_BETA
        const screenTy = tx1 * SIN_BETA + ty1 * COS_BETA
        const len = Math.hypot(screenTx, screenTy) || 1
        const ntx = screenTx / len
        const nty = screenTy / len

        // Dynamic twinkle
        const twinkle =
          0.72 + 0.28 * Math.sin(time * p.twinkleSpeed + p.twinklePhase)
        const finalAlpha = Math.min(1, Math.max(0, p.alpha * twinkle))
        const strokeAlpha = (finalAlpha * 0.55).toFixed(3)
        const fillAlpha = finalAlpha.toFixed(3)

        // Target back or front canvas based on orbital depth
        const ctx = sz < 0 ? backCtx : frontCtx

        // Render micro-tail
        ctx.beginPath()
        ctx.moveTo(sx - ntx * p.tailLength, sy - nty * p.tailLength)
        ctx.lineTo(sx, sy)
        ctx.strokeStyle = `rgba(${p.r},${p.g},${p.b},${strokeAlpha})`
        ctx.lineWidth = p.size * 0.8
        ctx.stroke()

        // Render stardust head
        ctx.beginPath()
        ctx.arc(sx, sy, p.size * 0.7, 0, Math.PI * 2)
        ctx.fillStyle = `rgba(${p.r},${p.g},${p.b},${fillAlpha})`
        ctx.fill()
      }
    }

    const mediaQuery = window.matchMedia?.('(prefers-reduced-motion: reduce)')
    let isReducedMotion = mediaQuery?.matches ?? false
    let animId = 0
    const startTime = performance.now()

    const loop = (now: number) => {
      const elapsed = (now - startTime) / 1000
      render(elapsed)
      if (!isReducedMotion) {
        animId = requestAnimationFrame(loop)
      }
    }

    if (isReducedMotion) {
      render(0)
    } else {
      animId = requestAnimationFrame(loop)
    }

    const handleMotionChange = (e: MediaQueryListEvent) => {
      isReducedMotion = e.matches
      cancelAnimationFrame(animId)
      if (isReducedMotion) {
        render(0)
      } else {
        animId = requestAnimationFrame(loop)
      }
    }

    mediaQuery?.addEventListener?.('change', handleMotionChange)

    return () => {
      cancelAnimationFrame(animId)
      mediaQuery?.removeEventListener?.('change', handleMotionChange)
    }
  }, [])

  return (
    <div
      className={cn(
        'relative flex size-44 items-center justify-center',
        props.className
      )}
      data-testid='mowan-loader-stage'
    >
      <canvas
        ref={backCanvasRef}
        aria-hidden='true'
        className='pointer-events-none absolute inset-0 size-full'
        data-testid='mowan-loader-ribbon-back'
      />
      {props.children}
      <canvas
        ref={frontCanvasRef}
        aria-hidden='true'
        className='pointer-events-none absolute inset-0 z-20 size-full'
        data-testid='mowan-loader-ribbon-front'
      />
    </div>
  )
}

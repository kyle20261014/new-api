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
import {
  AdditiveBlending,
  BufferAttribute,
  BufferGeometry,
  Color,
  PerspectiveCamera,
  Points,
  ShaderMaterial,
  Scene,
  Vector2,
  Vector3,
  WebGLRenderer,
} from 'three'
import { EffectComposer } from 'three/examples/jsm/postprocessing/EffectComposer.js'
import { OutputPass } from 'three/examples/jsm/postprocessing/OutputPass.js'
import type { Pass } from 'three/examples/jsm/postprocessing/Pass.js'
import { RenderPass } from 'three/examples/jsm/postprocessing/RenderPass.js'
import { UnrealBloomPass } from 'three/examples/jsm/postprocessing/UnrealBloomPass.js'

export interface SphereCenter {
  x: number
  y: number
}

export interface ParticleConfig {
  星球中心: [number, number, number]
  星球半径: number
  胶囊半宽: number
  胶囊半高: number
  星云半径: number
  波形半宽: number
  波形下移: number
  漩涡半径: number
}

// ----------------------------------------------------------------------------
// GLSL Shaders
// ----------------------------------------------------------------------------
const mainVertexShader = `// ============================================================================\r
//  白猪AI · 主粒子系统顶点着色器\r
//  单个 Points 对象，在 5 组目标坐标之间按 uProgress(0→4) 连续 lerp 变形：\r
//    0 星球/星系  →  1 胶囊输入框  →  2 噪点云  →  3 波形带  →  4 漩涡/黑洞\r
// ============================================================================\r
precision highp float;\r
\r
#define PI 3.141592653589793\r
\r
uniform float uTime;\r
uniform float uProgress;      // 0..4，由 ScrollTrigger scrub 驱动\r
uniform float uSize;          // 基础点大小\r
uniform float uDpr;\r
uniform vec3  uMouse;         // 世界坐标下的指针位置\r
uniform float uMouseStrength; // 斥力强度\r
uniform vec2  uParallax;      // hero 星球 ±5° 视差\r
uniform vec3  uSphereCenter;  // 星球中心（横屏偏右）\r
uniform float uPillHalfW;     // 胶囊半宽，内部流动粒子取模用\r
uniform float uWaveBoost;     // 播放按钮 hover 时增大波形振幅\r
uniform float uWaveOffsetY;   // 波形带整体下移，避开标题文案\r
uniform float uIntro;         // 入场：1 = 完全散开，0 = 汇聚成形\r
\r
// 形态 0 复用内置的 position 属性，省一组 buffer\r
attribute vec3  aPos1;\r
attribute vec3  aPos2;\r
attribute vec3  aPos3;\r
attribute vec3  aPos4;\r
attribute vec3  aRandom;\r
attribute vec4  aInfo;   // x:球体类型(0核心/1壳层/2轨道环) y:环半径 z:环角度 w:环编号\r
attribute vec4  aInfo2;  // x:胶囊内部标记 y:波形归一化x z:漩涡半径 w:漩涡角度\r
attribute float aScale;\r
\r
varying vec3  vColor;\r
varying float vAlpha;\r
\r
// ---- 主色板 ---------------------------------------------------------------\r
const vec3 C_CYAN   = vec3(0.310, 0.820, 1.000); // #4FD1FF\r
const vec3 C_BLUE   = vec3(0.357, 0.486, 1.000); // #5B7CFF\r
const vec3 C_PURPLE = vec3(0.608, 0.420, 1.000); // #9B6BFF\r
const vec3 C_PINK   = vec3(1.000, 0.420, 0.839); // #FF6BD6\r
const vec3 C_ORANGE = vec3(1.000, 0.702, 0.361); // #FFB35C\r
\r
mat2 rot(float a) { float s = sin(a), c = cos(a); return mat2(c, -s, s, c); }\r
\r
float hash11(float p) {\r
  p = fract(p * 0.1031);\r
  p *= p + 33.33;\r
  p *= p + p;\r
  return fract(p);\r
}\r
\r
float noise11(float x) {\r
  float i = floor(x), f = fract(x);\r
  f = f * f * (3.0 - 2.0 * f);\r
  return mix(hash11(i), hash11(i + 1.0), f);\r
}\r
\r
// 五段渐变：青 → 蓝 → 紫 → 粉 → 橙\r
vec3 ramp5(float t) {\r
  t = clamp(t, 0.0, 1.0) * 4.0;\r
  if (t < 1.0) return mix(C_CYAN,   C_BLUE,   t);\r
  if (t < 2.0) return mix(C_BLUE,   C_PURPLE, t - 1.0);\r
  if (t < 3.0) return mix(C_PURPLE, C_PINK,   t - 2.0);\r
  return              mix(C_PINK,   C_ORANGE, t - 3.0);\r
}\r
\r
// 轨道环的倾角（按环编号给不同姿态）\r
vec3 applyTilt(vec3 p, float id) {\r
  float ax =  0.52 + id * 0.40;\r
  float az = -0.30 + id * 0.55;\r
  p.yz = rot(ax) * p.yz;\r
  p.xy = rot(az) * p.xy;\r
  return p;\r
}\r
\r
// ---- 形态 0：星球 / 星系 ---------------------------------------------------\r
vec3 animSphere(vec3 p) {\r
  if (aInfo.x > 1.5) {\r
    // 轨道环：在自身平面内公转（约 30s/圈），再套用倾角\r
    float r = aInfo.y;\r
    float a = aInfo.z + uTime * (0.21 + aInfo.w * 0.045);\r
    vec3 q = vec3(cos(a) * r, aRandom.y * 0.10, sin(a) * r);\r
    p = applyTilt(q, aInfo.w);\r
  } else {\r
    // 核心与壳层：整体缓慢自转 + 轻微呼吸\r
    p.xz = rot(uTime * 0.055) * p.xz;\r
    p += aRandom * 0.07 * sin(uTime * 0.7 + aRandom.x * 12.0);\r
  }\r
  // 鼠标视差 ±5°\r
  p.yz = rot(uParallax.y) * p.yz;\r
  p.xz = rot(uParallax.x) * p.xz;\r
  return p + uSphereCenter;\r
}\r
\r
// ---- 形态 1：胶囊输入框 ----------------------------------------------------\r
vec3 animPill(vec3 p) {\r
  if (aInfo2.x > 0.5) {\r
    // 内部稀疏粒子与周围浮尘：沿 x 轴循环流动\r
    float w = uPillHalfW * (aInfo2.x > 1.5 ? 1.4 : 1.0);\r
    float speed = aInfo2.x > 1.5 ? 0.35 : 0.85;\r
    p.x = mod(p.x + uTime * speed + w, 2.0 * w) - w;\r
    p.y += sin(uTime * 1.3 + aRandom.z * 9.0) * 0.05;\r
  } else {\r
    // 外沿：只做极小抖动，保持轮廓锐利\r
    p += aRandom * 0.035 * sin(uTime * 2.0 + aRandom.x * 20.0);\r
  }\r
  return p;\r
}\r
\r
// ---- 形态 2：散乱噪点云 ----------------------------------------------------\r
vec3 animCloud(vec3 p) {\r
  float r = length(p.xy);\r
  p.xy = rot(uTime * 0.13 / (1.0 + r * 0.35)) * p.xy;   // 中心快、外圈慢的旋涌\r
  p.z += sin(uTime * 0.6 + aRandom.y * 10.0) * 0.30;\r
  p += aRandom * 0.22 * sin(uTime * 0.4 + aRandom.z * 7.0);\r
  return p;\r
}\r
\r
// ---- 形态 3：正弦波形带 ----------------------------------------------------\r
vec3 animWave(vec3 p) {\r
  float nx  = aInfo2.y;                       // 0..1\r
  float env = pow(sin(nx * PI), 1.15);        // 中部振幅最大\r
  float y = sin(nx * 13.0 - uTime * 1.6) * 1.00\r
          + sin(nx * 26.0 + uTime * 1.1) * 0.42\r
          + (noise11(nx * 17.0 + uTime * 0.9) - 0.5) * 1.10;\r
  p.y  = y * env * (2.3 + uWaveBoost) + aRandom.y * (0.26 * env + 0.07) + uWaveOffsetY;\r
  p.z += sin(uTime * 0.9 + nx * 9.0) * 0.45;\r
  return p;\r
}\r
\r
// ---- 形态 4：漩涡 / 黑洞 ---------------------------------------------------\r
vec3 animVortex(vec3 p) {\r
  float r = aInfo2.z;\r
  float a = aInfo2.w + uTime * (0.60 / (0.7 + r * 0.55));  // 越靠内转得越快\r
  vec3 q = vec3(cos(a) * r, sin(a) * r, p.z);\r
  q.z += sin(uTime * 0.5 + r * 1.4) * 0.45;\r
  return q;\r
}\r
\r
void main() {\r
  float p = clamp(uProgress, 0.0, 4.0);\r
\r
  // 顺序 lerp 的四个插值因子，各自做一次 smoothstep 让过渡更柔\r
  float c1 = smoothstep(0.0, 1.0, clamp(p,       0.0, 1.0));\r
  float c2 = smoothstep(0.0, 1.0, clamp(p - 1.0, 0.0, 1.0));\r
  float c3 = smoothstep(0.0, 1.0, clamp(p - 2.0, 0.0, 1.0));\r
  float c4 = smoothstep(0.0, 1.0, clamp(p - 3.0, 0.0, 1.0));\r
\r
  vec3 s0 = animSphere(position);\r
  vec3 s1 = animPill(aPos1);\r
  vec3 s2 = animCloud(aPos2);\r
  vec3 s3 = animWave(aPos3);\r
  vec3 s4 = animVortex(aPos4);\r
\r
  vec3 pos = s0;\r
  pos = mix(pos, s1, c1);\r
  pos = mix(pos, s2, c2);\r
  pos = mix(pos, s3, c3);\r
  pos = mix(pos, s4, c4);\r
\r
  // 变形途中让粒子适度飞散，两端为 0\r
  float tr = sin(fract(p) * PI);\r
  pos += aRandom * tr * 1.15;\r
\r
  // 入场：从散乱状态汇聚\r
  pos += aRandom * uIntro * 9.0;\r
\r
  // 各形态的有效权重（用于混色与透明度）\r
  float w4 = c4;\r
  float w3 = c3 * (1.0 - c4);\r
  float w2 = c2 * (1.0 - c3) * (1.0 - c4);\r
  float w1 = c1 * (1.0 - c2) * (1.0 - c3) * (1.0 - c4);\r
  float w0 = (1.0 - c1) * (1.0 - c2) * (1.0 - c3) * (1.0 - c4);\r
\r
  // 指针微弱斥力\r
  vec2 d = pos.xy - uMouse.xy;\r
  float dist = length(d);\r
  float force = uMouseStrength / (1.0 + dist * dist * 0.55);\r
  pos.xy += normalize(d + vec2(0.0001)) * min(force, 0.9);\r
\r
  // ---- 颜色 ---------------------------------------------------------------\r
  float rad0 = length(position);\r
  vec3 col0 = mix(\r
    vec3(1.0),                                              // 核心亮白\r
    mix(C_BLUE, C_PURPLE, smoothstep(1.0, 6.0, rad0)),      // 外围蓝紫\r
    smoothstep(0.0, 1.7, rad0)\r
  );\r
  vec3 col1 = mix(C_CYAN, vec3(0.88, 0.96, 1.0), hash11(aRandom.x * 31.0));\r
  vec3 col2 = ramp5(fract(aRandom.x * 3.7 + aRandom.z * 1.3));\r
  vec3 col3 = ramp5(aInfo2.y);\r
  vec3 col4 = mix(C_PURPLE, C_PINK, clamp(aInfo2.z / 9.0, 0.0, 1.0));\r
  col4 = mix(col4, C_CYAN, smoothstep(7.0, 12.0, aInfo2.z) * 0.6);\r
\r
  vColor = col0 * w0 + col1 * w1 + col2 * w2 + col3 * w3 + col4 * w4;\r
\r
  // ---- 透明度：胶囊内部/浮尘压暗、漩涡中心掏空 + 随机闪烁 ------------------\r
  float aPill = aInfo2.x < 0.5 ? 1.0 : (aInfo2.x < 1.5 ? 0.42 : 0.2);\r
  float aVortex = smoothstep(0.5, 3.2, aInfo2.z);\r
  float alpha = w0 + aPill * w1 + w2 + w3 + aVortex * w4;\r
  float tw = 0.72 + 0.28 * sin(uTime * 1.6 + aRandom.x * 40.0);\r
  vAlpha = clamp(alpha, 0.0, 1.0) * tw * (0.42 + 0.38 * hash11(aRandom.z * 97.0));\r
  vAlpha *= 1.0 - uIntro * 0.6;\r
\r
  vec4 mv = modelViewMatrix * vec4(pos, 1.0);\r
  gl_Position = projectionMatrix * mv;\r
  gl_PointSize = uSize * aScale * uDpr * (30.0 / max(-mv.z, 0.001));\r
}\r
`

const mainFragmentShader = `// 主粒子系统片元着色器：软边圆点 + additive blending\r
precision highp float;\r
\r
varying vec3  vColor;\r
varying float vAlpha;\r
\r
void main() {\r
  vec2 uv = gl_PointCoord - 0.5;\r
  float d = length(uv);\r
  float a = smoothstep(0.5, 0.0, d);\r
  a = pow(a, 1.9);\r
  if (a < 0.01) discard;\r
  gl_FragColor = vec4(vColor * a, a * vAlpha);\r
}\r
`

const ambientVertexShader = `// 星空背景层：慢速视差漂移 + 随机闪烁
precision highp float;\r
\r
uniform float uTime;\r
uniform float uSize;\r
uniform float uDpr;\r
uniform float uDrift;     // 该层的漂移速度（视差）\r
uniform vec2  uParallax;  // 指针视差\r
\r
attribute float aSeed;\r
attribute float aScale;\r
\r
varying float vTwinkle;\r
varying float vTint;\r
\r
void main() {\r
  vec3 p = position;\r
\r
  // 横向缓慢漂移，越过边界后回绕\r
  float span = 60.0;\r
  p.x = mod(p.x + uTime * uDrift + span * 0.5, span) - span * 0.5;\r
  p.xy += uParallax * (1.0 + aSeed * 0.8);\r
\r
  // 随机相位闪烁\r
  float tw = sin(uTime * (0.6 + aSeed * 2.2) + aSeed * 60.0);\r
  vTwinkle = 0.35 + 0.65 * (tw * 0.5 + 0.5);\r
  vTint = aSeed;\r
\r
  vec4 mv = modelViewMatrix * vec4(p, 1.0);\r
  gl_Position = projectionMatrix * mv;\r
  gl_PointSize = uSize * aScale * uDpr * (30.0 / max(-mv.z, 0.001));\r
}\r
`

const ambientFragmentShader = `precision highp float;
\r
uniform float uOpacity;\r
\r
varying float vTwinkle;\r
varying float vTint;\r
\r
void main() {\r
  vec2 uv = gl_PointCoord - 0.5;\r
  float d = length(uv);\r
  float a = smoothstep(0.5, 0.0, d);\r
  a = pow(a, 2.2);\r
  if (a < 0.01) discard;\r
\r
  // 大部分冷白，少量偏青/偏紫\r
  vec3 c = mix(vec3(0.86, 0.90, 1.0), vec3(0.62, 0.78, 1.0), step(0.75, vTint));\r
  c = mix(c, vec3(0.80, 0.70, 1.0), step(0.93, vTint));\r
\r
  gl_FragColor = vec4(c * a, a * vTwinkle * uOpacity);\r
}\r
`

// ----------------------------------------------------------------------------
// Generator Functions
// ----------------------------------------------------------------------------
function getDimensions(isMobile: boolean): ParticleConfig {
  return isMobile
    ? {
        星球中心: [0, -2.6, 0],
        星球半径: 2.5,
        胶囊半宽: 3.9,
        胶囊半高: 1.15,
        星云半径: 5.4,
        波形半宽: 6,
        波形下移: -3.4,
        漩涡半径: 6.2,
      }
    : {
        星球中心: [7.4, 0.2, 0],
        星球半径: 5,
        胶囊半宽: 9.6,
        胶囊半高: 1.25,
        星云半径: 9,
        波形半宽: 18,
        波形下移: -2.6,
        漩涡半径: 11,
      }
}

function randGaussian(): number {
  let sum = 0
  for (let i = 0; i < 3; i++) sum += Math.random()
  return (sum / 3 - 0.5) * 2
}

function randOnSphere(): [number, number, number] {
  const z = Math.random() * 2 - 1
  const phi = Math.random() * Math.PI * 2
  const r = Math.sqrt(1 - z * z)
  return [r * Math.cos(phi), z, r * Math.sin(phi)]
}

function genSphere(count: number, cfg: ParticleConfig) {
  const pos = new Float32Array(count * 3)
  const info = new Float32Array(count * 4)
  const radius = cfg.星球半径
  const coreCutoff = Math.floor(count * 0.05)
  const shellCutoff = Math.floor(count * 0.72)
  const ringRadii = [radius * 1.26, radius * 1.54, radius * 1.84]

  for (let i = 0; i < count; i++) {
    const pIdx = i * 3
    const iIdx = i * 4
    const [sx, sy, sz] = randOnSphere()

    if (i < coreCutoff) {
      const d = radius * 0.055 * Math.cbrt(Math.random())
      pos[pIdx] = sx * d
      pos[pIdx + 1] = sy * d
      pos[pIdx + 2] = sz * d
      info[iIdx] = 0
    } else if (i < shellCutoff) {
      const d = radius * (0.34 + 0.66 * Math.random() ** 0.42)
      pos[pIdx] = sx * d
      pos[pIdx + 1] = sy * d * 0.94
      pos[pIdx + 2] = sz * d
      info[iIdx] = 1
    } else {
      const ringId = (i - shellCutoff) % 3
      const r = ringRadii[ringId] + randGaussian() * 0.13
      const angle = Math.random() * Math.PI * 2
      pos[pIdx] = Math.cos(angle) * r
      pos[pIdx + 1] = randGaussian() * 0.1
      pos[pIdx + 2] = Math.sin(angle) * r
      info[iIdx] = 2
      info[iIdx + 1] = r
      info[iIdx + 2] = angle
      info[iIdx + 3] = ringId
    }
  }
  return { pos, info }
}

function genPill(count: number, cfg: ParticleConfig) {
  const pos = new Float32Array(count * 3)
  const interior = new Float32Array(count)
  const halfW = cfg.胶囊半宽
  const halfH = cfg.胶囊半高
  const straightLen = Math.max(halfW - halfH, 0.001) * 2
  const arcLen = Math.PI * halfH
  const perimeter = straightLen * 2 + arcLen * 2
  const borderCutoff = Math.floor(count * 0.56)
  const fillCutoff = Math.floor(count * 0.78)

  for (let i = 0; i < count; i++) {
    const pIdx = i * 3
    if (i < borderCutoff) {
      let d = Math.random() * perimeter
      let x = 0
      let y = 0
      if (d < straightLen) {
        x = -straightLen / 2 + d
        y = halfH
      } else if ((d -= straightLen) < arcLen) {
        const a = -Math.PI / 2 + (d / arcLen) * Math.PI
        x = straightLen / 2 + Math.cos(a) * halfH
        y = Math.sin(a) * halfH
      } else if ((d -= arcLen) < straightLen) {
        x = straightLen / 2 - d
        y = -halfH
      } else {
        d -= straightLen
        const a = Math.PI / 2 + (d / arcLen) * Math.PI
        x = -straightLen / 2 + Math.cos(a) * halfH
        y = Math.sin(a) * halfH
      }
      pos[pIdx] = x + randGaussian() * 0.022
      pos[pIdx + 1] = y + randGaussian() * 0.022
      pos[pIdx + 2] = randGaussian() * 0.07
      interior[i] = 0
    } else if (i < fillCutoff) {
      const rx = (Math.random() * 2 - 1) * halfW
      const lim = Math.min(Math.max(Math.abs(rx) - straightLen / 2, 0), halfH)
      const my = Math.sqrt(Math.max(halfH * halfH - lim * lim, 0)) * 0.8
      pos[pIdx] = rx
      pos[pIdx + 1] = (Math.random() * 2 - 1) * my
      pos[pIdx + 2] = randGaussian() * 0.3
      interior[i] = 1
    } else {
      pos[pIdx] = randGaussian() * halfW * 1.35
      pos[pIdx + 1] = randGaussian() * halfH * 4.2
      pos[pIdx + 2] = randGaussian() * 2.4
      interior[i] = 2
    }
  }
  return { pos, interior }
}

function genCloud(count: number, cfg: ParticleConfig) {
  const pos = new Float32Array(count * 3)
  const radius = cfg.星云半径
  for (let i = 0; i < count; i++) {
    const pIdx = i * 3
    const [sx, sy, sz] = randOnSphere()
    const dist = radius * Math.random() ** 0.72
    pos[pIdx] = sx * dist
    pos[pIdx + 1] = sy * dist * 0.82
    pos[pIdx + 2] = sz * dist * 0.45
  }
  return { pos }
}

function genWave(count: number, cfg: ParticleConfig) {
  const pos = new Float32Array(count * 3)
  const nx = new Float32Array(count)
  const halfW = cfg.波形半宽
  for (let i = 0; i < count; i++) {
    const pIdx = i * 3
    const rand = Math.random()
    nx[i] = rand
    pos[pIdx] = (rand - 0.5) * 2 * halfW
    pos[pIdx + 1] = 0
    pos[pIdx + 2] = randGaussian() * 1.05
  }
  return { pos, nx }
}

function genVortex(count: number, cfg: ParticleConfig) {
  const pos = new Float32Array(count * 3)
  const ra = new Float32Array(count * 2)
  const radius = cfg.漩涡半径
  for (let i = 0; i < count; i++) {
    const pIdx = i * 3
    const raIdx = i * 2
    const dist = 0.45 + (radius - 0.45) * Math.random() ** 0.62
    const armAngle = (i % 3) * ((Math.PI * 2) / 3)
    const jitter = randGaussian() * (0.24 + (dist / radius) * 0.85)
    const angle = armAngle + dist * 0.55 + jitter
    pos[pIdx] = Math.cos(angle) * dist
    pos[pIdx + 1] = Math.sin(angle) * dist
    pos[pIdx + 2] = randGaussian() * 0.9 * (1 - (dist / radius) * 0.6)
    ra[raIdx] = dist
    ra[raIdx + 1] = angle
  }
  return { pos, ra }
}

function genAmbientDust(count: number, minZ: number, maxZ: number) {
  const pos = new Float32Array(count * 3)
  const seed = new Float32Array(count)
  const scale = new Float32Array(count)
  for (let i = 0; i < count; i++) {
    const pIdx = i * 3
    pos[pIdx] = (Math.random() - 0.5) * 60
    pos[pIdx + 1] = (Math.random() - 0.5) * 42
    pos[pIdx + 2] = minZ + Math.random() * (maxZ - minZ)
    seed[i] = Math.random()
    scale[i] = 0.45 + Math.random() ** 2.4 * 1.5
  }
  return { pos, seed, scale }
}

// ----------------------------------------------------------------------------
// Scene Manager
// ----------------------------------------------------------------------------
export function createParticleScene(
  canvas: HTMLCanvasElement,
  getProgress: () => number,
  onFailure: () => void,
  getSphereCenter?: () => SphereCenter | null | undefined,
  capsule?: HTMLElement | null
): (() => void) | null {
  const renderer = new WebGLRenderer({
    canvas,
    antialias: false,
    alpha: false,
    powerPreference: 'high-performance',
  })
  renderer.setClearColor('#050508', 1)

  const scene = new Scene()
  scene.background = new Color('#050508')

  const camera = new PerspectiveCamera(50, 1, 0.1, 200)
  camera.position.z = 22

  let composer: EffectComposer | undefined
  let animId: number | undefined
  let disposed = false
  let lastTime = performance.now()
  let elapsed = 0
  let currentProgress = 0
  let isMobile: boolean | null = null
  const viewSize = new Vector2()
  const pillSize = new Vector2()
  const capsuleObserver =
    capsule && typeof ResizeObserver !== 'undefined'
      ? new ResizeObserver(handleResize)
      : undefined

  const mouseCoord = new Vector2()
  const parallaxOffset = new Vector2()
  let hasPointer = false

  const gl = renderer.getContext()
  const debugInfo = gl.getExtension('WEBGL_debug_renderer_info')
  const unmaskedRenderer = debugInfo
    ? String(gl.getParameter(debugInfo.UNMASKED_RENDERER_WEBGL))
    : ''

  const nav = navigator as Navigator & { deviceMemory?: number }
  const isLowEnd =
    /swiftshader|llvmpipe|software|microsoft basic/i.test(unmaskedRenderer) ||
    (navigator.hardwareConcurrency ?? 8) <= 4 ||
    (nav.deviceMemory ?? 8) <= 4

  const activeMaterials: ShaderMaterial[] = []
  const passes: Array<{ dispose?: () => void }> = []

  renderer.debug.onShaderError = () => {
    throw new Error('粒子着色器不可用')
  }

  function clearScene() {
    while (scene.children.length > 0) {
      const obj = scene.children[0]
      if ('geometry' in obj && obj.geometry instanceof BufferGeometry) {
        obj.geometry.dispose()
      }
      if ('material' in obj && obj.material instanceof ShaderMaterial) {
        obj.material.dispose()
      }
      scene.remove(obj)
    }
    activeMaterials.length = 0
  }

  function addPointsLayer(
    attribs: Record<string, [ArrayLike<number>, number]>,
    vertexShader: string,
    fragmentShader: string,
    uniforms: Record<string, { value: unknown }>
  ) {
    const geo = new BufferGeometry()
    for (const [name, [arr, size]] of Object.entries(attribs)) {
      geo.setAttribute(name, new BufferAttribute(arr as Float32Array, size))
    }

    const mat = new ShaderMaterial({
      vertexShader,
      fragmentShader,
      uniforms,
      transparent: true,
      depthWrite: false,
      depthTest: false,
      blending: AdditiveBlending,
    })

    const pts = new Points(geo, mat)
    pts.frustumCulled = false
    scene.add(pts)
    activeMaterials.push(mat)
  }

  function buildParticleMesh() {
    clearScene()
    let count = 52000
    if (isLowEnd) {
      count = 10000
    } else if (isMobile) {
      count = 24000
    }
    const cfg = getDimensions(Boolean(isMobile))
    if (pillSize.x > 0) {
      cfg.胶囊半宽 = pillSize.x
      cfg.胶囊半高 = pillSize.y
    }

    const sphereData = genSphere(count, cfg)
    const pillData = genPill(count, cfg)
    const cloudData = genCloud(count, cfg)
    const waveData = genWave(count, cfg)
    const vortexData = genVortex(count, cfg)

    const info2 = new Float32Array(count * 4)
    const randomVec = new Float32Array(count * 3)
    const scaleArr = new Float32Array(count)

    for (let i = 0; i < count; i++) {
      info2.set(
        [
          pillData.interior[i] ?? 0,
          waveData.nx[i] ?? 0,
          vortexData.ra[i * 2] ?? 0,
          vortexData.ra[i * 2 + 1] ?? 0,
        ],
        i * 4
      )
      randomVec.set(
        [Math.random() * 2 - 1, Math.random() * 2 - 1, Math.random() * 2 - 1],
        i * 3
      )
      scaleArr[i] = 0.4 + Math.random() ** 3 * 1.35
    }

    addPointsLayer(
      {
        position: [sphereData.pos, 3],
        aPos1: [pillData.pos, 3],
        aPos2: [cloudData.pos, 3],
        aPos3: [waveData.pos, 3],
        aPos4: [vortexData.pos, 3],
        aInfo: [sphereData.info, 4],
        aInfo2: [info2, 4],
        aRandom: [randomVec, 3],
        aScale: [scaleArr, 1],
      },
      mainVertexShader,
      mainFragmentShader,
      {
        uTime: { value: 0 },
        uProgress: { value: 0 },
        uSize: { value: isMobile ? 3 : 3.4 },
        uDpr: { value: 1 },
        uMouse: { value: new Vector3(999, 999, 0) },
        uMouseStrength: { value: 0 },
        uParallax: { value: new Vector2() },
        uSphereCenter: { value: new Vector3(...cfg.星球中心) },
        uPillHalfW: { value: cfg.胶囊半宽 },
        uWaveBoost: { value: 0 },
        uWaveOffsetY: { value: cfg.波形下移 },
        uIntro: { value: 1 },
      }
    )

    // Ambient stars background
    const ambientConfigs: Array<
      [number, number, number, number, number, number]
    > = [
      [isMobile ? 700 : 1400, -34, -20, 2.6, 0.12, 0.55],
      [isMobile ? 500 : 1000, -14, -6, 3.4, 0.32, 0.85],
    ]

    for (const [cnt, minZ, maxZ, size, drift, opacity] of ambientConfigs) {
      const dust = genAmbientDust(cnt, minZ, maxZ)
      addPointsLayer(
        {
          position: [dust.pos, 3],
          aSeed: [dust.seed, 1],
          aScale: [dust.scale, 1],
        },
        ambientVertexShader,
        ambientFragmentShader,
        {
          uTime: { value: 0 },
          uSize: { value: size },
          uDpr: { value: 1 },
          uDrift: { value: drift },
          uOpacity: { value: opacity },
          uParallax: { value: new Vector2() },
        }
      )
    }
  }

  function handleResize() {
    const width = window.innerWidth
    const height = window.innerHeight
    const mobile = width <= 900 || height > width

    camera.aspect = width / height
    camera.updateProjectionMatrix()

    renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.5))
    renderer.setSize(width, height, false)

    if (composer) {
      composer.setPixelRatio(renderer.getPixelRatio())
      composer.setSize(width, height)
    }

    const rect = capsule?.getBoundingClientRect()
    camera.getViewSize(camera.position.z, viewSize)
    // Fit narrow screens and undersized halos; preserve the wide desktop effect.
    let halfWidth = rect?.width
      ? ((rect.width + 24) * viewSize.x) / (2 * width)
      : 0
    let halfHeight = rect?.height
      ? ((rect.height + 24) * viewSize.y) / (2 * height)
      : 0
    if (!mobile && halfWidth <= getDimensions(false).胶囊半宽) {
      halfWidth = 0
      halfHeight = 0
    }
    if (
      mobile !== isMobile ||
      activeMaterials.length === 0 ||
      pillSize.x !== halfWidth ||
      pillSize.y !== halfHeight
    ) {
      isMobile = mobile
      pillSize.set(halfWidth, halfHeight)
      buildParticleMesh()
    }
  }

  function handlePointerMove(e: PointerEvent) {
    mouseCoord.set(
      (e.clientX / window.innerWidth) * 2 - 1,
      (e.clientY / window.innerHeight) * 2 - 1
    )
    hasPointer = true
  }

  function handlePointerLeave() {
    hasPointer = false
    mouseCoord.set(0, 0)
  }

  function renderFrame(now: number) {
    if (disposed) return
    const delta = Math.min((now - lastTime) / 1000, 0.05)
    lastTime = now

    if (!document.hidden) {
      elapsed += delta
      currentProgress +=
        (getProgress() - currentProgress) * Math.min(delta * 6, 1)

      for (const mat of activeMaterials) {
        const u = mat.uniforms as Record<string, { value: unknown }>
        if (u.uTime) u.uTime.value = elapsed
        if (u.uDpr) u.uDpr.value = renderer.getPixelRatio()
        if (u.uParallax && u.uParallax.value instanceof Vector2) {
          u.uParallax.value.lerp(
            parallaxOffset.set(mouseCoord.x * 0.07, -mouseCoord.y * 0.07),
            Math.min(delta * 4, 1)
          )
        }
        if (u.uProgress) {
          const center = getSphereCenter?.()
          if (
            center &&
            u.uSphereCenter &&
            u.uSphereCenter.value instanceof Vector3
          ) {
            u.uSphereCenter.value.set(
              center.x * 10.26 * camera.aspect,
              center.y * 10.26,
              0
            )
          }
          u.uProgress.value = currentProgress
          if (u.uIntro) {
            u.uIntro.value = Math.max(0, 1 - elapsed / 2.6) ** 3
          }
          if (u.uMouseStrength) {
            u.uMouseStrength.value = hasPointer ? 0.55 : 0
          }
          if (u.uMouse && u.uMouse.value instanceof Vector3) {
            u.uMouse.value.set(
              mouseCoord.x * 10.26 * camera.aspect,
              -mouseCoord.y * 10.26,
              0
            )
          }
        }
      }

      try {
        if (composer && !isMobile) {
          composer.render()
        } else {
          renderer.render(scene, camera)
        }
      } catch {
        disposeScene()
        onFailure()
        return
      }
    }

    if (!document.hidden) {
      animId = requestAnimationFrame(renderFrame)
    }
  }

  function handleVisibilityChange() {
    if (animId) cancelAnimationFrame(animId)
    if (!document.hidden && !disposed) {
      lastTime = performance.now()
      animId = requestAnimationFrame(renderFrame)
    }
  }

  function disposeScene() {
    if (disposed) return
    disposed = true

    if (animId) cancelAnimationFrame(animId)
    capsuleObserver?.disconnect()
    window.removeEventListener('resize', handleResize)
    window.removeEventListener('pointermove', handlePointerMove)
    document.removeEventListener('pointerleave', handlePointerLeave)
    document.removeEventListener('visibilitychange', handleVisibilityChange)
    canvas.removeEventListener('webglcontextlost', handleContextLost)

    clearScene()
    for (const p of passes) {
      p.dispose?.()
    }
    composer?.dispose()
    renderer.dispose()
  }

  function handleContextLost(e: Event) {
    e.preventDefault()
    disposeScene()
    onFailure()
  }

  try {
    handleResize()
    if (capsule) capsuleObserver?.observe(capsule)

    if (!isMobile && !isLowEnd) {
      composer = new EffectComposer(renderer)
      const renderPass = new RenderPass(scene, camera)
      const bloomPass = new UnrealBloomPass(
        new Vector2(window.innerWidth, window.innerHeight),
        0.42,
        0.6,
        0.42
      )
      const outputPass = new OutputPass()

      passes.push(renderPass, bloomPass, outputPass)
      passes.forEach((p) => composer?.addPass(p as Pass))
    }

    window.addEventListener('resize', handleResize, { passive: true })
    window.addEventListener('pointermove', handlePointerMove, { passive: true })
    document.addEventListener('pointerleave', handlePointerLeave)
    document.addEventListener('visibilitychange', handleVisibilityChange)
    canvas.addEventListener('webglcontextlost', handleContextLost)

    renderFrame(performance.now())

    return disposed ? null : disposeScene
  } catch (err) {
    disposeScene()
    throw err
  }
}

<template>
  <div
    ref="dialElement"
    class="login-dial"
    :class="{ 'login-dial--animated': motionEnabled }"
    aria-hidden="true"
  >
    <svg viewBox="0 0 400 400" fill="none" focusable="false">
      <circle class="login-dial__outline" cx="200" cy="200" r="198" />
      <circle class="login-dial__outline" cx="200" cy="200" r="160" />
      <circle class="login-dial__outline" cx="200" cy="200" r="116" />
      <circle class="login-dial__outline" cx="200" cy="200" r="98" />

      <g ref="outerRing" transform="rotate(-12 200 200)">
        <line
          v-for="tick in outerTicks"
          :key="tick.index"
          class="login-dial__tick"
          :class="{ 'login-dial__tick--major': tick.major }"
          x1="200"
          y1="7"
          x2="200"
          :y2="tick.end"
          :transform="`rotate(${tick.index * 3.6} 200 200)`"
        />
        <text
          v-for="number in outerNumbers"
          :key="number.index"
          class="login-dial__number"
          x="200"
          y="40"
          text-anchor="middle"
          dominant-baseline="central"
          :transform="`rotate(${number.index * 36} 200 200)`"
        >{{ number.label }}</text>
      </g>

      <g ref="innerRing" transform="rotate(19.2 200 200)">
        <line
          v-for="tick in innerTicks"
          :key="tick.index"
          class="login-dial__tick login-dial__tick--inner"
          :class="{ 'login-dial__tick--major': tick.major }"
          x1="200"
          y1="84"
          x2="200"
          :y2="tick.major ? 102 : 92"
          :transform="`rotate(${tick.index * 6} 200 200)`"
        />
      </g>

      <circle class="login-dial__dashes" cx="200" cy="200" r="146" />
      <circle class="login-dial__dashes login-dial__dashes--inner" cx="200" cy="200" r="132" />
      <circle class="login-dial__center" cx="200" cy="200" r="74" />
      <circle class="login-dial__center-outline" cx="200" cy="200" r="66" />

      <g v-if="showLogo">
        <rect class="login-dial__logo-backing" x="174" y="150" width="52" height="52" rx="13" />
        <image
          :href="siteLogo"
          x="180"
          y="156"
          width="40"
          height="40"
          preserveAspectRatio="xMidYMid meet"
          @error="logoFailed = true"
        />
      </g>
      <text
        v-for="(line, index) in nameLines"
        :key="index"
        class="login-dial__name"
        x="200"
        :y="nameCenter + (index - (nameLines.length - 1) / 2) * nameFontSize * 1.05"
        :font-size="nameFontSize"
        text-anchor="middle"
        dominant-baseline="central"
      >{{ line }}</text>

      <path class="login-dial__pointer" d="M195 2h10l-5 11z" />
    </svg>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{
  siteName: string
  // The parent supplies only a sanitized custom logo; the default mark is omitted.
  siteLogo?: string
}>()

const dialElement = ref<HTMLDivElement | null>(null)
const outerRing = ref<SVGGElement | null>(null)
const innerRing = ref<SVGGElement | null>(null)
const motionEnabled = ref(false)
const logoFailed = ref(false)
const showLogo = computed(() => Boolean(props.siteLogo) && !logoFailed.value)

watch(() => props.siteLogo, () => { logoFailed.value = false })

const outerTicks = Array.from({ length: 100 }, (_, index) => ({
  index,
  major: index % 10 === 0,
  end: index % 10 === 0 ? 28 : index % 5 === 0 ? 21 : 14
}))
const outerNumbers = Array.from({ length: 10 }, (_, index) => ({
  index,
  label: String(index * 10).padStart(2, '0')
}))
const innerTicks = Array.from({ length: 60 }, (_, index) => ({
  index,
  major: index % 5 === 0
}))

// Estimate em widths conservatively, including names without word separators.
function characterWidth(character: string): number {
  if (character === ' ') return 0.4
  if (/[MW@#%&]/.test(character)) return 1
  return (character.codePointAt(0) ?? 0) <= 0xff ? 0.7 : 1.1
}

function lineWidth(line: string): number {
  return Array.from(line).reduce((width, character) => width + characterWidth(character), 0)
}

const nameLines = computed(() => {
  const name = props.siteName.trim().replace(/\s+/g, ' ')
  if (!name) return []
  const words = name.split(' ')
  if (words.length === 2 && lineWidth(name) <= 10) return words

  const lines: string[] = []
  let line = ''
  for (const word of words) {
    if (line && lineWidth(`${line} ${word}`) > 10) {
      lines.push(line)
      line = ''
    }
    if (line) line += ' '
    for (const character of Array.from(word)) {
      if (line && lineWidth(line + character) > 10) {
        lines.push(line)
        line = ''
      }
      line += character
    }
  }
  if (line) lines.push(line)
  return lines
})

const nameCenter = computed(() => showLogo.value ? 229 : 200)
const nameFontSize = computed(() => {
  const maxWidth = Math.max(1, ...nameLines.value.map(lineWidth))
  const availableWidth = showLogo.value ? 104 : 114
  const availableHeight = showLogo.value ? 42 : 90
  return Math.min(
    showLogo.value ? 21 : 38,
    availableWidth / (maxWidth + 0.6),
    availableHeight / (Math.max(1, nameLines.value.length) * 1.05)
  )
})

let reducedMotion: MediaQueryList | undefined
let desktopViewport: MediaQueryList | undefined
let finePointer: MediaQueryList | undefined
let intersectionObserver: IntersectionObserver | undefined
let inViewport = true
let listeningForPointer = false
let animationFrame: number | null = null
let previousFrameTime = 0
let currentAngle = -12
let targetAngle = -12
let lastPointerTime = -Infinity

function stopAnimation() {
  if (animationFrame !== null) window.cancelAnimationFrame(animationFrame)
  animationFrame = null
  previousFrameTime = 0
}

function animate(time: number) {
  animationFrame = null
  if (!motionEnabled.value) return

  const elapsed = previousFrameTime ? Math.min(time - previousFrameTime, 64) : 16
  previousFrameTime = time
  if (time - lastPointerTime < 1200) {
    // Always follow the short arc, including when the pointer crosses north.
    const difference = (((targetAngle - currentAngle) % 360 + 540) % 360) - 180
    currentAngle += Math.abs(difference) < 0.025
      ? difference
      : difference * (1 - Math.exp(-elapsed / 140))
  } else {
    currentAngle += elapsed * 0.012
  }
  // Keep an unwrapped angle so the opposing inner ring never jumps at 360 degrees.
  outerRing.value?.setAttribute('transform', `rotate(${currentAngle} 200 200)`)
  innerRing.value?.setAttribute('transform', `rotate(${-currentAngle * 1.6} 200 200)`)

  animationFrame = window.requestAnimationFrame(animate)
}

function handlePointerMove(event: PointerEvent) {
  if (!motionEnabled.value || event.pointerType === 'touch') return
  const bounds = dialElement.value?.getBoundingClientRect()
  if (!bounds || !bounds.width || !bounds.height) return
  const x = event.clientX - bounds.left - bounds.width / 2
  const y = event.clientY - bounds.top - bounds.height / 2
  if (Math.hypot(x, y) < 16) return
  targetAngle = (Math.atan2(y, x) * 180 / Math.PI + 450) % 360
  lastPointerTime = performance.now()
  if (animationFrame === null) animationFrame = window.requestAnimationFrame(animate)
}

function updateMotionState() {
  const bounds = dialElement.value?.getBoundingClientRect()
  const visible = Boolean(bounds && bounds.width && bounds.height &&
    bounds.bottom > 0 && bounds.top < window.innerHeight &&
    bounds.right > 0 && bounds.left < window.innerWidth)
  motionEnabled.value = Boolean(
    !document.hidden && !reducedMotion?.matches && desktopViewport?.matches &&
    inViewport && visible
  )

  const pointerEnabled = motionEnabled.value && finePointer?.matches
  if (pointerEnabled && !listeningForPointer) {
    window.addEventListener('pointermove', handlePointerMove, { passive: true })
    listeningForPointer = true
  } else if (!pointerEnabled && listeningForPointer) {
    window.removeEventListener('pointermove', handlePointerMove)
    listeningForPointer = false
  }
  if (!motionEnabled.value) {
    stopAnimation()
  } else if (animationFrame === null) {
    animationFrame = window.requestAnimationFrame(animate)
  }
}

onMounted(() => {
  if (typeof window.matchMedia !== 'function' || typeof window.requestAnimationFrame !== 'function') return
  reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
  desktopViewport = window.matchMedia('(min-width: 1024px)')
  finePointer = window.matchMedia('(pointer: fine)')
  for (const query of [reducedMotion, desktopViewport, finePointer]) {
    query.addEventListener('change', updateMotionState)
  }
  document.addEventListener('visibilitychange', updateMotionState)
  window.addEventListener('resize', updateMotionState, { passive: true })

  if ('IntersectionObserver' in window && dialElement.value) {
    intersectionObserver = new IntersectionObserver(([entry]) => {
      inViewport = Boolean(entry?.isIntersecting)
      updateMotionState()
    })
    intersectionObserver.observe(dialElement.value)
  } else {
    window.addEventListener('scroll', updateMotionState, { passive: true })
  }
  updateMotionState()
})

onBeforeUnmount(() => {
  stopAnimation()
  intersectionObserver?.disconnect()
  for (const query of [reducedMotion, desktopViewport, finePointer]) {
    query?.removeEventListener('change', updateMotionState)
  }
  document.removeEventListener('visibilitychange', updateMotionState)
  window.removeEventListener('resize', updateMotionState)
  window.removeEventListener('scroll', updateMotionState)
  window.removeEventListener('pointermove', handlePointerMove)
})
</script>

<style scoped>
.login-dial {
  width: 100%;
  aspect-ratio: 1;
  pointer-events: none;
  user-select: none;
}

.login-dial svg {
  display: block;
  width: 100%;
  height: 100%;
  overflow: visible;
}

.login-dial__outline {
  stroke: var(--login-dial-muted, theme('colors.primary.300'));
  stroke-width: 0.6;
  opacity: 0.4;
}

.login-dial__tick {
  stroke: var(--login-dial-muted, theme('colors.primary.300'));
  stroke-width: 0.9;
}

.login-dial__tick--major {
  stroke: var(--login-dial-ink, theme('colors.primary.700'));
  stroke-width: 1.2;
}

.login-dial__tick--inner {
  opacity: 0.68;
}

.login-dial__number {
  fill: var(--login-dial-ink, theme('colors.primary.700'));
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 10px;
  font-weight: 500;
  letter-spacing: 0.06em;
}

.login-dial__dashes {
  stroke: var(--login-dial-muted, theme('colors.primary.300'));
  stroke-width: 0.8;
  stroke-dasharray: 1 6;
  stroke-linecap: round;
  transform-origin: 200px 200px;
  animation: login-dial-orbit 80s linear infinite paused;
}

.login-dial__dashes--inner {
  stroke-dasharray: 2 7;
  opacity: 0.6;
  animation-duration: 120s;
  animation-direction: reverse;
}

.login-dial--animated .login-dial__dashes {
  animation-play-state: running;
}

.login-dial__center,
.login-dial__pointer {
  fill: var(--login-accent, theme('colors.primary.600'));
}

.login-dial__center-outline {
  stroke: var(--login-on-accent, white);
  stroke-width: 0.6;
  opacity: 0.16;
}

.login-dial__logo-backing {
  fill: var(--login-on-accent, white);
  opacity: 0.94;
}

.login-dial__name {
  fill: var(--login-on-accent, white);
  font-family: inherit;
  font-weight: 650;
  letter-spacing: -0.035em;
}

@keyframes login-dial-orbit {
  to { transform: rotate(360deg); }
}

@media (prefers-reduced-motion: reduce) {
  .login-dial__dashes { animation: none; }
}
</style>

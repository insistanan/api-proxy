import { onMounted, onUnmounted, watch, type Ref } from 'vue'
import { usePreferencesStore } from '@/shared/stores/preferences'
import { useTheme, type ThemeSettings } from '@/shared/theme'
import { useToast } from '@/shared/composables/useToast'

// 顶栏统一委托事件；鼠标移动只写当前元素的 CSS 变量，不触发 Vue 列表更新。
export function useHeaderFeedback(header: Ref<HTMLElement | undefined>) {
  const preferences = usePreferencesStore()
  const theme = useTheme()
  const { showToast } = useToast()
  let current: HTMLElement | null = null
  let bounds: Pick<ReturnType<HTMLElement['getBoundingClientRect']>, 'left' | 'top' | 'width' | 'height'> | undefined
  let frame: number | undefined
  let previousPointer: { x: number; y: number; time: number } | undefined
  let pointerX = 0
  let pointerY = 0
  let speed = 0
  let reducedMotion = true
  let motionQuery: ReturnType<typeof window.matchMedia> | undefined
  let audio: AudioContext | undefined
  let lastTone = -Infinity
  let soundErrorReported = false
  let disposed = false
  const baseNotes: Record<ThemeSettings['palette'], number> = { pop: 587.33, breeze: 440, ink: 493.88, ocean: 523.25, forest: 392, sunset: 466.16, rose: 415.3 }

  function hitTarget(event: Event): HTMLElement | null {
    const target = event.target instanceof Element ? event.target.closest<HTMLElement>('[data-header-hit]') : null
    return target && header.value?.contains(target) && !target.hasAttribute('disabled') && target.getAttribute('aria-disabled') !== 'true'
      ? target : null
  }

  function soundFailure(error: unknown) {
    if (disposed) return
    preferences.headerSoundEnabled = false
    if (soundErrorReported) return
    soundErrorReported = true
    showToast(`界面音效无法播放，已关闭：${error instanceof Error ? error.message : String(error)}`, 'warning')
  }

  async function unlockSound() {
    if (disposed || !preferences.headerSoundEnabled || document.hidden) return
    try {
      if (typeof AudioContext === 'undefined') throw new Error('当前浏览器不支持 Web Audio')
      audio ??= new AudioContext()
      if (audio.state === 'suspended') await audio.resume()
      return audio
    } catch (error) { soundFailure(error) }
  }

  function playTone(kind: 'hover' | 'select', velocity: number) {
    if (disposed || !preferences.headerSoundEnabled || !audio || audio.state !== 'running' || document.hidden) return
    const now = performance.now()
    if (now - lastTone < (kind === 'hover' ? 110 : 50)) return
    lastTone = now
    try {
      const oscillator = audio.createOscillator()
      const gain = audio.createGain()
      const start = audio.currentTime
      const duration = kind === 'select' ? .075 : .045
      const frequency = baseNotes[theme.settings.palette] * (kind === 'select' ? 1.5 : 1 + Math.min(velocity, 2) * .12)
      oscillator.type = theme.settings.palette === 'pop' ? 'triangle' : 'sine'
      oscillator.frequency.setValueAtTime(frequency, start)
      oscillator.frequency.exponentialRampToValueAtTime(frequency * (kind === 'select' ? 1.15 : .92), start + duration)
      gain.gain.setValueAtTime(0, start)
      gain.gain.linearRampToValueAtTime(kind === 'select' ? .025 : .012, start + .006)
      gain.gain.exponentialRampToValueAtTime(.0001, start + duration)
      oscillator.connect(gain)
      gain.connect(audio.destination)
      oscillator.onended = () => { oscillator.disconnect(); gain.disconnect() }
      oscillator.start(start)
      oscillator.stop(start + duration + .01)
    } catch (error) { soundFailure(error) }
  }

  function resetTarget() {
    if (frame !== undefined) cancelAnimationFrame(frame)
    frame = undefined
    if (current) {
      delete current.dataset.pointerActive
      delete current.dataset.hoverSpeed
      for (const name of ['--hover-x', '--hover-y', '--hover-speed']) current.style.removeProperty(name)
    }
    current = null
    bounds = undefined
  }

  function scheduleMotion() {
    if (reducedMotion || !current || frame !== undefined) return
    frame = requestAnimationFrame(() => {
      frame = undefined
      if (!current || !bounds) return
      const x = Math.max(-1, Math.min(1, (pointerX - bounds.left) / Math.max(bounds.width, 1) * 2 - 1))
      const y = Math.max(-1, Math.min(1, (pointerY - bounds.top) / Math.max(bounds.height, 1) * 2 - 1))
      current.style.setProperty('--hover-x', x.toFixed(3))
      current.style.setProperty('--hover-y', (-y).toFixed(3))
      current.style.setProperty('--hover-speed', Math.min(speed, 2).toFixed(3))
      current.dataset.pointerActive = 'true'
      current.dataset.hoverSpeed = speed > 1.1 ? 'swift' : speed > .3 ? 'brisk' : 'slow'
    })
  }

  function samplePointer(event: PointerEvent) {
    pointerX = event.clientX
    pointerY = event.clientY
    if (previousPointer) {
      const elapsed = event.timeStamp - previousPointer.time
      if (elapsed > 0) {
        const velocity = Math.hypot(pointerX - previousPointer.x, pointerY - previousPointer.y) / Math.max(elapsed, 8)
        speed = elapsed > 150 ? 0 : speed * .35 + Math.min(velocity, 3) * .65
      }
    }
    previousPointer = { x: pointerX, y: pointerY, time: event.timeStamp }
  }

  function pointerMove(event: PointerEvent) {
    if (event.pointerType !== 'mouse' || event.buttons) return
    samplePointer(event)
    scheduleMotion()
  }

  function pointerOver(event: PointerEvent) {
    if (event.pointerType !== 'mouse' || event.buttons) return
    const target = hitTarget(event)
    if (!target || target === current) return
    resetTarget()
    samplePointer(event)
    current = target
    // 每次进入目标只读取一次尺寸，逐帧更新不再强制布局。
    bounds = target.getBoundingClientRect()
    scheduleMotion()
    if (!target.hasAttribute('data-feedback-silent')) playTone('hover', speed)
  }

  function pointerOut(event: PointerEvent) {
    if (current && !current.contains(event.relatedTarget as Element | null)) resetTarget()
  }

  function pointerLeave() {
    resetTarget()
    previousPointer = undefined
    speed = 0
  }

  function activate(event: Event) {
    const target = hitTarget(event)
    if (!target || target.hasAttribute('data-feedback-silent')) return
    void unlockSound().then(() => playTone('select', speed))
  }

  function prepareSound(event: Event) {
    if (hitTarget(event)?.hasAttribute('data-feedback-silent') === false) void unlockSound()
  }

  function keyDown(event: KeyboardEvent) {
    if (event.key === 'Enter' || event.key === ' ') prepareSound(event)
  }

  function updateMotionPreference() {
    reducedMotion = motionQuery?.matches ?? true
    if (reducedMotion) resetTarget()
  }

  function pauseSound() {
    if (audio?.state === 'running') void audio.suspend().catch(soundFailure)
  }

  function visibilityChange() {
    if (document.hidden) { pointerLeave(); pauseSound() }
  }

  watch(() => preferences.headerSoundEnabled, enabled => {
    if (!enabled) pauseSound()
    else soundErrorReported = false
  })
  onMounted(() => {
    motionQuery = window.matchMedia('(prefers-reduced-motion: reduce)')
    updateMotionPreference()
    motionQuery.addEventListener('change', updateMotionPreference)
    document.addEventListener('visibilitychange', visibilityChange)
  })
  onUnmounted(() => {
    disposed = true
    pointerLeave()
    motionQuery?.removeEventListener('change', updateMotionPreference)
    document.removeEventListener('visibilitychange', visibilityChange)
    if (audio) void audio.close().catch(error => console.warn('关闭顶栏音效失败：', error))
  })

  return { pointerMove, pointerOver, pointerOut, pointerLeave, activate, prepareSound, keyDown }
}

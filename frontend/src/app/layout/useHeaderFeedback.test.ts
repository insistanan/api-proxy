// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick, ref } from 'vue'
import { createPinia } from 'pinia'
import { useHeaderFeedback } from '@/app/layout/useHeaderFeedback'
import { usePreferencesStore } from '@/shared/stores/preferences'

const cleanups: Array<() => void> = []
afterEach(() => {
  cleanups.splice(0).forEach(cleanup => cleanup())
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function setupFeedback(reduced = false) {
  vi.stubGlobal('matchMedia', () => ({ matches: reduced, addEventListener: vi.fn(), removeEventListener: vi.fn() }))
  vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  const frames = new Map<number, (timestamp: number) => void>()
  let frameId = 0
  const requestFrame = vi.fn((callback: (timestamp: number) => void) => { frames.set(++frameId, callback); return frameId })
  vi.stubGlobal('requestAnimationFrame', requestFrame)
  vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id))
  const header = document.createElement('header')
  const target = document.createElement('button')
  target.setAttribute('data-header-hit', '')
  header.append(target)
  document.body.append(header)
  vi.spyOn(target, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 100, height: 40 } as ReturnType<HTMLElement['getBoundingClientRect']>)
  const host = document.createElement('div')
  document.body.append(host)
  const pinia = createPinia()
  let feedback!: ReturnType<typeof useHeaderFeedback>
  const app = createApp({ setup() {
    feedback = useHeaderFeedback(ref(header))
    return () => null
  } })
  app.use(pinia)
  app.mount(host)
  const stop = () => { app.unmount(); header.remove(); host.remove() }
  cleanups.push(stop)
  const pointer = (x: number, time: number) => ({
    target, pointerType: 'mouse', buttons: 0, clientX: x, clientY: 20, timeStamp: time
  }) as unknown as PointerEvent
  const flushFrame = () => {
    const pending = [...frames.values()]
    frames.clear()
    pending.forEach(callback => callback(16))
  }
  return { feedback, target, pointer, flushFrame, frames, requestFrame, preferences: usePreferencesStore(pinia), stop }
}

describe('顶栏交互资源与偏好', () => {
  it('鼠标移动合并到一帧，快速划过增强反馈，离开后不保留动画任务', () => {
    const env = setupFeedback()
    env.feedback.pointerOver(env.pointer(20, 10))
    env.flushFrame()
    expect(env.target.dataset.hoverSpeed).toBe('slow')
    env.feedback.pointerMove(env.pointer(45, 20))
    env.feedback.pointerMove(env.pointer(90, 30))
    expect(env.frames.size).toBe(1)
    env.flushFrame()
    expect(env.target.dataset.hoverSpeed).toBe('swift')
    expect(env.requestFrame).toHaveBeenCalledTimes(2)
    expect(env.target.style.getPropertyValue('--hover-x')).toBe('0.800')
    env.feedback.pointerMove(env.pointer(95, 40))
    env.feedback.pointerLeave()
    expect(env.frames.size).toBe(0)
    expect(env.target.dataset.pointerActive).toBeUndefined()
    expect(env.target.style.getPropertyValue('--hover-x')).toBe('')
  })

  it('减少动态效果时不创建倾斜动画任务', () => {
    const env = setupFeedback(true)
    env.feedback.pointerOver(env.pointer(20, 10))
    env.feedback.pointerMove(env.pointer(90, 20))
    expect(env.requestFrame).not.toHaveBeenCalled()
    expect(env.target.dataset.pointerActive).toBeUndefined()
  })

  it('声音只在交互后创建，悬停限频，关闭与卸载时释放资源', async () => {
    const env = setupFeedback()
    let now = 1000
    vi.spyOn(performance, 'now').mockImplementation(() => now)
    const audio = {
      state: 'running', currentTime: 0,
      destination: {}, resume: vi.fn().mockResolvedValue(undefined),
      suspend: vi.fn().mockResolvedValue(undefined), close: vi.fn().mockResolvedValue(undefined),
      createOscillator: vi.fn(() => ({
        frequency: { setValueAtTime: vi.fn(), exponentialRampToValueAtTime: vi.fn() },
        connect: vi.fn(), disconnect: vi.fn(), start: vi.fn(), stop: vi.fn(), onended: null
      })),
      createGain: vi.fn(() => ({
        gain: { setValueAtTime: vi.fn(), linearRampToValueAtTime: vi.fn(), exponentialRampToValueAtTime: vi.fn() },
        connect: vi.fn(), disconnect: vi.fn()
      }))
    }
    const AudioMock = vi.fn(function () { return audio })
    vi.stubGlobal('AudioContext', AudioMock)
    env.feedback.pointerOver(env.pointer(20, 10))
    expect(AudioMock).not.toHaveBeenCalled()
    env.feedback.activate(env.pointer(20, 20))
    await nextTick()
    expect(AudioMock).toHaveBeenCalledTimes(1)
    expect(audio.createOscillator).toHaveBeenCalledTimes(1)
    now += 200
    env.feedback.pointerLeave()
    env.feedback.pointerOver(env.pointer(40, 40))
    env.feedback.pointerLeave()
    env.feedback.pointerOver(env.pointer(60, 50))
    expect(audio.createOscillator).toHaveBeenCalledTimes(2)
    env.preferences.headerSoundEnabled = false
    await nextTick()
    env.feedback.pointerLeave()
    env.feedback.pointerOver(env.pointer(80, 60))
    expect(audio.createOscillator).toHaveBeenCalledTimes(2)
    expect(audio.suspend).toHaveBeenCalledOnce()
    cleanups.pop()!()
    expect(audio.close).toHaveBeenCalledOnce()
    expect(env.frames.size).toBe(0)
  })
})

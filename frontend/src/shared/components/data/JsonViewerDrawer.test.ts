import { afterEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import JsonViewerDrawer from '@/shared/components/data/JsonViewerDrawer.vue'
import { useJsonViewerDrawer } from '@/shared/composables/useJsonViewerDrawer'

type State = Record<string, unknown>
const value = <T>(state: State, key: string) => (state[key] as Ref<T>).value
async function setupDrawer(data: unknown) {
  useJsonViewerDrawer().openSource({ data, title: '测试正文' })
  let state!: State
  const setup = (JsonViewerDrawer as unknown as { setup: (props: object, context: SetupContext) => State }).setup
  await renderToString(createSSRApp({ setup(props, context) { state = setup(props, { ...context, expose: vi.fn() }); return () => null } }))
  return state
}
afterEach(() => { useJsonViewerDrawer().close(); vi.restoreAllMocks() })

describe('JSON 抽屉解析保护', () => {
  it('超过 64KB 不自动解析，手动载入后可查看结构；超过 2MB 始终只展示原文', async () => {
    const raw = JSON.stringify({ payload: '大'.repeat(30000) })
    const state = await setupDrawer(raw)
    expect(value<boolean>(state, 'needsConsent')).toBe(true)
    expect(value<{ valid: boolean }>(state, 'parseResult').valid).toBe(false)
    expect(value<{ text: string }>(state, 'formatted').text).toBe(raw)
    ;(state.consented as Ref<boolean>).value = true
    expect(value<{ valid: boolean }>(state, 'parseResult').valid).toBe(true)
    const huge = await setupDrawer(JSON.stringify({ payload: 'x'.repeat(2 * 1024 * 1024) }))
    ;(huge.consented as Ref<boolean>).value = true
    expect(value<boolean>(huge, 'needsConsent')).toBe(true)
    expect(value<{ valid: boolean }>(huge, 'parseResult').valid).toBe(false)
  })

  it('无效 JSON 明示解析失败并保留原文；格式化字符串根节点保留引号', async () => {
    const invalid = await setupDrawer('{broken')
    expect(value<{ notice: string }>(invalid, 'parseResult').notice).toContain('非合法 JSON')
    expect(value<{ text: string }>(invalid, 'formatted').text).toBe('{broken')
    const scalar = await setupDrawer('"hello"')
    ;(scalar.mode as Ref<string>).value = 'raw'
    expect(value<{ text: string }>(scalar, 'formatted').text).toBe('"hello"')
  })

  it('Esc 关闭抽屉并停止事件传播，避免同时关闭底层业务弹窗', async () => {
    const state = await setupDrawer('{}')
    const event = { key: 'Escape', preventDefault: vi.fn(), stopImmediatePropagation: vi.fn() }
    ;(state.onKeydown as (event: unknown) => void)(event)
    expect(useJsonViewerDrawer().isOpen.value).toBe(false)
    expect(event.stopImmediatePropagation).toHaveBeenCalledOnce()
  })
})

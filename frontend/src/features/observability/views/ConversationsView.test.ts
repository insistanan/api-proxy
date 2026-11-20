import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp, type Ref, type SetupContext } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { createPinia, setActivePinia } from 'pinia'
import { api } from '@/api'
import type { ConversationEntry } from '@/types/conversation'
import ConversationsView from '@/features/observability/views/ConversationsView.vue'
import { useToast } from '@/shared/composables/useToast'

vi.mock('@/shared/stores/auth', () => ({ useAuthStore: () => ({ apiKey: null, clearAuth: vi.fn() }) }))
type State = Record<string, unknown>
const value = <T>(state: State, key: string) => (state[key] as Ref<T>).value
const entry = (id = 'one', name?: string): ConversationEntry => ({ id, name, apiKind: 'responses', stream: false, firstSeenAt: '', lastSeenAt: '', requestCount: 1, errorCount: 0 })
async function setupView() {
  let state!: State
  const setup = (ConversationsView as unknown as { setup: (props: object, context: SetupContext) => State }).setup
  await renderToString(createSSRApp({ setup(props, context) { state = setup(props, { ...context, expose: vi.fn() }); return () => null } }))
  ;(state.conversations as Ref<ConversationEntry[]>).value = [entry()]
  ;(state.openDetailDialog as (item: ConversationEntry) => void)(entry())
  return state
}
beforeEach(() => setActivePinia(createPinia()))
afterEach(() => vi.restoreAllMocks())

describe('对话详情命名', () => {
  it('在详情中命名并同步列表和所选会话，保存期间重复提交只发送一次', async () => {
    let finish!: (result: ConversationEntry) => void
    const save = vi.spyOn(api, 'setConversationName').mockReturnValue(new Promise(resolve => { finish = resolve }))
    const state = await setupView()
    ;(state.detailName as Ref<string>).value = '  新名称  '
    const action = state.saveDetailName as () => Promise<void>
    const pending = action()
    await action()
    expect(save).toHaveBeenCalledExactlyOnceWith('one', '新名称')
    finish(entry('one', '新名称'))
    await pending
    expect(value<ConversationEntry[]>(state, 'conversations')[0]?.name).toBe('新名称')
    expect(value<ConversationEntry>(state, 'selectedConversation').name).toBe('新名称')
    expect(value<string>(state, 'detailName')).toBe('新名称')
    expect(value<boolean>(state, 'detailDialog')).toBe(true)
  })

  it('保存失败保留名称草稿和原数据，并显示错误', async () => {
    vi.spyOn(api, 'setConversationName').mockRejectedValue(new Error('写入失败'))
    const state = await setupView()
    ;(state.detailName as Ref<string>).value = '待保存'
    await (state.saveDetailName as () => Promise<void>)()
    expect(value<string>(state, 'detailName')).toBe('待保存')
    expect(value<ConversationEntry>(state, 'selectedConversation').name).toBeUndefined()
    expect(value<ConversationEntry[]>(state, 'conversations')[0]?.name).toBeUndefined()
    expect(useToast().toasts.value.some(toast => toast.message.includes('写入失败'))).toBe(true)
  })

  it('保存期间切换到另一对话，晚到响应只更新原列表项', async () => {
    let finish!: (result: ConversationEntry) => void
    vi.spyOn(api, 'setConversationName').mockReturnValue(new Promise(resolve => { finish = resolve }))
    const state = await setupView()
    ;(state.detailName as Ref<string>).value = '第一条'
    const pending = (state.saveDetailName as () => Promise<void>)()
    ;(state.openDetailDialog as (item: ConversationEntry) => void)(entry('two', '第二条'))
    finish(entry('one', '第一条'))
    await pending
    expect(value<ConversationEntry[]>(state, 'conversations')[0]?.name).toBe('第一条')
    expect(value<ConversationEntry>(state, 'selectedConversation').id).toBe('two')
    expect(value<string>(state, 'detailName')).toBe('第二条')
  })
})

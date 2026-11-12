import { describe, expect, it } from 'vitest'
import { createSSRApp, defineComponent, h } from 'vue'
import { renderToString } from '@vue/server-renderer'
import IntentGroupCapabilities from './IntentGroupCapabilities.vue'
import type { IntentGroupCapability } from '@/services/intent'

const pools = [{ id: 'grok', name: 'grok' }, { id: 'gemini', name: 'gemini' }, { id: 'code', name: '代码组' }]

// 通过实际模板和表单事件验证已有分组的显示与编辑，不使用浏览器。
async function renderEditor(model: IntentGroupCapability[]) {
  let changed: IntentGroupCapability[] | undefined
  const fields = new Map<string, { value: unknown; edit: (value: unknown) => void }>()
  const control = defineComponent({
    inheritAttrs: false,
    props: {
      modelValue: { type: [Array, Number], required: true },
      label: { type: String, required: true }
    },
    emits: ['update:modelValue'],
    setup(props, { attrs, emit }) {
      fields.set(String(attrs['aria-label']), { value: props.modelValue, edit: value => emit('update:modelValue', value) })
      return () => h('label', props.label)
    }
  })
  const app = createSSRApp(IntentGroupCapabilities, {
    modelValue: model, pools, tasks: [], disabled: false, useFeedback: false,
    'onUpdate:modelValue': (value: IntentGroupCapability[]) => { changed = value }
  })
  app.component('VSelect', control)
  app.component('VCombobox', control)
  app.component('VTextField', control)
  const html = await renderToString(app)
  return { html, fields, changed: () => changed }
}

describe('现有渠道分组的能力配置', () => {
  it('未保存能力时直接显示全部现有分组，无需添加或再次选择分组', async () => {
    const editor = await renderEditor([])
    for (const pool of pools) {
      expect(editor.html).toContain(pool.name + '的任务能力')
      expect(editor.fields.get(pool.name + '适合的任务')?.value).toEqual([])
      expect(editor.fields.get(pool.name + '可处理的难度')?.value).toEqual([])
    }
    expect(editor.html).not.toContain('添加分组')
    expect(editor.html).not.toContain('目标分组')
    expect(editor.html).not.toContain('移除配置')
    expect(editor.changed()).toBeUndefined()
  })

  it('按分组 ID 回填并编辑，清空能力不会删除真实分组或修改其他组', async () => {
    const original: IntentGroupCapability[] = [{ poolId: 'gemini', tasks: ['translation'], tiers: [0, 1], cost: 7 }]
    let editor = await renderEditor(original)
    expect(editor.fields.get('gemini适合的任务')?.value).toEqual(['translation'])
    expect(editor.fields.get('gemini可处理的难度')?.value).toEqual([0, 1])
    editor.fields.get('grok适合的任务')!.edit(['code_generation'])
    let draft = editor.changed()!
    expect(draft).toContainEqual({ poolId: 'grok', tasks: ['code_generation'], tiers: [], cost: 0 })
    expect(draft).toContainEqual(original[0])

    editor = await renderEditor(draft)
    editor.fields.get('grok可处理的难度')!.edit([1, 2])
    draft = editor.changed()!
    expect(draft.find(group => group.poolId === 'grok')?.tiers).toEqual([1, 2])

    editor = await renderEditor(draft)
    editor.fields.get('grok适合的任务')!.edit(null)
    editor = await renderEditor(editor.changed()!)
    editor.fields.get('grok可处理的难度')!.edit(null)
    expect(editor.changed()).toEqual(original)
    expect(original).toEqual([{ poolId: 'gemini', tasks: ['translation'], tiers: [0, 1], cost: 7 }])
    const cleared = await renderEditor(editor.changed()!)
    expect(cleared.html).toContain('grok的任务能力')
    expect(pools).toHaveLength(3)
  })
})

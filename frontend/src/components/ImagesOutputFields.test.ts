// 输出格式切换控制 GIF 专属表单；使用 SSR 和控件 stub，不启动管理服务。
import { describe, expect, it } from 'vitest'
import { createSSRApp, defineComponent, h } from 'vue'
import { renderToString } from '@vue/server-renderer'
import ImagesOutputFields from './ImagesOutputFields.vue'

const control = defineComponent({
  props: {
    label: { type: String, default: '' },
    modelValue: { type: [String, Number], default: '' }
  },
  setup: (props, { slots }) => () => props.label
    ? h('label', `${props.label}:${props.modelValue}`)
    : h('div', slots.default?.())
})

describe('Images 输出配置表单', () => {
  it.each(['gif', 'png'] as const)('输出格式 %s 控制 GIF 参数可见性', async outputFormat => {
    const app = createSSRApp(ImagesOutputFields, { outputFormat, frameCount: 9, frameDurationMs: 200, serviceType: 'responses' })
    app.component('VSelect', control)
    app.component('VTextField', control)
    app.component('VRow', control)
    app.component('VCol', control)
    const html = await renderToString(app)
    expect(html).toContain('输出格式')
    if (outputFormat === 'gif') {
      expect(html).toContain('GIF 帧数:9')
      expect(html).toContain('每帧时长（毫秒）:200')
      expect(html).toContain('对 Responses 渠道同样生效')
    } else {
      expect(html).not.toContain('GIF 帧数')
    }
  })
})

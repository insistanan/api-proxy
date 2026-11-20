// 输出格式切换控制 GIF 专属表单；使用 SSR 渲染真实控件，不启动管理服务。
import { describe, expect, it } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'
import ImagesOutputFields from '@/features/channels/components/form/ImagesOutputFields.vue'

describe('Images 输出配置表单', () => {
  it.each(['gif', 'png'] as const)('输出格式 %s 控制 GIF 参数可见性', async outputFormat => {
    const app = createSSRApp(ImagesOutputFields, { outputFormat, frameCount: 9, frameDurationMs: 200, serviceType: 'responses' })
    const html = await renderToString(app)
    expect(html).toContain('输出格式')
    if (outputFormat === 'gif') {
      expect(html).toContain('GIF 帧数')
      expect(html).toMatch(/<input[^>]+type="number"[^>]+value="9"/)
      expect(html).toContain('每帧时长（毫秒）')
      expect(html).toMatch(/<input[^>]+type="number"[^>]+value="200"/)
    } else {
      expect(html).not.toContain('GIF 帧数')
    }
  })
})

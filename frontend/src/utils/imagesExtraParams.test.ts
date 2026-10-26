import { describe, expect, it } from 'vitest'
import { useChannelEditor } from '@/composables/useChannelEditor'
import type { Channel } from '@/types/channel'
import { compileImagesExtraParams, rowsFromImagesExtraParams } from './imagesExtraParams'

const channel = (imagesExtraParams?: Record<string, string>): Channel => ({
  name: 'images',
  serviceType: 'openai',
  baseUrl: 'https://api.example.com',
  apiKeys: ['k'],
  index: 0,
  imagesExtraParams
})

describe('Images 自定义请求参数', () => {
  it('按文本保存，并拒绝空键、重复键、保留字段和残缺 JSON', () => {
    expect(compileImagesExtraParams([
      { key: ' watermark ', value: ' false ' },
      { key: 'label', value: '"false"' },
      { key: 'count', value: '2' },
      { key: 'plain', value: 'hello' }
    ])).toEqual({
      params: { watermark: 'false', label: '"false"', count: '2', plain: 'hello' },
      error: ''
    })
    expect(compileImagesExtraParams([{ key: ' ', value: '1' }]).error).toContain('不能为空')
    expect(compileImagesExtraParams([{ key: 'model', value: 'x' }]).error).toContain('保留字段')
    expect(compileImagesExtraParams([{ key: 'stream', value: 'false' }]).error).toContain('保留字段')
    expect(compileImagesExtraParams([{ key: 'a', value: '1' }, { key: ' a ', value: '2' }]).error).toContain('重复')
    expect(compileImagesExtraParams([{ key: 'meta', value: '{' }]).error).toContain('不是合法 JSON')
    expect(compileImagesExtraParams([{ key: 'code', value: '01' }]).error).toBe('')
  })

  it('编辑草稿从渠道配置载入参数行，重置后清空', () => {
    const editor = useChannelEditor('images')
    editor.loadChannelData(channel({ watermark: 'false', output_format: 'png' }))
    expect(editor.form.imagesExtraParams).toEqual(rowsFromImagesExtraParams({
      watermark: 'false',
      output_format: 'png'
    }))
    expect(editor.form.imagesForceNonStream).toBe(false)
    editor.loadChannelData({ ...channel({ watermark: 'false' }), imagesForceNonStream: true })
    expect(editor.form.imagesForceNonStream).toBe(true)
    editor.resetForm()
    expect(editor.form.imagesExtraParams).toEqual([])
    expect(editor.form.imagesForceNonStream).toBe(false)
  })
})

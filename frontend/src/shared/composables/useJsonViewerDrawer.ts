import { readonly, ref, shallowRef } from 'vue'

export interface JsonViewerSource {
  data: unknown
  title?: string
  meta?: string
}

const isOpen = ref(false)
const source = shallowRef<JsonViewerSource | null>(null)

function close() {
  isOpen.value = false
  source.value = null
}
function openSource(next: JsonViewerSource) {
  source.value = next
  isOpen.value = true
}

// 只保存当前预览，不依赖业务 API，不将可能含敏感信息的 JSON 写入持久化状态。
export function useJsonViewerDrawer() {
  return { isOpen: readonly(isOpen), source: readonly(source), openSource, close }
}

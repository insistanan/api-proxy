import { computed, ref } from 'vue'

/** 只在服务端读取或提交成功后更新基线，失败时保留可恢复草稿。 */
export function useDraftSnapshot(read: () => unknown) {
  const saved = ref<string>()
  const dirty = computed(() => saved.value !== undefined && JSON.stringify(read()) !== saved.value)
  const markSaved = () => { saved.value = JSON.stringify(read()) }
  return { dirty, markSaved }
}

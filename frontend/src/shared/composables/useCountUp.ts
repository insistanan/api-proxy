import { onScopeDispose, ref, watch, type Ref } from 'vue'

/**
 * 数字滚动动画 composable
 * 当值变化时从旧值平滑过渡到新值
 */
export function useCountUp(targetValue: Ref<number>) {
  const displayValue = ref(String(targetValue.value))
  let frameId: number | undefined
  const cancelAnimation = () => {
    if (frameId !== undefined) cancelAnimationFrame(frameId)
    frameId = undefined
  }
  onScopeDispose(cancelAnimation)

  watch(
    targetValue,
    (newVal: number, oldVal: number | undefined) => {
      cancelAnimation()
      // 从当前显示值续接，避免快速刷新时多条动画同时争写；首次渲染直接展示数据。
      const start = Number(displayValue.value)
      const end = newVal
      if (oldVal === undefined || start === end || window.matchMedia('(prefers-reduced-motion: reduce)').matches || document.hidden) {
        displayValue.value = String(end)
        return
      }
      const duration = 240
      const startTime = performance.now()
      const animate = (now: number) => {
        const elapsed = now - startTime
        const progress = Math.min(elapsed / duration, 1)
        const eased = 1 - Math.pow(1 - progress, 3) // easeOutCubic
        const current = Math.round(start + (end - start) * eased)
        displayValue.value = String(current)
        if (progress < 1) {
          frameId = requestAnimationFrame(animate)
        } else {
          frameId = undefined
        }
      }
      frameId = requestAnimationFrame(animate)
    },
    { immediate: true }
  )

  return { displayValue }
}

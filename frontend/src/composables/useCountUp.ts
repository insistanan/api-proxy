import { ref, watch, type Ref } from 'vue'

/**
 * 数字滚动动画 composable
 * 当值变化时从旧值平滑过渡到新值
 */
export function useCountUp(targetValue: Ref<number>) {
  const displayValue = ref('0')

  watch(
    targetValue,
    (newVal: number, oldVal: number | undefined) => {
      const start = oldVal ?? 0
      const end = newVal
      if (start === end) {
        displayValue.value = String(end)
        return
      }
      const duration = 600 // ms
      const startTime = performance.now()
      const animate = (now: number) => {
        const elapsed = now - startTime
        const progress = Math.min(elapsed / duration, 1)
        const eased = 1 - Math.pow(1 - progress, 3) // easeOutCubic
        const current = Math.round(start + (end - start) * eased)
        displayValue.value = String(current)
        if (progress < 1) {
          requestAnimationFrame(animate)
        }
      }
      requestAnimationFrame(animate)
    },
    { immediate: true }
  )

  return { displayValue }
}

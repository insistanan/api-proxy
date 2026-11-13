import { onUnmounted, type Ref } from 'vue'

/**
 * 自动刷新定时器 composable。
 *
 * 收敛背景：GlobalStatsChart 与 KeyTrendChart 各自维护一套几乎相同的
 * setInterval 定时器逻辑（start/stop + onUnmounted 清理）。
 * 本 composable 把"每隔 interval 调用一次 refresh，且当前刷新进行中则跳过"的统一逻辑抽出来。
 *
 * 注意：不自动 start。组件在自身 onMounted 中调用 start() 以配合首次加载时机，
 * composable 只负责组件卸载时自动 stop() 清理定时器。
 *
 * @param refresh 每次 tick 调用的刷新函数（返回 Promise 表示刷新中）
 * @param isBusy 刷新进行中的状态引用（用于跳过并发 tick）
 * @param interval 刷新间隔（毫秒）
 * @returns { start, stop } 控制句柄
 */
export function useAutoRefresh(
  refresh: () => Promise<void> | void,
  isBusy: Ref<boolean>,
  interval = 2000,
) {
  let timer: ReturnType<typeof setInterval> | null = null

  const stop = () => {
    if (timer) {
      clearInterval(timer)
      timer = null
    }
  }

  const start = () => {
    stop()
    timer = setInterval(() => {
      // 跳过并发：前一次刷新未结束时不再触发，避免 stale 覆盖
      if (!isBusy.value && (typeof document === 'undefined' || !document.hidden)) {
        void refresh()
      }
    }, interval)
  }

  // 组件卸载时自动清理，避免定时器泄漏
  onUnmounted(stop)

  return { start, stop }
}

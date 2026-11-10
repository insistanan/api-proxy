import { defineAsyncComponent, type Component } from 'vue'
import ChartLoadStatus from '@/components/ChartLoadStatus.vue'

// 折叠状态不加载图表模块；加载失败必须在页面上显示，避免出现无法解释的空白区域。
export function defineLazyChart<T extends Component>(loader: () => Promise<{ default: T }>) {
  return defineAsyncComponent({
    loader,
    loadingComponent: ChartLoadStatus,
    errorComponent: ChartLoadStatus,
    delay: 200,
    timeout: 15000
  })
}

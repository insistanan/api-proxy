import { ref } from 'vue'

interface Toast {
  id: number
  message: string
  type: 'success' | 'error' | 'warning' | 'info'
  show?: boolean
}

const toasts = ref<Toast[]>([])
let toastId = 0

/**
 * Toast 消息管理 composable
 * 全局单例，供 App.vue 和 Store 使用
 */
export function useToast() {
  const getToastColor = (type: string) => {
    const colorMap: Record<string, string> = {
      success: 'success',
      error: 'error',
      warning: 'warning',
      info: 'info',
    }
    return colorMap[type] || 'info'
  }

  const getToastIcon = (type: string) => {
    const iconMap: Record<string, string> = {
      success: 'mdi-check-circle',
      error: 'mdi-alert-circle',
      warning: 'mdi-alert',
      info: 'mdi-information',
    }
    return iconMap[type] || 'mdi-information'
  }

  const showToast = (message: string, type: 'success' | 'error' | 'warning' | 'info' = 'info') => {
    const toast: Toast = { id: ++toastId, message, type, show: true }
    toasts.value.push(toast)
    setTimeout(() => {
      const index = toasts.value.findIndex(t => t.id === toast.id)
      if (index > -1) toasts.value.splice(index, 1)
    }, 3000)
  }

  const showErrorToast = (message: string) => {
    showToast(message, 'error')
  }

  const showSuccessToast = (message: string) => {
    showToast(message, 'info')
  }

  return {
    toasts,
    getToastColor,
    getToastIcon,
    showToast,
    showErrorToast,
    showSuccessToast,
  }
}

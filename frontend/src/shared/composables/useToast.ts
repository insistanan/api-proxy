import { ref } from 'vue'

export type ToastType = 'success' | 'error' | 'warning' | 'info'

interface Toast {
  id: number
  message: string
  type: ToastType
  show: boolean
}

export const toastIcons: Record<ToastType, string> = {
  success: 'mdi-check-circle',
  error: 'mdi-alert-circle',
  warning: 'mdi-alert',
  info: 'mdi-information'
}

const TOAST_DURATION_MS = 3000
const toasts = ref<Toast[]>([])
let toastId = 0

/** 全局单例消息队列：App.vue 负责渲染，任意页面和 composable 都可以推送。 */
export function useToast() {
  const showToast = (message: string, type: ToastType = 'info') => {
    const id = ++toastId
    toasts.value.push({ id, message, type, show: true })
    setTimeout(() => {
      toasts.value = toasts.value.filter(toast => toast.id !== id)
    }, TOAST_DURATION_MS)
  }

  return { toasts, showToast, TOAST_DURATION_MS }
}

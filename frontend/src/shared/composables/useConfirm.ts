import { ref } from 'vue'

/** 一次待确认的危险操作。 */
export interface ConfirmRequest {
  /** 弹窗正文，说明操作后果。 */
  message: string
  /** 弹窗标题，默认「请确认」。 */
  title?: string
  confirmText?: string
  cancelText?: string
  /** 确认按钮颜色，默认 error。 */
  confirmColor?: string
  /** 用户确认后执行的动作。 */
  action: () => void | Promise<void>
  /** 用户取消时的回调，用于需要同步结果的场景（如路由离开守卫）。 */
  onCancel?: () => void
}

/**
 * 全站危险操作二次确认的唯一入口。
 * 页面挂载一个 ConfirmDialog，通过 askConfirm 登记动作，避免各处自建弹窗或使用 window.confirm。
 */
export function useConfirm() {
  const pending = ref<ConfirmRequest | null>(null)

  const askConfirm = (request: ConfirmRequest) => {
    pending.value = request
  }

  const acceptConfirm = async () => {
    const request = pending.value
    pending.value = null
    if (request) await request.action()
  }

  const cancelConfirm = () => {
    const request = pending.value
    pending.value = null
    request?.onCancel?.()
  }

  return { pending, askConfirm, acceptConfirm, cancelConfirm }
}

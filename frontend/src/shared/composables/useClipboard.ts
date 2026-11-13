import { useToast } from '@/shared/composables/useToast'

/**
 * 全站复制到剪贴板的唯一入口：统一失败提示，避免各处自写 try/catch。
 */
export function useClipboard() {
  const { showToast } = useToast()

  const copyText = async (text: string, successMessage?: string): Promise<boolean> => {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text)
      } else {
        // 自托管常以 http 内网地址访问，此时没有 Clipboard API，退回 execCommand。
        fallbackCopy(text)
      }
      if (successMessage) showToast(successMessage, 'success')
      return true
    } catch {
      showToast('复制失败，请手动复制', 'error')
      return false
    }
  }

  return { copyText }
}

function fallbackCopy(text: string) {
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  const copied = document.execCommand('copy')
  document.body.removeChild(textarea)
  if (!copied) throw new Error('copy command rejected')
}

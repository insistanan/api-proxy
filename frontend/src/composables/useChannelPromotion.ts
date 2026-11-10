// 渠道促销编辑：保持恢复熔断、写入状态、设置促销及部分提交错误的顺序。
import { ref } from 'vue'
import { ApiError, channelApiByType, type ApiTab, type Channel } from '@/services/api'
import { useChannelStore } from '@/stores/channel'

interface PromotionEmit {
  (event: 'success', message: string): void
  (event: 'error', message: string): void
}

export function useChannelPromotion(props: { channelType: ApiTab }, emit: PromotionEmit) {
  const channelStore = useChannelStore()
  // 促销弹窗相关
  const showPromotionDialog = ref(false)
  const promotionChannel = ref<Channel | null>(null)
  const promotionChannelType = ref<ApiTab>('messages')
  const isSavingPromotion = ref(false)
  const promotionError = ref('')
  const promotionDuration = ref(5) // 默认 5 分钟
  const promotionCount = ref(0) // 默认 0 = 不限制次数

  const openPromotionDialog = (channel: Channel) => {
    if (isSavingPromotion.value) return
    promotionChannel.value = { ...channel }
    promotionChannelType.value = props.channelType
    promotionError.value = ''
    promotionDuration.value = 5
    promotionCount.value = 0
    showPromotionDialog.value = true
  }

  const closePromotionDialog = () => {
    if (isSavingPromotion.value) return
    showPromotionDialog.value = false
    promotionChannel.value = null
  }

  const confirmPromotion = async () => {
    if (!promotionChannel.value || isSavingPromotion.value) return
    const channel = promotionChannel.value
    const tab = promotionChannelType.value
    const durationMinutes = Number(promotionDuration.value)
    const count = Number(promotionCount.value)
    const ca = channelApiByType(tab)
    let changed = false
    let settingPromotion = false
    isSavingPromotion.value = true
    promotionError.value = ''
    try {
      if (!Number.isSafeInteger(durationMinutes) || durationMinutes < 0 ||
          !Number.isSafeInteger(durationMinutes * 60) || !Number.isSafeInteger(count) || count < 0) {
        throw new Error('促销时长和次数必须是有效的非负整数')
      }
      if (channel.status === 'suspended' && (durationMinutes > 0 || count > 0)) {
        // resume 只重置熔断；持久化状态必须单独设为 active。
        await ca.resumeChannel(channel.index)
        await ca.setStatus(channel.index, 'active')
        channel.status = 'active'
        changed = true
      }

      settingPromotion = true
      await ca.setPromotion(channel.index, durationMinutes * 60, count)
      changed = true
      const durationText = durationMinutes > 0 ? `${durationMinutes}分钟` : ''
      const countText = count > 0 ? `${count}次成功请求` : ''
      const combined = [durationText, countText].filter(Boolean).join(' / ')
      emit('success', combined ? `渠道 ${channel.name} 已设置促销（${combined}，任一限制达到即结束）` : `渠道 ${channel.name} 已清除促销`)
      showPromotionDialog.value = false
      promotionChannel.value = null
    } catch (error) {
      const errorMessage = error instanceof Error ? error.message : '未知错误'
      if (error instanceof ApiError && error.committed) {
        changed = true
        if (settingPromotion) {
          showPromotionDialog.value = false
          promotionChannel.value = null
          emit('error', `促销变更已写入，但收尾失败: ${errorMessage}。请刷新查看实际状态，请勿重复提交`)
        } else {
          channel.status = 'active'
          promotionError.value = `渠道恢复已写入，但收尾失败: ${errorMessage}。促销尚未设置`
        }
      } else {
        promotionError.value = `${changed ? '渠道已恢复，但促销设置失败' : '设置促销失败'}: ${errorMessage}`
      }
    } finally {
      if (changed) {
        try {
          await channelStore.refreshChannels(tab)
        } catch (error) {
          emit('error', `变更已写入，但刷新失败: ${error instanceof Error ? error.message : '未知错误'}。请刷新查看实际状态`)
        }
      }
      isSavingPromotion.value = false
    }
  }

  return {
    showPromotionDialog, promotionChannel, isSavingPromotion, promotionError,
    promotionDuration, promotionCount, openPromotionDialog, closePromotionDialog, confirmPromotion
  }
}

<template>
  <ConfirmDialog
:model-value="pending !== null" :message="pending?.message ?? ''" :title="pending?.title"
    :confirm-text="pending?.confirmText" @update:model-value="cancelConfirm" @confirm="acceptConfirm" />
</template>
<script setup lang="ts">
import { onScopeDispose, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import ConfirmDialog from '@/shared/components/overlay/ConfirmDialog.vue'
import { useConfirm } from '@/shared/composables/useConfirm'
import { useToast } from '@/shared/composables/useToast'

const props = defineProps<{ dirty: boolean; busy?: boolean }>()
const { pending, askConfirm, acceptConfirm, cancelConfirm } = useConfirm()
const { showToast } = useToast()
const beforeUnload = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }

watch(() => props.dirty || props.busy, active => {
  window.removeEventListener('beforeunload', beforeUnload)
  if (active) window.addEventListener('beforeunload', beforeUnload)
}, { immediate: true })
onScopeDispose(() => window.removeEventListener('beforeunload', beforeUnload))
onBeforeRouteLeave(() => {
  if (props.busy) { showToast('操作仍在进行，请等待完成后离开', 'info'); return false }
  if (!props.dirty) return true
  return new Promise<boolean>(resolve => askConfirm({
    message: '当前页面有未保存的修改，确定放弃并离开吗？', confirmText: '放弃修改',
    action: () => resolve(true), onCancel: () => resolve(false)
  }))
})
</script>

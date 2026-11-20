<template>
  <UiDialog :model-value="modelValue" :max-width="widths[size]" :title="title" :persistent="busy" scrollable @update:model-value="toggle">
    <UiCard class="app-dialog">
      <header class="app-dialog__head">
        <div class="app-dialog__heading">
          <h2>{{ title }}</h2>
          <p v-if="description">{{ description }}</p>
        </div>
        <div class="app-dialog__head-actions">
          <slot name="header-actions"></slot>
          <UiButton
            icon="mdi-close"
            variant="text"
            size="small"
            :aria-label="`关闭${title}`"
            :disabled="busy"
            @click="close"
          />
        </div>
      </header>
      <hr class="border-0 border-t border-border" />
      <div class="app-dialog__body"><slot></slot></div>
      <template v-if="$slots.actions">
        <hr class="border-0 border-t border-border" />
        <footer class="app-dialog__actions"><slot name="actions"></slot></footer>
      </template>
    </UiCard>
  </UiDialog>
</template>

<script setup lang="ts">
import UiButton from '@/shared/ui/button/Button.vue'
import UiCard from '@/shared/ui/card/Card.vue'
import UiDialog from '@/shared/ui/overlay/Dialog.vue'
// 全站统一弹窗外壳：右上角关闭按钮常驻，底部按钮区右对齐。
// 未进行中的操作允许点击空白或按 Esc 关闭；busy 为真时锁定，避免中断保存。
withDefaults(defineProps<{
  modelValue: boolean
  title: string
  description?: string
  size?: 'sm' | 'md' | 'lg' | 'xl'
  busy?: boolean
}>(), {
  size: 'md',
  busy: false
})

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()

const widths = { sm: 440, md: 640, lg: 900, xl: 1200 } as const

const toggle = (value: boolean) => emit('update:modelValue', value)
const close = () => emit('update:modelValue', false)
</script>

<style scoped>
@reference "@/styles/index.css";

.app-dialog { @apply flex flex-col [max-height:86dvh]; }
.app-dialog__head { @apply flex shrink-0 items-start justify-between gap-4 [padding:20px_16px_18px_24px]; }
.app-dialog__heading { @apply min-w-0; }
h2 { @apply m-0 text-foreground [font-size:1.0625rem] font-semibold [line-height:1.4] [letter-spacing:-0.01em]; }
p { @apply [margin:6px_0_0] text-muted-foreground [font-size:0.8125rem] [line-height:1.6]; }
.app-dialog__head-actions { @apply flex items-center shrink-0 gap-1 [margin-top:-4px]; }
.app-dialog__body { @apply [flex:1_1_auto] min-h-0 [padding:20px_24px] overflow-y-auto; }
.app-dialog__actions { @apply flex shrink-0 flex-wrap items-center justify-end gap-2 bg-muted/20 [padding:14px_24px]; }
@media (max-width: 600px) {
  .app-dialog__head { @apply [padding:16px_8px_14px_16px]; }
  .app-dialog__body { @apply p-4; }
  .app-dialog__actions { @apply [padding:12px_16px]; }
}
</style>

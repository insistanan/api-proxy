<template>
  <v-dialog :model-value="modelValue" :max-width="widths[size]" :persistent="busy" scrollable @update:model-value="toggle">
    <v-card class="app-dialog">
      <header class="app-dialog__head">
        <div class="app-dialog__heading">
          <h2>{{ title }}</h2>
          <p v-if="description">{{ description }}</p>
        </div>
        <div class="app-dialog__head-actions">
          <slot name="header-actions"></slot>
          <v-btn
            icon="mdi-close"
            variant="text"
            size="small"
            :aria-label="`关闭${title}`"
            :disabled="busy"
            @click="close"
          />
        </div>
      </header>
      <v-divider />
      <div class="app-dialog__body"><slot></slot></div>
      <template v-if="$slots.actions">
        <v-divider />
        <footer class="app-dialog__actions"><slot name="actions"></slot></footer>
      </template>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
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
.app-dialog { display: flex; flex-direction: column; max-height: 86vh; }
.app-dialog__head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; padding: 20px 16px 16px 24px; }
.app-dialog__heading { min-width: 0; }
h2 { margin: 0; color: rgb(var(--v-theme-on-surface)); font-size: 1.0625rem; font-weight: 600; line-height: 1.4; letter-spacing: -0.01em; }
p { margin: 6px 0 0; color: rgb(var(--v-theme-on-surface-variant)); font-size: 0.8125rem; line-height: 1.6; }
.app-dialog__head-actions { display: flex; align-items: center; flex-shrink: 0; gap: 4px; margin-top: -4px; }
.app-dialog__body { flex: 1 1 auto; min-height: 0; padding: 20px 24px; overflow-y: auto; }
.app-dialog__actions { display: flex; align-items: center; justify-content: flex-end; gap: 8px; padding: 14px 24px; }
@media (max-width: 600px) {
  .app-dialog__head { padding: 16px 8px 14px 16px; }
  .app-dialog__body { padding: 16px; }
  .app-dialog__actions { padding: 12px 16px; }
}
</style>

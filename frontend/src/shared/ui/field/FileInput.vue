<template>
  <label class="block min-w-0 text-xs font-medium text-muted-foreground"
    >{{ label
    }}<input
      :disabled="disabled"
      :data-disabled="disabled || undefined"
      :accept="accept"
      type="file"
      class="field-surface mt-1 block w-full border-input p-2 text-sm outline-none file:mr-3 file:rounded-control file:border-0 file:bg-muted file:px-3 file:py-1 file:text-foreground"
      @change="change"
  /></label>
</template>
<script setup lang="ts">
defineProps<{
  modelValue?: File | File[] | null
  label?: string
  accept?: string
  disabled?: boolean
  density?: string
  hideDetails?: boolean
  variant?: string
  prependIcon?: string
  showSize?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: File | null] }>()
function change(event: Event) {
  emit('update:modelValue', (event.target as HTMLInputElement).files?.[0] ?? null)
}
</script>

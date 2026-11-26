<template>
  <label class="inline-flex min-h-9 items-center gap-2 text-sm"
    ><input
      type="radio"
      :checked="group.value.value === value"
      class="size-4 accent-primary"
      @change="group.select(value)"
    />{{ label }}</label
  >
</template>
<script setup lang="ts">
import { inject } from 'vue'
import { radioKey } from '@/shared/ui/check/context'
defineProps<{ value: unknown; label?: string }>()
const group = inject(radioKey)
if (!group) throw new Error('Radio 必须放在 RadioGroup 内')
</script>

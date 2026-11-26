<template>
  <form novalidate>
    <fieldset :disabled="disabled" class="min-w-0 border-0 p-0"><slot></slot></fieldset>
  </form>
</template>
<script setup lang="ts">
import { computed, provide } from 'vue'
import { formKey } from '@/shared/ui/form/context'
const props = defineProps<{ disabled?: boolean }>()
const fields = new Set<() => Promise<boolean>>()
provide(formKey, {
  disabled: computed(() => !!props.disabled),
  register: validate => {
    fields.add(validate)
    return () => fields.delete(validate)
  }
})
async function validate() {
  return { valid: (await Promise.all([...fields].map(field => field()))).every(Boolean) }
}
defineExpose({ validate })
</script>

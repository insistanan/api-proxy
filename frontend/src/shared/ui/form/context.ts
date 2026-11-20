import { computed, inject, onBeforeUnmount, onMounted, ref, watch, type InjectionKey, type Ref } from 'vue'
export type ValidationRule<T = unknown> = (value: T) => boolean | string | Promise<boolean | string>
export const formKey: InjectionKey<{
  disabled: Ref<boolean>
  register: (validate: () => Promise<boolean>) => () => void
}> = Symbol('form')
export function useField<T>(props: {
  modelValue?: T
  disabled?: boolean
  rules?: ValidationRule<T>[]
  errorMessages?: string | string[]
  error?: boolean
}) {
  const form = inject(formKey, null)
  const touched = ref(false)
  const validationError = ref('')
  const disabled = computed(() => props.disabled || form?.disabled.value)
  const message = computed(
    () =>
      (Array.isArray(props.errorMessages) ? props.errorMessages.join('；') : props.errorMessages) ||
      validationError.value
  )
  let revision = 0
  async function validate() {
    if (disabled.value) return true
    touched.value = true
    const current = ++revision
    for (const rule of props.rules ?? []) {
      const result = await rule(props.modelValue as T)
      if (current !== revision) return false
      if (result !== true) {
        validationError.value = typeof result === 'string' ? result : '请检查此项'
        return false
      }
    }
    validationError.value = ''
    return !props.error && !message.value
  }
  watch(
    () => props.modelValue,
    () => {
      if (touched.value) return validate()
    }
  )
  let unregister: (() => void) | undefined
  onMounted(() => {
    unregister = form?.register(validate)
  })
  onBeforeUnmount(() => unregister?.())
  return { disabled, message, validate }
}

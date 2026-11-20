import type { InjectionKey, Ref } from 'vue'
export const toggleKey: InjectionKey<{
  value: Ref<unknown>
  disabled: Ref<boolean>
  select: (value: unknown) => void
}> = Symbol('toggle-group')

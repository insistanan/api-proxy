import type { InjectionKey, Ref } from 'vue'
export const radioKey: InjectionKey<{ value: Ref<unknown>; select: (value: unknown) => void }> = Symbol('radio-group')

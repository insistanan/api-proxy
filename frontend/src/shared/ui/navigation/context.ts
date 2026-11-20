import type { InjectionKey, Ref } from 'vue'
export const listKey: InjectionKey<{ selected: Ref<unknown[]>; select: (value: unknown) => void }> = Symbol('list')
export const accordionKey: InjectionKey<string> = Symbol('accordion')

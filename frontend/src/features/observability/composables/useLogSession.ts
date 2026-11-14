import { inject, provide, reactive, type InjectionKey } from 'vue'
import { createLogsState, useLogsStore } from '@/features/observability/stores/logs'

export function createLogSession() {
  return reactive(createLogsState())
}

type LogSession = ReturnType<typeof createLogSession>
const logSessionKey: InjectionKey<LogSession> = Symbol('log-session')

export function provideLogSession() {
  const session = createLogSession()
  provide(logSessionKey, session)
  return session
}

// 独立弹窗拥有自己的选择、分页和缓存；普通日志页继续使用 Pinia 实例。
export function useLogSession(): LogSession {
  return inject(logSessionKey, undefined) ?? useLogsStore()
}

// 路由、菜单事件与偏好状态的公开契约：移除评测后其他渠道操作仍可使用。
import { describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import router from './index'
import { usePreferencesStore } from '@/stores/preferences'
import { channelMenuActions } from '@/components/ChannelQuickMenu.vue'

vi.mock('vue-router', () => ({
  createWebHistory: () => ({}),
  createRouter: (options: { routes: { path: string; name?: string }[] }) => ({ getRoutes: () => options.routes })
}))

describe('评测功能移除后的入口契约', () => {
  it('路由移除评测，保留渠道、设置和客户端页面', () => {
    const routes = router.getRoutes()
    expect(routes.map(route => route.path)).not.toContain('/eval')
    expect(routes.map(route => route.name)).not.toContain('eval')
    expect(routes.map(route => route.path)).toEqual(expect.arrayContaining(['/channels/:type', '/settings', '/pi-agent', '/opencode', '/skills', '/mcp']))
  })

  it('渠道菜单移除评测动作，保留快捷测试、促销和延迟检查', () => {
    expect(channelMenuActions).not.toContain('eval')
    expect(channelMenuActions).toEqual(expect.arrayContaining(['quickTest', 'promotion', 'ping', 'edit']))
  })

  it('偏好设置不再保存评测选择，主题和伪装设置可继续修改', () => {
    setActivePinia(createPinia())
    const preferences = usePreferencesStore()
    expect(Object.keys(preferences.$state).some(key => key.startsWith('eval'))).toBe(false)
    expect(preferences).not.toHaveProperty('setEvalLastSelection')
    preferences.setDarkMode('dark')
    preferences.setClientDisguise('messages', true)
    expect(preferences.darkModePreference).toBe('dark')
    expect(preferences.claudeCodeDisguiseEnabled).toBe(true)
  })
})

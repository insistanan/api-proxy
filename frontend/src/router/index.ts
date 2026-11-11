import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  {
    path: '/',
    redirect: '/channels/messages'  // 默认跳转到 Messages
  },
  {
    path: '/conversations',
    name: 'conversations',
    component: () => import('@/views/ConversationsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/logs',
    name: 'request-logs',
    component: () => import('@/views/RequestLogsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/blocked-logs',
    name: 'blocked-logs',
    component: () => import('@/views/BlockedLogsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/skills',
    name: 'skills',
    component: () => import('@/views/SkillsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/mcp',
    name: 'mcp',
    component: () => import('@/views/McpView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/opencode',
    name: 'opencode',
    component: () => import('@/views/OpenCodeView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/claude-code',
    name: 'claude-code',
    component: () => import('@/views/ClaudeCodeView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/gpt',
    name: 'gpt',
    component: () => import('@/views/GptView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/pi-agent',
    name: 'pi-agent',
    component: () => import('@/views/PiAgentView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/dsh',
    name: 'dsh',
    component: () => import('@/views/DshView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/views/SettingsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/channels/:type',  // 协议参数由 ChannelsView 校验，包含 images
    name: 'channels',
    component: () => import('@/views/ChannelsView.vue'),  // 懒加载
    props: true,  // 将路由参数作为 props 传递
    meta: { requiresAuth: true }
  }
]

const router = createRouter({
  history: createWebHistory(),  // 使用 HTML5 History 模式
  routes
})

export default router

import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  {
    path: '/datasets',
    name: 'datasets',
    component: () => import('@/features/extensions/datasets/views/DatasetsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/',
    redirect: '/channels/messages'  // 默认跳转到 Messages
  },
  {
    path: '/conversations',
    name: 'conversations',
    component: () => import('@/features/observability/views/ConversationsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/usage-journal',
    name: 'usage-journal',
    component: () => import('@/features/usage-journal/views/UsageJournalView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/logs',
    name: 'request-logs',
    component: () => import('@/features/observability/views/RequestLogsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/blocked-logs',
    name: 'blocked-logs',
    component: () => import('@/features/observability/views/BlockedLogsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/skills',
    name: 'skills',
    component: () => import('@/features/extensions/skills/views/SkillsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/mcp',
    name: 'mcp',
    component: () => import('@/features/extensions/mcp/views/McpView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/opencode',
    name: 'opencode',
    component: () => import('@/features/clients/views/OpenCodeView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/claude-code',
    name: 'claude-code',
    component: () => import('@/features/clients/views/ClaudeCodeView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/gpt',
    name: 'gpt',
    component: () => import('@/features/clients/views/GptView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/pi-agent',
    name: 'pi-agent',
    component: () => import('@/features/clients/views/PiAgentView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/dsh',
    name: 'dsh',
    component: () => import('@/features/clients/views/DshView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/settings',
    name: 'settings',
    component: () => import('@/features/settings/views/SettingsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/channels/:type',  // 协议参数由 ChannelsView 校验，包含 images
    name: 'channels',
    component: () => import('@/features/channels/views/ChannelsView.vue'),  // 懒加载
    props: true,  // 将路由参数作为 props 传递
    meta: { requiresAuth: true }
  }
]

const router = createRouter({
  history: createWebHistory(),  // 使用 HTML5 History 模式
  routes
})

export default router

<template>
  <!--
    根节点必须是单个真实 DOM 元素：
    App.vue 用 <transition mode="out-in"> 包裹 router-view，Transition 需要一个
    可动画的元素节点来触发 afterLeave，才会挂载下一个组件。若这里渲染 fragment
    （多根），或根节点是同样多根的 ChannelOrchestration（v-card + v-dialog...），
    Transition 会递归下探到 fragment 而拿不到元素，isLeaving 卡死，切换路由后
    整块空白（刷新页面走 appear 路径才恢复）。
  -->
  <div class="channels-view">
    <!-- 渠道编排（高密度列表模式） -->
    <ChannelOrchestration
      v-if="channelStore.currentChannelsData.channels?.length"
      :channels="channelStore.currentChannelsData.channels"
      :current-channel-index="channelStore.currentChannelsData.current ?? 0"
      :channel-type="channelType"
      :dashboard-metrics="channelStore.currentDashboardMetrics"
      :dashboard-stats="channelStore.currentDashboardStats"
      :dashboard-recent-activity="channelStore.currentDashboardRecentActivity"
      class="mb-6"
      v-bind="$attrs"
    />

    <!-- 空状态 -->
    <v-card v-else elevation="2" class="text-center pa-12" rounded="lg">
      <v-avatar size="120" color="primary" class="mb-6">
        <v-icon size="60" color="white">mdi-rocket-launch</v-icon>
      </v-avatar>
      <div class="text-h4 mb-4 font-weight-bold">暂无渠道配置</div>
      <div class="text-subtitle-1 text-medium-emphasis mb-8">
        还没有配置任何API渠道，请添加第一个渠道来开始使用代理服务
      </div>
      <v-btn color="primary" size="x-large" prepend-icon="mdi-plus" variant="elevated" @click="emitAddChannel">
        添加第一个渠道
      </v-btn>
    </v-card>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useChannelStore } from '@/stores/channel'
import { useDialogStore } from '@/stores/dialog'
import ChannelOrchestration from '@/components/ChannelOrchestration.vue'
import type { ApiTab } from '@/services/channel-api'

// attrs 已显式 v-bind 到 ChannelOrchestration，关闭自动继承避免落到根 div 上
defineOptions({ inheritAttrs: false })

// 接收路由参数
const props = defineProps<{ type: string }>()

// 转换为类型安全的 channelType
const channelType = computed(() => props.type as ApiTab)

const channelStore = useChannelStore()
const dialogStore = useDialogStore()

const emitAddChannel = () => {
  // 打开添加渠道对话框
  dialogStore.openAddChannelModal(channelStore.activeTab)
}
</script>

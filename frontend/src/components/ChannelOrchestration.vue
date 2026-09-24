<template>
  <v-card elevation="0" rounded="lg" class="channel-orchestration" variant="flat">
    <!-- 调度器统计信息 -->
    <v-card-title class="orchestration-header d-flex align-center justify-space-between py-3 px-0">
      <div class="d-flex align-center">
        <v-icon class="mr-2" color="primary">mdi-swap-vertical-bold</v-icon>
        <span class="text-h6">渠道编排</span>
        <v-chip v-if="isMultiChannelMode" size="small" color="success" variant="tonal" class="ml-3">
          多渠道模式
        </v-chip>
        <v-chip v-else size="small" color="warning" variant="tonal" class="ml-3"> 单渠道模式 </v-chip>
      </div>
      <div class="d-flex align-center ga-2">
        <v-progress-circular v-if="isLoadingMetrics" indeterminate size="16" width="2" color="primary" />
      </div>
    </v-card-title>

    <v-divider />

    <ChannelPoolGrid
      :channels="channels"
      :channel-type="channelType"
      @edit="$emit('edit', $event)"
      @delete="$emit('delete', $event)"
      @error="$emit('error', $event)"
      @success="$emit('success', $event)"
      @refresh="$emit('refresh')"
      @toggle-vision-status="toggleChannelPrimaryStatus"
    >
      <template #channel="{ channel: element, index, pool, move_to_top, move_to_bottom }">
          <div class="channel-item-wrapper">
            <div
              class="channel-row"
              :class="{ 'is-suspended': element.status === 'suspended' }"
              @click="toggleChannelChart(element.index)"
            >
              <!-- SVG 活跃度波形柱状图背景 -->
              <svg class="activity-chart-bg" preserveAspectRatio="none" viewBox="0 0 150 100">
                <!-- 渐变定义（为每个柱子单独定义渐变） -->
                <defs>
                  <linearGradient
                    v-for="(bar, i) in getActivityBars(element.index)"
                    :id="`gradient-${element.index}-${i}`"
                    :key="`gradient-${element.index}-${i}`"
                    x1="0%"
                    y1="0%"
                    x2="0%"
                    y2="100%"
                  >
                    <stop offset="0%" :stop-color="bar.color" stop-opacity="0.9" />
                    <stop offset="100%" :stop-color="bar.color" stop-opacity="0.4" />
                  </linearGradient>
                </defs>
                <!-- 波形柱状图 -->
                <g v-for="(bar, i) in getActivityBars(element.index)" :key="i">
                  <rect
                    :x="bar.x"
                    :y="bar.y"
                    :width="bar.width"
                    :height="bar.height"
                    :fill="`url(#gradient-${element.index}-${i})`"
                    :rx="bar.radius"
                    :ry="bar.radius"
                    class="activity-bar"
                    :class="[
                      `activity-bar-${bar.level}`,
                      { 'activity-bar-latest': i === getActivityBars(element.index).length - 1 }
                    ]"
                  />
                </g>
              </svg>

              <!-- Grid 内容容器 -->
              <div class="channel-row-content">
                <!-- 拖拽手柄 -->
                <div class="drag-handle" @click.stop>
                  <v-icon size="small" color="grey">mdi-drag-vertical</v-icon>
                </div>

            <!-- 优先级序号 -->
            <div class="priority-number" @click.stop>
              <span class="text-caption font-weight-bold">{{ index + 1 }}</span>
            </div>

            <!-- 状态指示器 -->
            <div
              class="channel-status-toggle"
              role="button"
              tabindex="0"
              title="点击切换活跃或熔断"
              @click.stop="toggleChannelPrimaryStatus(element)"
              @keydown.enter.stop="toggleChannelPrimaryStatus(element)"
              @keydown.space.prevent.stop="toggleChannelPrimaryStatus(element)"
            >
              <ChannelStatusBadge :status="element.status || 'active'" :metrics="getChannelMetrics(element.index)" />
            </div>

            <!-- 渠道名称和描述 -->
            <div class="channel-name">
              <span
                class="font-weight-medium channel-name-link"
                tabindex="0"
                role="button"
                @click.stop="$emit('edit', element)"
                @keydown.enter.stop="$emit('edit', element)"
                @keydown.space.stop="$emit('edit', element)"
              >{{ element.name }}</span>
              <!-- 促销期标识 -->
              <v-chip
                v-if="isInPromotion(element)"
                size="x-small"
                color="info"
                variant="flat"
                class="ml-2"
              >
                <v-icon start size="12">mdi-rocket-launch</v-icon>
                {{ formatPromotionRemaining(element.promotionUntil, element.promotionCount) }}
              </v-chip>
              <v-chip
                v-if="element.temporary"
                size="x-small"
                color="warning"
                variant="tonal"
                class="ml-2"
              >
                <v-icon start size="12">mdi-timer-sand</v-icon>
                临时 {{ formatDateTime(element.temporaryUntil) }}
              </v-chip>
              <v-tooltip
                v-if="shouldShowVisionCapability(element)"
                text="支持图片理解"
                location="top"
                :open-delay="150"
                :open-on-focus="false"
              >
                <template #activator="{ props: tooltipProps }">
                  <v-chip
                    v-bind="tooltipProps"
                    size="x-small"
                    color="primary"
                    variant="flat"
                    class="ml-2 vision-default-chip"
                    @click.stop
                  >
                    <v-icon start size="12">mdi-image-search-outline</v-icon>
                    图片
                  </v-chip>
                </template>
              </v-tooltip>
              <!-- 官网链接按钮 -->
              <v-btn
                :href="getWebsiteUrl(element)"
                target="_blank"
                rel="noopener"
                icon
                size="x-small"
                variant="text"
                color="primary"
                class="ml-1"
                title="打开官网"
                @click.stop
              >
                <v-icon size="14">mdi-open-in-new</v-icon>
              </v-btn>
              <span class="text-caption text-medium-emphasis ml-2">{{ element.serviceType }}</span>
              <!-- 展开图标 -->
              <v-icon
                size="x-small"
                class="ml-auto expand-icon"
                :color="expandedChannelIndex === element.index ? 'primary' : 'grey-lighten-1'"
                @click.stop="toggleChannelChart(element.index)"
              >{{ expandedChannelIndex === element.index ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
            </div>

            <!-- 指标显示 — 视觉条 -->
            <div class="channel-metrics" @click.stop>
              <template v-if="getChannelMetrics(element.index)">
                <v-tooltip location="top" :open-delay="200" :open-on-focus="false">
                  <template #activator="{ props: tooltipProps }">
                    <div v-bind="tooltipProps" class="metrics-visual">
                      <!-- 15分钟有请求时显示指标条，否则显示 -- -->
                      <template v-if="get15mStats(element.index)?.requestCount">
                        <!-- 成功率条：成功填充 + 右侧失败占比，失败绝对量单独标注 -->
                        <div class="mini-metric-bar">
                          <span class="metric-bar-label">成功</span>
                          <div class="mmb-track">
                            <div
                              class="mmb-fill"
                              :class="getRateLevel(get15mStats(element.index)?.successRate)"
                              :style="{ width: `${get15mStats(element.index)?.successRate ?? 0}%` }"
                            ></div>
                            <!-- 失败占比：从右侧按窗口内失败率覆盖，一眼看出失败量级 -->
                            <div
                              v-if="(getFailureRate(get15mStats(element.index)) ?? 0) > 0"
                              class="mmb-fill-failure"
                              :style="{ width: `${getFailureRate(get15mStats(element.index)) ?? 0}%` }"
                            ></div>
                            <!-- 指标刷新时重放一次扫光 -->
                            <span
                              :key="`mf-${get15mStats(element.index)?.successRate?.toFixed(0)}`"
                              class="mmb-flash"
                            ></span>
                          </div>
                          <span
                            :key="`mmb-${get15mStats(element.index)?.successRate?.toFixed(0)}`"
                            class="mmb-value"
                            :class="getRateLevel(get15mStats(element.index)?.successRate)"
                          >
                            {{ get15mStats(element.index)?.successRate?.toFixed(0) }}%
                          </span>
                        </div>
                        <!-- 缓存命中率条：与成功率同规格，低命中只是省得少，不按故障报警 -->
                        <div
                          class="mini-metric-bar cache-mini-metric-bar"
                          :class="{ 'is-unavailable': !shouldShowCacheHitRate(get15mStats(element.index)) }"
                        >
                          <span class="metric-bar-label">缓存</span>
                          <div class="mmb-track">
                            <div
                              v-if="shouldShowCacheHitRate(get15mStats(element.index))"
                              class="mmb-fill cache-mmb-fill"
                              :class="getCacheRateLevel(getCacheHitRate(get15mStats(element.index)))"
                              :style="{ width: `${getCacheHitRate(get15mStats(element.index)) ?? 0}%` }"
                            ></div>
                            <span
                              v-if="shouldShowCacheHitRate(get15mStats(element.index))"
                              :key="`cf-${getCacheHitRate(get15mStats(element.index))?.toFixed(0)}`"
                              class="mmb-flash"
                            ></span>
                          </div>
                          <span
                            :key="`cmb-${getCacheHitRate(get15mStats(element.index))?.toFixed(0) ?? 'unknown'}`"
                            class="mmb-value cache-mmb-value"
                            :class="getCacheRateLevel(getCacheHitRate(get15mStats(element.index)))"
                          >
                            {{ shouldShowCacheHitRate(get15mStats(element.index)) ? `${getCacheHitRate(get15mStats(element.index))?.toFixed(0)}%` : '—' }}
                          </span>
                        </div>
                        <!-- 请求数 -->
                        <div class="mini-metric-secondary">
                          <span class="mm-requests">近15分钟 {{ get15mStats(element.index)?.requestCount }} 请求</span>
                          <span
                            v-if="get15mStats(element.index)?.failureCount"
                            class="mm-failures"
                          >失败 {{ get15mStats(element.index)?.failureCount }}</span>
                        </div>
                      </template>
                      <span v-else class="text-caption text-medium-emphasis">--</span>
                    </div>
                  </template>
                  <div class="metrics-tooltip">
                    <div class="text-caption font-weight-bold mb-1">请求统计</div>
                    <div class="metrics-tooltip-row">
                      <span>15分钟:</span>
                      <span>{{ formatStats(get15mStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>1小时:</span>
                      <span>{{ formatStats(get1hStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>6小时:</span>
                      <span>{{ formatStats(get6hStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>24小时:</span>
                      <span>{{ formatStats(get24hStats(element.index)) }}</span>
                    </div>

                    <div class="text-caption font-weight-bold mt-2 mb-1">缓存统计 (Token)</div>
                    <div class="metrics-tooltip-row">
                      <span>15分钟:</span>
                      <span>{{ formatCacheStats(get15mStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>1小时:</span>
                      <span>{{ formatCacheStats(get1hStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>6小时:</span>
                      <span>{{ formatCacheStats(get6hStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>24小时:</span>
                      <span>{{ formatCacheStats(get24hStats(element.index)) }}</span>
                    </div>
                  </div>
                </v-tooltip>
              </template>
              <span v-else class="text-caption text-medium-emphasis">--</span>
            </div>

            <!-- RPM/TPM 显示 -->
            <div class="channel-rpm-tpm" @click.stop>
              <div class="rpm-tpm-values">
                <span class="rpm-value" :class="{ 'has-data': hasActivityData(element.index) }">{{ formatRPM(element.index) }}</span>
                <span class="rpm-tpm-separator">/</span>
                <span class="tpm-value" :class="{ 'has-data': hasActivityData(element.index) }">{{ formatTPM(element.index) }}</span>
              </div>
              <div class="rpm-tpm-labels">
                <span>RPM</span>
                <span>/</span>
                <span>TPM</span>
              </div>
            </div>

            <!-- 延迟显示 -->
            <div class="channel-latency" @click.stop>
              <v-chip
                v-if="isLatencyValid(element, currentTime)"
                size="x-small"
                :color="getLatencyColor(element.latency!)"
                variant="tonal"
              >
                {{ element.latency }}ms
              </v-chip>
            </div>

            <!-- API密钥数量 -->
            <div class="channel-keys" @click.stop>
              <v-chip size="x-small" variant="outlined" class="keys-chip" @click="$emit('edit', element)">
                <v-icon start size="x-small">mdi-key</v-icon>
                {{ element.apiKeys?.length || 0 }}
              </v-chip>
            </div>

            <!-- 操作按钮 -->
            <div class="channel-actions" @click.stop>
              <!-- suspended 状态显示恢复按钮 -->
              <v-btn
                v-if="element.status === 'suspended'"
                icon
                size="x-small"
                variant="text"
                color="warning"
                title="恢复"
                @click="resumeChannel(element.index)"
              >
                <v-icon size="small">mdi-refresh</v-icon>
              </v-btn>

              <ChannelQuickMenu
                :channel="element"
                :position="index"
                :total="pool.channels.length"
                :copied="copiedChannelIndex === element.index"
                :supports-vision-capability="supportsVisionCapability"
                :can-delete="canDeleteChannel(element)"
                :show-eval="channelType !== 'images'"
                :supports-ccs="channelType !== 'images'"
                @edit="$emit('edit', element)"
                @duplicate="duplicateChannel(element.index)"
                @toggle-vision="toggleVisionCapability(element)"
                @copy-config="copyChannelConfig(element)"
                @import-ccs="openCcsImport(element)"
                @quick-test="handleQuickTest(element)"
                @eval="openEvalForChannel(element)"
                @ping="$emit('ping', element.index)"
                @logs="openLogsDialog(element)"
                @promotion="openPromotionDialog(element)"
                @move-top="move_to_top()"
                @move-bottom="move_to_bottom()"
                @resume="resumeChannel(element.index)"
                @suspend="setChannelStatus(element.index, 'suspended')"
                @disable="setChannelStatus(element.index, 'disabled')"
                @deprecate="setChannelStatus(element.index, 'deprecated')"
                @delete="handleDeleteChannel(element)"
              />
            </div>
              </div><!-- .channel-row-content -->

              <!-- 副行：映射 / 评测 / 描述。主行名称列塞不下它们，挤在一起会被压成看不出内容的小方块。
                   映射未配置也照样占位显示——漏配映射是常见误配，必须一眼看得到 -->
              <div class="channel-row-meta" @click.stop>
                <ChannelMappingBlock
                  :channel="element"
                  :channel-type="channelType"
                  @edit="$emit('edit', element)"
                />

                <button
                  v-if="evalChipFor(element)"
                  type="button"
                  class="meta-block meta-block--eval"
                  :class="`is-${evalChipFor(element)!.color}`"
                  :title="`${evalChipFor(element)!.tooltip} — 点击查看该渠道的历史评测`"
                  :aria-label="`查看 ${element.name} 的历史评测记录`"
                  @click.stop="openEvalForChannel(element)"
                >
                  <v-icon size="14" class="meta-block-icon">
                    {{ evalChipFor(element)!.watching ? 'mdi-clock-outline' : 'mdi-clipboard-check-outline' }}
                  </v-icon>
                  <span class="meta-block-label">评测</span>
                  <span class="meta-block-value">{{ evalChipFor(element)!.label }}</span>
                  <span class="meta-block-time">{{ evalChipFor(element)!.ago }}</span>
                  <v-icon size="13" class="meta-block-go">mdi-chevron-right</v-icon>
                </button>

                <span v-if="element.description" class="meta-desc" :title="element.description">
                  {{ element.description }}
                </span>
              </div>
          </div><!-- .channel-row -->

          <!-- 展开的图表区域 -->
          <v-expand-transition>
            <div v-if="expandedChannelIndex === element.index" class="channel-chart-wrapper">
              <KeyTrendChart
                :key="`chart-${channelType}-${element.index}`"
                :channel-id="element.index"
                :channel-type="channelType"
                @close="expandedChannelIndex = null"
              />
            </div>
          </v-expand-transition>
          </div>
      </template>

      <template #vision-actions="{ channel }">
        <ChannelQuickMenu
          :channel="channel"
          :copied="copiedChannelIndex === channel.index"
          :supports-vision-capability="supportsVisionCapability"
          :allow-reorder="false"
          :show-eval="channelType !== 'images'"
          :supports-ccs="channelType !== 'images'"
          @edit="$emit('edit', channel)"
          @duplicate="duplicateChannel(channel.index)"
          @toggle-vision="toggleVisionCapability(channel)"
          @copy-config="copyChannelConfig(channel)"
          @import-ccs="openCcsImport(channel)"
          @quick-test="handleQuickTest(channel)"
          @eval="openEvalForChannel(channel)"
          @ping="$emit('ping', channel.index)"
          @logs="openLogsDialog(channel)"
          @promotion="openPromotionDialog(channel)"
          @resume="resumeChannel(channel.index)"
          @suspend="setChannelStatus(channel.index, 'suspended')"
          @disable="setChannelStatus(channel.index, 'disabled')"
          @deprecate="setChannelStatus(channel.index, 'deprecated')"
          @delete="$emit('delete', channel.index)"
        />
      </template>
    </ChannelPoolGrid>

    <v-divider class="my-2" />

    <!-- 备用资源池 (disabled only) -->
    <div class="pt-2 pb-3">
      <div class="inactive-pool-header">
        <div class="text-subtitle-2 text-medium-emphasis d-flex align-center">
          <v-icon size="small" class="mr-1" color="grey">mdi-archive-outline</v-icon>
          备用资源池
          <v-chip size="x-small" class="ml-2">{{ inactiveChannels.length }}</v-chip>
        </div>
        <span class="section-inline-label">待启用</span>
      </div>

      <div v-if="inactiveChannels.length > 0" class="inactive-pool">
        <div v-for="channel in inactiveChannels" :key="channel.index" class="inactive-channel-row">
          <!-- 渠道信息 -->
          <div class="channel-info">
            <div class="channel-info-main">
              <span
                class="font-weight-medium channel-name-link"
                tabindex="0"
                role="button"
                @click="$emit('edit', channel)"
                @keydown.enter="$emit('edit', channel)"
                @keydown.space.prevent="$emit('edit', channel)"
              >{{ channel.name }}</span>
              <span class="text-caption text-disabled ml-2">{{ channel.serviceType }}</span>
              <v-chip v-if="channel.temporary" size="x-small" color="warning" variant="tonal" class="ml-2">
                临时 {{ formatDateTime(channel.temporaryUntil) }}
              </v-chip>
              <v-tooltip
                v-if="shouldShowVisionCapability(channel)"
                text="支持图片理解"
                location="top"
                :open-delay="150"
                :open-on-focus="false"
              >
                <template #activator="{ props: tooltipProps }">
                  <v-chip
                    v-bind="tooltipProps"
                    size="x-small"
                    color="primary"
                    variant="flat"
                    class="ml-2 vision-default-chip"
                  >
                    <v-icon start size="12">mdi-image-search-outline</v-icon>
                    图片
                  </v-chip>
                </template>
              </v-tooltip>
            </div>
            <div class="channel-info-meta">
              <ChannelMappingBlock
                :channel="channel"
                :channel-type="channelType"
                :preview-limit="26"
                @edit="$emit('edit', channel)"
              />
            </div>
            <div v-if="channel.description" class="channel-info-desc text-caption text-disabled">
              {{ channel.description }}
            </div>
          </div>

          <!-- API密钥数量 -->
          <div class="channel-keys">
            <v-chip size="x-small" variant="outlined" color="grey" class="keys-chip" @click="$emit('edit', channel)">
              <v-icon start size="x-small">mdi-key</v-icon>
              {{ channel.apiKeys?.length || 0 }}
            </v-chip>
          </div>

          <!-- 操作按钮 -->
          <div class="channel-actions">
            <v-btn size="small" color="success" variant="tonal" @click="enableChannel(channel.index)">
              <v-icon start size="small">mdi-play-circle</v-icon>
              启用
            </v-btn>

            <v-menu>
              <template #activator="{ props: menuProps }">
                <v-btn icon size="x-small" variant="text" v-bind="menuProps">
                  <v-icon size="small">mdi-dots-vertical</v-icon>
                </v-btn>
              </template>
              <v-list density="compact">
                <v-list-item @click="$emit('edit', channel)">
                  <template #prepend>
                    <v-icon size="small">mdi-pencil</v-icon>
                  </template>
                  <v-list-item-title>编辑</v-list-item-title>
                </v-list-item>
                <v-list-item @click="duplicateChannel(channel.index)">
                  <template #prepend>
                    <v-icon size="small">mdi-content-copy</v-icon>
                  </template>
                  <v-list-item-title>复制渠道</v-list-item-title>
                </v-list-item>
                <v-list-item v-if="supportsVisionCapability" @click="toggleVisionCapability(channel)">
                  <template #prepend>
                    <v-icon size="small" :color="channel.visionCapable ? 'success' : 'primary'">
                      {{ channel.visionCapable ? 'mdi-check-circle' : 'mdi-image-search-outline' }}
                    </v-icon>
                  </template>
                  <v-list-item-title>
                    {{ channel.visionCapable ? '取消支持图片理解' : '设为支持图片理解' }}
                  </v-list-item-title>
                </v-list-item>
                <v-divider />
                <v-list-item @click="enableChannel(channel.index)">
                  <template #prepend>
                    <v-icon size="small" color="success">mdi-play-circle</v-icon>
                  </template>
                  <v-list-item-title>启用</v-list-item-title>
                </v-list-item>
                <v-list-item @click="setChannelStatus(channel.index, 'deprecated')">
                  <template #prepend>
                    <v-icon size="small" color="grey">mdi-archive-clock-outline</v-icon>
                  </template>
                  <v-list-item-title>移至弃用池</v-list-item-title>
                </v-list-item>
                <v-list-item @click="$emit('delete', channel.index)">
                  <template #prepend>
                    <v-icon size="small" color="error">mdi-delete</v-icon>
                  </template>
                  <v-list-item-title>删除</v-list-item-title>
                </v-list-item>
              </v-list>
            </v-menu>
          </div>
        </div>
      </div>

      <div v-else class="text-center py-4 text-medium-emphasis text-caption">所有渠道都处于活跃状态</div>
    </div>

    <v-divider class="my-2" />

    <!-- 弃用渠道池 (deprecated only) -->
    <div class="pt-2 pb-3">
      <div class="inactive-pool-header">
        <div class="text-subtitle-2 text-medium-emphasis d-flex align-center">
          <v-icon size="small" class="mr-1" color="grey">mdi-archive-clock-outline</v-icon>
          弃用渠道池
          <v-chip size="x-small" class="ml-2">{{ deprecatedChannels.length }}</v-chip>
        </div>
        <span class="section-inline-label">待清理</span>
      </div>

      <div v-if="deprecatedChannels.length > 0" class="inactive-pool deprecated-pool">
        <div v-for="channel in deprecatedChannels" :key="channel.index" class="inactive-channel-row">
          <div class="channel-info">
            <div class="channel-info-main">
              <span
                class="font-weight-medium channel-name-link"
                tabindex="0"
                role="button"
                @click="$emit('edit', channel)"
                @keydown.enter="$emit('edit', channel)"
                @keydown.space.prevent="$emit('edit', channel)"
              >{{ channel.name }}</span>
              <span class="text-caption text-disabled ml-2">{{ channel.serviceType }}</span>
            </div>
            <div class="channel-info-meta">
              <ChannelMappingBlock
                :channel="channel"
                :channel-type="channelType"
                :preview-limit="26"
                @edit="$emit('edit', channel)"
              />
            </div>
            <div class="channel-info-desc text-caption text-disabled">
              弃用时间：{{ formatDateTime(channel.deprecatedAt) }}
            </div>
          </div>

          <div class="channel-keys">
            <v-chip size="x-small" variant="outlined" color="grey" class="keys-chip" @click="$emit('edit', channel)">
              <v-icon start size="x-small">mdi-key</v-icon>
              {{ channel.apiKeys?.length || 0 }}
            </v-chip>
          </div>

          <div class="channel-actions">
            <v-btn size="small" color="grey" variant="tonal" @click="setChannelStatus(channel.index, 'disabled')">
              <v-icon start size="small">mdi-archive-outline</v-icon>
              转备用
            </v-btn>
            <v-btn size="small" color="error" variant="text" @click="$emit('delete', channel.index)">
              <v-icon start size="small">mdi-delete</v-icon>
              删除
            </v-btn>
          </div>
        </div>
      </div>

      <div v-else class="text-center py-4 text-medium-emphasis text-caption">暂无弃用渠道</div>
    </div>
  </v-card>

    <v-dialog v-model="showPromotionDialog" max-width="420">
      <v-card>
        <v-card-title class="d-flex align-center">
          <v-icon color="info" class="mr-2">mdi-rocket-launch</v-icon>
          设置促销期
          <span v-if="promotionChannel" class="ml-2 text-subtitle-1 text-medium-emphasis"> — {{ promotionChannel.name }}</span>
        </v-card-title>
        <v-divider />
        <v-card-text class="pt-4">
          <p class="text-body-2 text-medium-emphasis mb-4">
            促销期渠道即"抢优先级"：只在 Fuzzy 模式打开时生效，处于促销期的渠道按各自位次整体排到普通渠道之前，可同时设置多个。
            Fuzzy 模式关闭时该设置不参与调度（固定走第一位渠道）。可同时设置时间和次数限制，任一条件满足即自动结束。
          </p>
          <v-text-field
            v-model.number="promotionDuration"
            label="促销时长（分钟）"
            type="number"
            :min="0"
            hint="0 = 不限时长"
            persistent-hint
            density="compact"
            class="mb-3"
          />
          <v-text-field
            v-model.number="promotionCount"
            label="促销请求次数"
            type="number"
            :min="0"
            hint="0 = 不限次数，每次成功请求自动减1"
            persistent-hint
            density="compact"
          />
        </v-card-text>
        <v-divider />
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="closePromotionDialog">取消</v-btn>
          <v-btn color="info" variant="flat" @click="confirmPromotion">确认设置</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="showLogsDialog" max-width="1280">
      <v-card class="channel-logs-dialog-card">
        <v-card-title class="channel-logs-title">
          <div class="channel-logs-title-main">
            <v-icon size="20" color="primary">mdi-text-box-search-outline</v-icon>
            <div class="channel-logs-title-text">
              <span>{{ logsChannel?.name || '渠道' }} 请求日志</span>
              <span class="channel-logs-subtitle">{{ channelLogs.length }} 条记录</span>
            </div>
          </div>
          <v-btn icon variant="text" size="small" @click="closeLogsDialog">
            <v-icon>mdi-close</v-icon>
          </v-btn>
        </v-card-title>
        <v-divider />
        <v-card-text class="channel-logs-dialog-body">
          <div v-if="isLoadingLogs" class="d-flex align-center justify-center py-8">
            <v-progress-circular indeterminate color="primary" />
          </div>
          <v-alert v-else-if="logsError" type="error" variant="tonal" density="compact">
            {{ logsError }}
          </v-alert>
          <div v-else>
            <div v-if="channelLogs.length > 0" class="log-trend-panel">
              <div class="log-trend-header">
                <div class="text-subtitle-2 font-weight-bold">调用用量趋势</div>
                <div class="text-caption text-medium-emphasis">
                  按分钟聚合，展示请求数、Token I/O 和缓存 R/W
                </div>
              </div>
              <apexchart
                type="line"
                height="220"
                :options="logTrendChartOptions"
                :series="logTrendSeries"
              />
            </div>

            <div class="channel-logs-table-shell">
              <v-table density="compact" class="channel-logs-table">
                <colgroup>
                  <col class="log-col-time" />
                  <col class="log-col-status" />
                  <col class="log-col-model" />
                  <col class="log-col-token" />
                  <col class="log-col-cache" />
                  <col class="log-col-upstream" />
                  <col class="log-col-key" />
                  <col class="log-col-duration" />
                  <col class="log-col-error" />
                </colgroup>
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>状态</th>
                    <th>模型</th>
                    <th>Token I/O</th>
                    <th>缓存 R/W</th>
                    <th>上游</th>
                    <th>Key</th>
                    <th>耗时</th>
                    <th>失败明细</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-if="channelLogs.length === 0">
                    <td colspan="9" class="text-center text-medium-emphasis py-6">暂无请求日志</td>
                  </tr>
                  <tr v-for="log in channelLogs" :key="log.attemptId">
                    <td class="log-time-cell">{{ formatLogTime(log.timestamp) }}</td>
                    <td>
                      <v-chip size="x-small" :color="getLogStatusColor(log.status)" variant="tonal">
                        {{ formatLogStatus(log.status, log.statusCode) }}
                      </v-chip>
                    </td>
                    <td>
                      <div class="log-model" :title="log.model || '--'">{{ log.model || '--' }}</div>
                    </td>
                    <td class="log-number-cell">
                      {{ formatLogTokenPair(log.inputTokens, log.outputTokens) }}
                    </td>
                    <td class="log-number-cell">
                      <div>{{ formatLogTokenPair(log.cacheCreationTokens, log.cacheReadTokens) }}</div>
                      <div v-if="(log.cacheCreation5mTokens || 0) + (log.cacheCreation1hTokens || 0) > 0" class="log-cell-note">
                        5m {{ formatNumber(log.cacheCreation5mTokens) }} / 1h {{ formatNumber(log.cacheCreation1hTokens) }}
                      </div>
                    </td>
                    <td>
                      <div class="log-base-url" :title="log.baseUrl">{{ formatLogBaseUrl(log.baseUrl) }}</div>
                    </td>
                    <td class="log-key-cell">{{ log.keyMask }}</td>
                    <td class="log-duration-cell">{{ formatDurationMs(log.durationMs) }}</td>
                    <td>
                      <div v-if="log.errorType || log.errorMessage" class="log-error" :title="formatLogError(log)">
                        <div class="log-error-type">{{ log.errorType || 'error' }}</div>
                        <div class="log-error-message">{{ log.errorMessage || '--' }}</div>
                      </div>
                      <span v-else class="text-medium-emphasis">--</span>
                    </td>
                  </tr>
                </tbody>
              </v-table>
            </div>
          </div>
        </v-card-text>
      </v-card>
    </v-dialog>

    <!-- 快捷测试弹窗 -->
    <QuickTestModal
      v-model="showQuickTestModal"
      :channel="quickTestChannel"
      :api-type="channelType"
    />

    <!-- CC Switch 导入弹窗 -->
    <CcsImportModal
      v-model="showCcsModal"
      :channel="ccsModalChannel"
      :channel-type="channelType"
      :ccs-configured="ccsConfigured"
      @imported="name => emit('success', `已调用 CC Switch 导入 ${name}，请在其确认框中确认`)"
      @copied="emit('success', 'CCS 链接已复制，粘贴到浏览器地址栏回车即可唤起 CC Switch；也可在 设置 → 集成 配置路径实现一键导入')"
      @error="message => emit('error', message)"
    />

</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import VueApexCharts from 'vue3-apexcharts'
import type { ApexOptions } from 'apexcharts'
import { api, channelApiByType, type Channel, type ChannelMetrics, type ChannelStatus, type TimeWindowStats, type ChannelRecentActivity, type ChannelLogEntry, type EvalChannelLatest } from '../services/api'
import { evalAggregateColor, evalAgoLabel, evalFormatTime } from '../utils/eval'
import { useChannelActivity } from '../composables/useChannelActivity'
import {
  getCacheRateLevel,
  getRateLevel,
  getFailureRate,
  getCacheHitRate,
  shouldShowCacheHitRate,
  getLatencyColor,
  isLatencyValid,
  isInPromotion,
  formatPromotionRemaining,
  formatStats,
  formatTokens,
  formatCacheStats,
  getWebsiteUrl
} from '../utils/channelMetricsFormat'
import CcsImportModal from './CcsImportModal.vue'
import ChannelStatusBadge from './ChannelStatusBadge.vue'
import ChannelPoolGrid from './ChannelPoolGrid.vue'
import ChannelQuickMenu from './ChannelQuickMenu.vue'
import ChannelMappingBlock from './ChannelMappingBlock.vue'
import KeyTrendChart from './KeyTrendChart.vue'
import QuickTestModal from './QuickTestModal.vue'

const apexchart = VueApexCharts
const router = useRouter()

const props = defineProps<{
  channels: Channel[]
  currentChannelIndex: number
  channelType: 'messages' | 'responses' | 'gemini' | 'chat' | 'images'
  // 可选：从父组件传入的 metrics 和 stats（使用 dashboard 接口时）
  dashboardMetrics?: ChannelMetrics[]
  dashboardStats?: {
    multiChannelMode: boolean
    activeChannelCount: number
    traceAffinityCount: number
    traceAffinityTTL: string
    failureThreshold: number
    windowSize: number
    circuitRecoveryTime?: string
  }
  // 可选：从父组件传入的实时活跃度数据
  dashboardRecentActivity?: ChannelRecentActivity[]
}>()

const emit = defineEmits<{
  (_e: 'edit', _channel: Channel): void
  (_e: 'delete', _channelId: number): void
  (_e: 'ping', _channelId: number): void
  (_e: 'refresh'): void
  (_e: 'error', _message: string): void
  (_e: 'success', _message: string): void
}>()

const supportsVisionCapability = computed(() => true)

// 快捷测试弹窗状态
const showQuickTestModal = ref(false)
const quickTestChannel = ref<Channel | null>(null)

// 状态
const metrics = ref<ChannelMetrics[]>([])
const recentActivity = ref<ChannelRecentActivity[]>([])
const schedulerStats = ref<{
  multiChannelMode: boolean
  activeChannelCount: number
  traceAffinityCount: number
  traceAffinityTTL: string
  failureThreshold: number
  windowSize: number
} | null>(null)
const isLoadingMetrics = ref(false)

const showLogsDialog = ref(false)
const logsChannel = ref<Channel | null>(null)
const channelLogs = ref<ChannelLogEntry[]>([])
const isLoadingLogs = ref(false)
const logsError = ref('')

type LogTrendBucket = {
  x: number
  requests: number
  inputTokens: number
  outputTokens: number
  cacheCreationTokens: number
  cacheReadTokens: number
}

const logTrendBuckets = computed<LogTrendBucket[]>(() => {
  const buckets = new Map<number, LogTrendBucket>()
  for (const log of channelLogs.value) {
    const timestamp = new Date(log.timestamp).getTime()
    if (!Number.isFinite(timestamp)) continue
    const bucketTime = Math.floor(timestamp / 60000) * 60000
    const bucket = buckets.get(bucketTime) ?? {
      x: bucketTime,
      requests: 0,
      inputTokens: 0,
      outputTokens: 0,
      cacheCreationTokens: 0,
      cacheReadTokens: 0
    }

    bucket.requests += 1
    bucket.inputTokens += log.inputTokens ?? 0
    bucket.outputTokens += log.outputTokens ?? 0
    bucket.cacheCreationTokens += log.cacheCreationTokens ?? 0
    bucket.cacheReadTokens += log.cacheReadTokens ?? 0
    buckets.set(bucketTime, bucket)
  }

  return Array.from(buckets.values()).sort((a, b) => a.x - b.x)
})

const logTrendSeries = computed(() => {
  const points = logTrendBuckets.value
  return [
    { name: '输入Token', data: points.map(point => [point.x, point.inputTokens]) },
    { name: '输出Token', data: points.map(point => [point.x, point.outputTokens]) },
    { name: '缓存创建Token', data: points.map(point => [point.x, point.cacheCreationTokens]) },
    { name: '缓存读取Token', data: points.map(point => [point.x, point.cacheReadTokens]) },
    { name: '请求数', data: points.map(point => [point.x, point.requests]) }
  ]
})

const logTrendChartOptions = computed<ApexOptions>(() => ({
  chart: {
    toolbar: { show: false },
    zoom: { enabled: false },
    animations: { enabled: false },
    fontFamily: 'inherit'
  },
  colors: ['#5C6BC8', '#2FA478', '#C1804A', '#8A72C0', '#7B8494'],
  dataLabels: { enabled: false },
  stroke: {
    width: [2, 2, 2, 2, 2],
    curve: 'smooth'
  },
  markers: {
    size: 3,
    strokeWidth: 0
  },
  grid: {
    borderColor: 'rgba(128, 134, 148, 0.24)',
    strokeDashArray: 4
  },
  xaxis: {
    type: 'datetime',
    labels: {
      datetimeUTC: false
    }
  },
  yaxis: [
    {
      seriesName: ['输入Token', '输出Token', '缓存创建Token', '缓存读取Token'],
      title: { text: 'Tokens' },
      labels: { formatter: value => formatCompactNumber(value) }
    },
    {
      seriesName: '请求数',
      opposite: true,
      title: { text: '请求数' },
      labels: { formatter: value => formatCompactNumber(value) }
    }
  ],
  tooltip: {
    shared: true,
    x: { format: 'HH:mm:ss' },
    y: { formatter: value => formatNumber(value) }
  },
  legend: {
    position: 'bottom',
    fontSize: '12px'
  }
}))

// 促销弹窗相关
const showPromotionDialog = ref(false)
const promotionChannel = ref<Channel | null>(null)
const promotionDuration = ref(5) // 默认 5 分钟
const promotionCount = ref(0) // 默认 0 = 不限制次数

// 用于触发响应式更新的时间戳
const currentTime = ref(Date.now())
let latencyCheckTimer: ReturnType<typeof setInterval> | null = null

// 用于触发活跃度视图更新的时间戳（每 2 秒更新）
const activityUpdateTick = ref(0)

// 活跃度可视化收敛到 useChannelActivity composable（R5，方案 8.1）：
// 数据本体（recentActivity）本组件持有，柱状/曲线派生计算经 composable。
const { getActivityBars, getActivityPath, formatRPM, formatTPM, hasActivityData, getChannelActivity } =
  useChannelActivity(recentActivity, activityUpdateTick)
let activityUpdateTimer: ReturnType<typeof setInterval> | null = null

// 图表展开状态
const expandedChannelIndex = ref<number | null>(null)

// 切换渠道图表展开/收起
const toggleChannelChart = (channelIndex: number) => {
  expandedChannelIndex.value = expandedChannelIndex.value === channelIndex ? null : channelIndex
}

const handleQuickTest = (channel: Channel) => {
  quickTestChannel.value = channel
  showQuickTestModal.value = true
}

const evalLatestMap = ref<Record<string, EvalChannelLatest>>({})
let evalLatestTimer: ReturnType<typeof setInterval> | null = null

/** 渠道行评测块要显示的内容。 */
type EvalRowBlock = {
  label: string
  color: string
  watching: boolean
  ago: string
  tooltip: string
}

/**
 * 按渠道 UUID 预先算好评测块，模板里同一渠道要读四五次（图标、配色、文案、时间），
 * 每次都重算一遍聚合与时间格式化没有意义。
 */
const evalBlockMap = computed<Record<string, EvalRowBlock>>(() => {
  if (props.channelType === 'images') return {}
  const blocks: Record<string, EvalRowBlock> = {}
  for (const [channelId, latest] of Object.entries(evalLatestMap.value)) {
    if (!latest) continue
    blocks[channelId] = {
      label: latest.label,
      color: evalAggregateColor(latest.aggregate),
      watching: latest.watching,
      ago: evalAgoLabel(latest.finishedAt),
      tooltip: [
        latest.suiteName || '未知套件',
        evalFormatTime(latest.finishedAt),
        latest.watching ? '值班中' : '未值班'
      ].join(' · ')
    }
  }
  return blocks
})

const evalChipFor = (channel: Channel): EvalRowBlock | null => {
  if (channel.status === 'disabled' || channel.status === 'deprecated') return null
  if (!channel.id) return null
  return evalBlockMap.value[channel.id] ?? null
}

const openEvalForChannel = (channel: Channel) => {
  if (!channel.id) return
  void router.push({ path: '/eval', query: { channel: channel.id } })
}

const refreshEvalLatestMap = async () => {
  if (props.channelType === 'images') return
  try {
    const response = await api.getEvalLatestMap()
    evalLatestMap.value = response.channels || {}
  } catch {
    // 渠道页芯片失败不打断编排；评测页会显示完整错误
  }
}

// 复制渠道配置到剪贴板
const copiedChannelIndex = ref<number | null>(null)
const copyChannelConfig = async (channel: Channel) => {
  try {
    // 获取第一个密钥
    const firstKey = channel.apiKeys.length > 0 ? channel.apiKeys[0] : ''
    
    // 构建配置文本
    const configText = `名称: ${channel.name}
URL: ${channel.baseUrl}
密钥: ${firstKey}`
    
    // 复制到剪贴板
    await navigator.clipboard.writeText(configText)
    
    // 显示已复制状态
    copiedChannelIndex.value = channel.index
    setTimeout(() => {
      copiedChannelIndex.value = null
    }, 2000)
  } catch (error) {
    console.error('复制配置失败:', error)
  }
}

// ============== CC Switch 导入 ==============

// CC Switch 路径（settings.integration），决定弹窗按钮是"导入"还是"复制链接"
const ccsConfigured = ref(false)
const showCcsModal = ref(false)
const ccsModalChannel = ref<Channel | null>(null)

onMounted(async () => {
  try {
    const settings = await api.getSettings()
    ccsConfigured.value = Boolean(settings.integration?.ccSwitchPath?.trim())
  } catch {
    // 取不到设置就按未配置处理：复制链接兜底
    ccsConfigured.value = false
  }
})

// 打开导入弹窗（目标 app 在弹窗中选择，按渠道池预选）
const openCcsImport = (channel: Channel) => {
  ccsModalChannel.value = channel
  showCcsModal.value = true
}

// 计算属性：非活跃渠道 - 仅 disabled 状态
const inactiveChannels = computed(() => {
	return props.channels.filter(ch => !ch.excludeFromConversation && ch.status === 'disabled')
})

const deprecatedChannels = computed(() => {
	return props.channels.filter(ch => !ch.excludeFromConversation && ch.status === 'deprecated')
})

// 计算属性：是否为多渠道模式
// 多渠道模式判断逻辑：
// 1. 只有一个启用的渠道 → 单渠道模式
// 2. 有一个 active + 几个 suspended → 单渠道模式
// 3. 有多个 active 渠道 → 多渠道模式
const isMultiChannelMode = computed(() => {
	const activeCount = props.channels.filter(
		ch => !ch.excludeFromConversation && (ch.status === 'active' || ch.status === undefined || ch.status === '')
	).length
  return activeCount > 1
})

// 监听 dashboard props 变化（从父组件传入的合并数据）
watch(() => props.dashboardMetrics, (newMetrics) => {
  if (newMetrics) {
    metrics.value = newMetrics
  }
}, { immediate: true })

watch(() => props.dashboardStats, (newStats) => {
  if (newStats) {
    schedulerStats.value = newStats
  }
}, { immediate: true })

// 监听 recentActivity props 变化
watch(() => props.dashboardRecentActivity, (newActivity) => {
  recentActivity.value = newActivity ?? []
}, { immediate: true })

// 监听 channelType 变化 - 切换时刷新指标并收起图表
watch(() => props.channelType, () => {
  expandedChannelIndex.value = null // 收起展开的图表
  // 如果没有使用 dashboard props，则自己刷新
  if (!props.dashboardMetrics) {
    refreshMetrics()
  }
})

// 获取渠道指标
const getChannelMetrics = (channelIndex: number): ChannelMetrics | undefined => {
  return metrics.value.find(m => m.channelIndex === channelIndex)
}

// 获取分时段统计的辅助方法
const get15mStats = (channelIndex: number) => {
  return getChannelMetrics(channelIndex)?.timeWindows?.['15m']
}

const get1hStats = (channelIndex: number) => {
  return getChannelMetrics(channelIndex)?.timeWindows?.['1h']
}

const get6hStats = (channelIndex: number) => {
  return getChannelMetrics(channelIndex)?.timeWindows?.['6h']
}

const get24hStats = (channelIndex: number) => {
  return getChannelMetrics(channelIndex)?.timeWindows?.['24h']
}

// 指标条档位：驱动 CSS 里的配色与动效强度（成功率与缓存各一套阈值）
const shouldShowVisionCapability = (channel: Channel): boolean => {
  return supportsVisionCapability.value && !!channel.visionCapable
}

const toggleVisionCapability = async (channel: Channel) => {
  const nextValue = !channel.visionCapable

  try {
    await channelApiByType(props.channelType).updateChannel(channel.index, { visionCapable: nextValue })

    emit('refresh')
    emit('success', nextValue
      ? `已将 ${channel.name} 设为支持图片理解`
      : `已取消 ${channel.name} 的图片理解支持`)
  } catch (error) {
    console.error('Failed to update vision capability:', error)
    const errorMessage = error instanceof Error ? error.message : '未知错误'
    emit('error', `设置图片理解能力失败: ${errorMessage}`)
  }
}

// 刷新指标
const refreshMetrics = async () => {
  isLoadingMetrics.value = true
  try {
    const [metricsData, statsData] = await Promise.all([
      channelApiByType(props.channelType).getChannelMetrics(),
      api.getSchedulerStats(props.channelType)
    ])
    metrics.value = metricsData
    schedulerStats.value = statsData
  } catch (error) {
    console.error('Failed to load metrics:', error)
  } finally {
    isLoadingMetrics.value = false
  }
}

const duplicateChannel = async (channelIndex: number) => {
  try {
    await api.duplicateChannel(props.channelType, channelIndex)
    emit('refresh')
  } catch (error) {
    console.error('Failed to duplicate channel:', error)
    const errorMessage = error instanceof Error ? error.message : '未知错误'
    emit('error', `复制渠道失败: ${errorMessage}`)
  }
}

// 设置渠道状态
const setChannelStatus = async (channelId: number, status: ChannelStatus) => {
  try {
    await channelApiByType(props.channelType).setStatus(channelId, status)
    emit('refresh')
  } catch (error) {
    console.error('Failed to set channel status:', error)
    const errorMessage = error instanceof Error ? error.message : '未知错误'
    emit('error', `设置渠道状态失败: ${errorMessage}`)
  }
}

// 启用渠道（从备用池移到活跃序列）
const enableChannel = async (channelId: number) => {
  await setChannelStatus(channelId, 'active')
}

// 恢复渠道（重置指标并设为 active）
const resumeChannel = async (channelId: number) => {
  try {
    await channelApiByType(props.channelType).resumeChannel(channelId)
    await setChannelStatus(channelId, 'active')
  } catch (error) {
    console.error('Failed to resume channel:', error)
  }
}

const toggleChannelPrimaryStatus = async (channel: Channel) => {
  const currentStatus = channel.status || 'active'

  if (currentStatus === 'suspended') {
    await resumeChannel(channel.index)
    return
  }

  if (currentStatus === 'active') {
    await setChannelStatus(channel.index, 'suspended')
  }
}

const openPromotionDialog = (channel: Channel) => {
  promotionChannel.value = channel
  promotionDuration.value = 5
  promotionCount.value = 0
  showPromotionDialog.value = true
}

const closePromotionDialog = () => {
  showPromotionDialog.value = false
  promotionChannel.value = null
}

const openLogsDialog = async (channel: Channel) => {
  logsChannel.value = channel
  showLogsDialog.value = true
  logsError.value = ''
  channelLogs.value = []
  isLoadingLogs.value = true
  try {
    const response = await api.getChannelLogs(props.channelType, channel.index)
    channelLogs.value = response.logs || []
  } catch (error) {
    console.error('Failed to load channel logs:', error)
    logsError.value = error instanceof Error ? error.message : '加载请求日志失败'
  } finally {
    isLoadingLogs.value = false
  }
}

const closeLogsDialog = () => {
  showLogsDialog.value = false
  logsChannel.value = null
  channelLogs.value = []
  logsError.value = ''
}

const formatLogTime = (value: string): string => {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

const formatDateTime = (value?: string): string => {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

const getLogStatusColor = (status: string): string => {
  if (status === 'completed') return 'success'
  if (status === 'cancelled') return 'grey'
  return 'error'
}

const formatLogStatus = (status: string, statusCode?: number): string => {
  const labels: Record<string, string> = {
    completed: '成功',
    failed: '失败',
    cancelled: '取消'
  }
  const label = labels[status] || status || '--'
  return statusCode ? `${label} ${statusCode}` : label
}

const formatLogBaseUrl = (value?: string): string => {
  const raw = String(value || '').trim()
  if (!raw) return '--'
  try {
    const url = new URL(raw)
    const path = url.pathname && url.pathname !== '/' ? url.pathname : ''
    return `${url.host}${path}`
  } catch {
    return raw
  }
}

const formatDurationMs = (value?: number): string => {
  const numeric = Number(value ?? 0)
  if (!Number.isFinite(numeric) || numeric <= 0) return '0ms'
  if (numeric >= 1000) return `${(numeric / 1000).toFixed(numeric >= 10000 ? 0 : 1)}s`
  return `${Math.round(numeric)}ms`
}

const formatLogError = (log: ChannelLogEntry): string => {
  return [log.errorType, log.errorMessage].filter(Boolean).join('\n') || '--'
}

const formatNumber = (value?: number): string => {
  const numeric = Number(value ?? 0)
  if (!Number.isFinite(numeric)) return '0'
  return new Intl.NumberFormat().format(Math.round(numeric))
}

const formatCompactNumber = (value?: number): string => {
  const numeric = Number(value ?? 0)
  if (!Number.isFinite(numeric)) return '0'
  return new Intl.NumberFormat(undefined, {
    notation: 'compact',
    maximumFractionDigits: 1
  }).format(numeric)
}

const formatLogTokenPair = (left?: number, right?: number): string => {
  return `${formatNumber(left)} / ${formatNumber(right)}`
}

const normalizePromotionInput = (value: unknown): number => {
  const parsed = typeof value === 'number'
    ? value
    : Number(String(value ?? '').trim())

  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 0
  }

  return Math.floor(parsed)
}

const confirmPromotion = async () => {
  if (!promotionChannel.value) return
  const channel = promotionChannel.value
  const durationMinutes = normalizePromotionInput(promotionDuration.value)
  const count = normalizePromotionInput(promotionCount.value)

  promotionDuration.value = durationMinutes
  promotionCount.value = count

  const durationSeconds = durationMinutes * 60

  try {
    if (channel.status === 'suspended') {
      await channelApiByType(props.channelType).resumeChannel(channel.index)
      await setChannelStatus(channel.index, 'active')
    }

    await channelApiByType(props.channelType).setPromotion(channel.index, durationSeconds, count)
    emit('refresh')
    const durationText = durationMinutes > 0 ? `${durationMinutes}分钟内` : ''
    const countText = count > 0 ? `${count}次请求内` : ''
    const combined = [durationText, countText].filter(Boolean).join(' / ')
    emit('success', `渠道 ${channel.name} 已设为最高优先级${combined ? '（' + combined + '）' : ''}`)
  } catch (error) {
    console.error('Failed to set promotion:', error)
    const errorMessage = error instanceof Error ? error.message : '未知错误'
    emit('error', `设置优先级失败: ${errorMessage}`)
  } finally {
    closePromotionDialog()
  }
}

// 判断渠道是否可以删除
// 规则：当前渠道分组中至少要保留一个 active 状态的渠道
const canDeleteChannel = (channel: Channel): boolean => {
  // 统计当前 active 状态的渠道数量
  const poolID = channel.poolId || 'default'
  const activeCount = props.channels.filter(
    ch => !ch.excludeFromConversation && (ch.poolId || 'default') === poolID &&
      (ch.status === 'active' || ch.status === undefined || ch.status === '')
  ).length

  // 如果要删除的是 active 渠道，且只剩一个 active，则不允许删除
  const isActive = channel.status === 'active' || channel.status === undefined || channel.status === ''
  if (isActive && activeCount <= 1) {
    return false
  }

  return true
}

// 处理删除渠道
const handleDeleteChannel = (channel: Channel) => {
  if (!canDeleteChannel(channel)) {
    emit('error', '无法删除：当前渠道分组中至少需要保留一个活跃渠道')
    return
  }
  emit('delete', channel.index)
}

// 组件挂载时加载指标并启动延迟过期检查定时器
onMounted(() => {
  refreshMetrics()
  void refreshEvalLatestMap()
  evalLatestTimer = setInterval(() => {
    void refreshEvalLatestMap()
  }, 30000)
  // 每 30 秒更新一次 currentTime，触发延迟显示的响应式更新
  latencyCheckTimer = setInterval(() => {
    currentTime.value = Date.now()
  }, 30000)
  // 每 2 秒更新一次 activityUpdateTick，触发活跃度视图更新
  activityUpdateTimer = setInterval(() => {
    activityUpdateTick.value++
  }, 2000)
})

// 组件卸载时清理定时器
onUnmounted(() => {
  if (latencyCheckTimer) {
    clearInterval(latencyCheckTimer)
    latencyCheckTimer = null
  }
  if (activityUpdateTimer) {
    clearInterval(activityUpdateTimer)
    activityUpdateTimer = null
  }
  if (evalLatestTimer) {
    clearInterval(evalLatestTimer)
    evalLatestTimer = null
  }
})

// 暴露方法给父组件
defineExpose({
  refreshMetrics
})
</script>

<style scoped>
/* ============================================================
   API Proxy — 调度轨道
   编号、状态边线、数据波形组成统一的设备语言
   ============================================================ */

.channel-orchestration { overflow: visible; background: transparent; border: none; }
.orchestration-header {
  min-height: 58px;
  margin-bottom: 16px;
  border-bottom: 2px solid rgba(var(--v-theme-outline), 0.55);
  position: relative;
  overflow: visible;
}
/* 标题下的主色标尺 — 带刻度，像面板上的量程条 */
.orchestration-header::after {
  content: '';
  position: absolute;
  left: 0;
  bottom: -2px;
  width: clamp(110px, 18vw, 240px);
  height: 3px;
  background:
    repeating-linear-gradient(90deg,
      rgba(255, 255, 255, 0.55) 0 1px,
      transparent 1px 8px),
    rgb(var(--v-theme-primary));
}
.section-inline-label {
  color: rgba(var(--v-theme-on-surface), 0.48);
  font-size: 11px;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}
.channel-list { display: flex; flex-direction: column; gap: 8px; }
.channel-item-wrapper { display: flex; flex-direction: column; }

.channel-row {
  position: relative;
  padding: 12px 16px;
  background: rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--v-theme-outline), 0.6);
  /* 对角不对称切角 — 全站统一的形状签名 */
  border-radius: var(--cut-md) var(--cut-xs) var(--cut-md) var(--cut-xs);
  box-shadow: var(--shadow-1);
  min-height: 52px;
  transition: transform 0.16s var(--ease-out), box-shadow 0.16s ease, border-color 0.16s ease;
  cursor: pointer;
  overflow: hidden;
}

/* 左侧刻度状态轨 */
.channel-row::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  bottom: 0;
  width: var(--rail);
  background:
    repeating-linear-gradient(180deg,
      rgba(255, 255, 255, 0.5) 0 1px,
      transparent 1px 8px),
    linear-gradient(180deg,
      rgb(var(--v-theme-primary)) 0%,
      rgba(var(--v-theme-primary), 0.42) 50%,
      rgb(var(--v-theme-primary)) 100%);
  background-size: 100% 100%, 100% 200%;
  box-shadow: inset -1px 0 0 rgba(0, 0, 0, 0.14);
  z-index: 2;
}

/* 横向扫描线 — 仅 hover 时扫过，避免整屏几十行同时闪动 */
.channel-row::after {
  content: '';
  position: absolute;
  top: 0;
  bottom: 0;
  left: 0;
  width: 40%;
  background: linear-gradient(90deg, transparent, rgba(var(--v-theme-primary), 0.1), transparent);
  pointer-events: none;
  opacity: 0;
  animation: scan-sweep 2.6s ease-in-out infinite;
  animation-play-state: paused;
  z-index: 3;
}
.channel-row:hover::after { opacity: 1; animation-play-state: running; }
.channel-row:hover::before { animation: rail-flow 3s linear infinite; }

.channel-row-content {
  display: grid;
  grid-template-columns: 28px 28px 90px minmax(140px, 1fr) auto 60px 60px 60px auto;
  align-items: center;
  gap: 8px;
  position: relative;
  z-index: 1;
}

.channel-row:hover {
  border-color: rgba(var(--v-theme-primary), 0.7);
  box-shadow: var(--shadow-2);
  transform: translateX(2px);
}

.channel-row:active { transform: translateY(0); box-shadow: var(--shadow-1); }

/* suspended 状态 — 琥珀底 + 琥珀轨，与活跃行拉开明显反差 */
.channel-row.is-suspended {
  background: color-mix(in srgb, rgb(var(--v-theme-warning)) 9%, rgb(var(--v-theme-surface)));
  border-color: rgba(var(--v-theme-warning), 0.6);
}
.channel-row.is-suspended:hover { border-color: rgba(var(--v-theme-warning), 0.85); }
.channel-row.is-suspended::before {
  background:
    repeating-linear-gradient(180deg,
      rgba(255, 255, 255, 0.5) 0 1px,
      transparent 1px 8px),
    rgb(var(--v-theme-warning));
}

.channel-row.ghost {
  opacity: 0.5;
  background: rgba(var(--v-theme-primary), 0.06);
  border: 2px dashed rgba(var(--v-theme-primary), 0.45);
}

/* SVG 活跃度波形背景 */
.activity-chart-bg {
  position: absolute; top: 0; left: 0; width: 100%; height: 100%;
  pointer-events: none; z-index: 0; opacity: 0.2; overflow: hidden;
  border-radius: inherit;
  transition: opacity 0.2s ease;
}
.channel-row:hover .activity-chart-bg { opacity: 0.34; }

/* 柱子生长 — 从底部竖起（首次渲染播放一次） */
.activity-bar {
  transition: none;
  transform-box: fill-box;
  transform-origin: center bottom;
  animation: bar-rise 0.5s var(--ease-out) both;
}

/* 分档动效：健康档静止，越差抖得越急 —— 余光扫过就能发现故障段 */
.activity-bar-warn {
  animation: bar-rise 0.5s var(--ease-out) both, spark-blink 2.4s ease-in-out infinite;
}
.activity-bar-bad {
  animation: bar-rise 0.5s var(--ease-out) both, spark-blink 1.05s ease-in-out infinite;
}

/* 最新柱保持高亮，标记当前窗口 */
.activity-bar-latest {
  filter: brightness(1.45) saturate(1.2);
  animation: bar-rise 0.5s var(--ease-out) both;
}

.channel-chart-wrapper { margin: 4px 0 8px 0; }

.drag-handle {
  cursor: grab; display: flex; align-items: center; justify-content: center;
  width: 24px; height: 24px; border-radius: 5px;
  transition: all 0.1s ease;
}
.drag-handle:hover { background: rgba(var(--v-theme-on-surface), 0.06); }
.drag-handle:active { cursor: grabbing; background: rgba(var(--v-theme-primary), 0.1); }

.priority-number {
  display: flex; align-items: center; justify-content: center;
  width: 24px; height: 24px;
  background: rgb(var(--v-theme-primary));
  color: rgb(var(--v-theme-on-primary));
  font-size: 11px; font-weight: 800;
  font-family: 'Fira Code', 'JetBrains Mono', monospace;
  border: none;
  /* 与卡片同向的不对称切角 */
  border-radius: 6px 2px 6px 2px;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.25);
}

.channel-name {
  display: flex; align-items: center; overflow: hidden; gap: 4px;
}
.channel-name .expand-icon { flex-shrink: 0; margin-left: auto; }
.channel-name .font-weight-medium { font-size: 0.9rem; flex-shrink: 0; }

.channel-name-link { cursor: pointer; transition: color 0.15s ease; }
.channel-name-link:hover, .channel-name-link:focus { color: rgb(var(--v-theme-primary)); }
.channel-name-link:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; border-radius: 4px; }

.channel-status-toggle {
  display: inline-flex; align-items: center; border-radius: 3px; cursor: pointer;
}
.channel-status-toggle:focus-visible { outline: 2px solid rgb(var(--v-theme-primary)); outline-offset: 2px; }
.channel-status-toggle :deep(.badge-content) { cursor: pointer; }

.channel-metrics { display: flex; align-items: center; gap: 6px; flex-wrap: nowrap; white-space: nowrap; min-width: 186px; }

/* ===== 迷你指标条 — 与渠道卡同一套仪表语言的压缩版 ===== */
.metrics-visual { min-width: 176px; }
.mini-metric-bar { display: flex; align-items: center; gap: 7px; margin-bottom: 3px; }
.metric-bar-label {
  width: 27px;
  flex: 0 0 27px;
  color: rgba(var(--v-theme-on-surface-variant), 0.82);
  font-size: 9px;
  font-weight: 700;
  letter-spacing: 0.02em;
}

/* 凹槽轨道 + 等距刻度 */
.mmb-track {
  flex: 1;
  height: 9px;
  min-width: 60px;
  border-radius: 2px;
  position: relative;
  overflow: hidden;
  background:
    repeating-linear-gradient(90deg,
      rgba(var(--v-theme-on-surface), 0.13) 0 1px,
      transparent 1px var(--tick)),
    rgba(var(--v-theme-outline), 0.24);
  box-shadow: inset 0 1px 2px rgba(0, 0, 0, 0.14);
}
/* 90% 健康阈值刻线 */
.mmb-track::after {
  content: '';
  position: absolute;
  left: 90%; top: 0; bottom: 0;
  width: 1px;
  background: rgba(var(--v-theme-on-surface), 0.4);
  z-index: 3;
}
.v-theme--dark .mmb-track {
  background:
    repeating-linear-gradient(90deg,
      rgba(255, 255, 255, 0.09) 0 1px,
      transparent 1px var(--tick)),
    rgba(0, 0, 0, 0.4);
  box-shadow: inset 0 1px 3px rgba(0, 0, 0, 0.55);
}
.v-theme--dark .mmb-track::after { background: rgba(255, 255, 255, 0.3); }

.mmb-fill {
  height: 100%;
  border-radius: 2px;
  transition: width 0.85s var(--ease-spring);
  min-width: 3px;
  position: relative;
  z-index: 1;
}

/* 波头 — 末端示波器亮点 */
.mmb-fill::before {
  content: '';
  position: absolute;
  right: -1px;
  top: 50%;
  transform: translateY(-50%);
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.92);
  box-shadow: 0 0 8px 2px currentColor;
  animation: wave-head 2.6s ease-in-out infinite;
}

/* 流光 — 数据在流动 */
.mmb-fill::after {
  content: '';
  position: absolute;
  top: 0; bottom: 0; left: 0;
  width: 55%;
  background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.36), transparent);
  animation: shimmer-slide 3.2s ease-in-out infinite both;
}

/* 指标刷新扫光 */
.mmb-flash {
  position: absolute;
  inset: 0;
  z-index: 2;
  pointer-events: none;
  background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.5), transparent);
  animation: shimmer-slide 0.62s var(--ease-out) 1 both;
}
.v-theme--dark .mmb-flash { background: linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.3), transparent); }

/* 失败占比 — 从轨道右侧覆盖，不做波头 / 流光动效，避免与成功条抢注意力 */
.mmb-fill-failure {
  position: absolute;
  top: 0; right: 0; bottom: 0;
  border-radius: 2px;
  background: linear-gradient(90deg, rgba(var(--v-theme-error), 0.5), rgb(var(--v-theme-error)));
  transition: width 0.85s var(--ease-spring);
  z-index: 1;
  pointer-events: none;
}

/* 健康（绿）— 平稳 */
.mmb-fill.high {
  background: linear-gradient(90deg, rgba(var(--v-theme-success), 0.72), rgb(var(--v-theme-success)));
  color: rgb(var(--v-theme-success));
  box-shadow: 0 0 8px rgba(var(--v-theme-success), 0.4), inset 0 1px 0 rgba(255, 255, 255, 0.3);
}
/* 亚健康（黄）— 节奏加快 */
.mmb-fill.medium {
  background: linear-gradient(90deg, rgba(var(--v-theme-warning), 0.72), rgb(var(--v-theme-warning)));
  color: rgb(var(--v-theme-warning));
  box-shadow: 0 0 8px rgba(var(--v-theme-warning), 0.42), inset 0 1px 0 rgba(255, 255, 255, 0.28);
}
.mmb-fill.medium::before { animation-duration: 1.8s; }
.mmb-fill.medium::after { animation-duration: 2.2s; }
/* 故障（红）— 滚动警示斜纹 + 急促红光呼吸 */
.mmb-fill.low {
  background:
    repeating-linear-gradient(135deg,
      rgba(0, 0, 0, 0.26) 0 5px,
      transparent 5px 10px),
    linear-gradient(90deg, rgba(var(--v-theme-error), 0.75), rgb(var(--v-theme-error)));
  background-size: 20px 100%, 100% 100%;
  color: rgb(var(--v-theme-error));
  animation: stripe-drift 0.85s linear infinite, alarm-breathe 1.5s ease-in-out infinite;
}
.mmb-fill.low::before { animation-duration: 1.05s; }
.mmb-fill.low::after { display: none; }

.mmb-value {
  font-size: 11px; font-weight: 700;
  min-width: 30px; text-align: right;
  font-family: 'Fira Code', 'JetBrains Mono', monospace;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  animation: value-pop 0.45s var(--ease-spring);
}
.mmb-value.high { color: rgb(var(--v-theme-success)); }
.mmb-value.medium { color: rgb(var(--v-theme-warning)); }
.mmb-value.low { color: rgb(var(--v-theme-error)); }

/* ===== 缓存命中率条 =====
   与成功率条同规格（同高、同轨、同刻度），只换配色与节奏。
   必须写在成功率档位样式之后：档位类名 high/medium/low 是共用的，
   靠更高特异性 + 靠后顺序把红色警报那套急促动效盖掉——缓存命中低只是省得少，不是故障。 */
.mmb-fill.cache-mmb-fill.high {
  background: linear-gradient(90deg, rgba(var(--v-theme-success), 0.6), rgb(var(--v-theme-success)));
  color: rgb(var(--v-theme-success));
  animation: fill-glow-breathe 3.4s ease-in-out infinite;
}
.mmb-fill.cache-mmb-fill.medium {
  background: linear-gradient(90deg, rgba(var(--v-theme-info), 0.6), rgb(var(--v-theme-info)));
  color: rgb(var(--v-theme-info));
  animation: fill-glow-breathe 3.4s ease-in-out infinite;
}
.mmb-fill.cache-mmb-fill.low {
  background: linear-gradient(90deg, rgba(var(--v-theme-warning), 0.6), rgb(var(--v-theme-warning)));
  color: rgb(var(--v-theme-warning));
  animation: fill-glow-breathe 4.2s ease-in-out infinite;
}
/* 波头放慢；流光要显式恢复——成功率的 low 档把它 display:none 掉了 */
.mmb-fill.cache-mmb-fill.high::before,
.mmb-fill.cache-mmb-fill.medium::before,
.mmb-fill.cache-mmb-fill.low::before { animation-duration: 3s; }
.mmb-fill.cache-mmb-fill.high::after,
.mmb-fill.cache-mmb-fill.medium::after,
.mmb-fill.cache-mmb-fill.low::after { display: block; animation-duration: 3.6s; }
.mmb-value.cache-mmb-value.high { color: rgb(var(--v-theme-success)); }
.mmb-value.cache-mmb-value.medium { color: rgb(var(--v-theme-info)); }
.mmb-value.cache-mmb-value.low { color: rgb(var(--v-theme-warning)); }
.mmb-value.cache-mmb-value.unknown { color: rgba(var(--v-theme-on-surface), 0.42); }
.cache-mini-metric-bar.is-unavailable { opacity: 0.72; }

.mini-metric-secondary {
  display: flex; align-items: center; gap: 4px;
  font-size: 10px;
  font-family: 'Fira Code', 'JetBrains Mono', monospace;
  color: rgba(var(--v-theme-on-surface-variant), 0.8);
}
.mm-sep { opacity: 0.35; }
/* 失败次数 — 与成功率条同一窗口的绝对量，红色强调 */
.mm-failures { color: rgb(var(--v-theme-error)); font-weight: 700; }
.channel-latency { display: flex; align-items: center; min-width: 60px; }

/* ===== 副行：映射 / 评测 / 描述 =====
   缩进到与名称列大致对齐，靠一条竖线表明"这是上一行的补充信息" */
.channel-row-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  position: relative;
  z-index: 1;
  margin: 8px 0 0 62px;
  padding-left: 10px;
  border-left: 2px solid rgba(var(--v-theme-outline), 0.5);
}
/* 副行内的块一律不参与收缩：被压扁到只剩边框就等于没显示 */
.channel-row-meta > * { flex: 0 0 auto; max-width: 100%; }

.meta-block--eval {
  cursor: pointer;
  border-color: currentColor;
  background: color-mix(in srgb, currentColor 12%, transparent);
}
.meta-block--eval:hover,
.meta-block--eval:focus-visible {
  background: color-mix(in srgb, currentColor 20%, transparent);
  transform: translateY(-1px);
}
.meta-block--eval.is-success { color: rgb(var(--v-theme-success)); }
.meta-block--eval.is-warning { color: rgb(var(--v-theme-warning)); }
.meta-block--eval.is-error { color: rgb(var(--v-theme-error)); }
.meta-block--eval.is-grey { color: rgba(var(--v-theme-on-surface), 0.62); }
/* 结论文字用正文色，避免整块都是同一种警示色而读不出层次 */
.meta-block--eval .meta-block-value,
.meta-block--eval .meta-block-time { color: rgb(var(--v-theme-on-surface)); }

.meta-desc {
  overflow: hidden;
  min-width: 0;
  color: rgba(var(--v-theme-on-surface), 0.5);
  font-size: 0.78rem;
  white-space: nowrap;
  text-overflow: ellipsis;
}
/* 描述是唯一允许被压缩的项：它可以省略号，映射与评测不行 */
.channel-row-meta > .meta-desc { flex: 1 1 auto; }

.channel-rpm-tpm { display: flex; flex-direction: column; align-items: center; min-width: 60px; }
.rpm-tpm-values {
  display: flex; align-items: baseline; gap: 2px;
  font-size: 13px; font-weight: 700;
  font-family: 'Fira Code', 'JetBrains Mono', monospace;
  font-variant-numeric: tabular-nums;
  color: rgba(var(--v-theme-on-surface), 0.4);
}
.rpm-tpm-values .rpm-value.has-data, .rpm-tpm-values .tpm-value.has-data { color: rgb(var(--v-theme-primary)); }
.rpm-tpm-separator { color: rgba(var(--v-theme-on-surface), 0.2); font-weight: 400; }
.rpm-tpm-labels { display: flex; align-items: center; gap: 2px; font-size: 9px; color: rgba(var(--v-theme-on-surface), 0.4); text-transform: uppercase; letter-spacing: 0.06em; }

.channel-keys { display: flex; align-items: center; }
.channel-keys .keys-chip { cursor: pointer; transition: all 0.15s ease; }
.channel-keys .keys-chip:hover { background: rgba(var(--v-theme-primary), 0.06); border-color: rgba(var(--v-theme-primary), 0.3); color: rgb(var(--v-theme-primary)); }

.channel-actions { display: flex; align-items: center; gap: 2px; justify-content: flex-end; min-width: 50px; }

/* 备用资源池 */
.inactive-pool-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 8px; }
.inactive-pool {
  display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 10px;
  background: rgba(var(--v-theme-surface-variant), 0.45);
  padding: 16px;
  border: 1px dashed rgba(var(--v-theme-outline), 0.65);
  border-radius: var(--cut-md) var(--cut-xs) var(--cut-md) var(--cut-xs);
}
.inactive-channel-row {
  display: flex; align-items: center; justify-content: space-between; gap: 12px;
  padding: 10px 14px;
  background: rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--v-theme-outline), 0.55);
  border-radius: 7px 2px 7px 2px;
  transition: transform 0.15s var(--ease-out), border-color 0.15s ease, box-shadow 0.15s ease;
}
.inactive-channel-row:hover {
  border-color: rgb(var(--v-theme-primary));
  box-shadow: var(--shadow-1);
  transform: translateX(3px);
}
.inactive-channel-row .channel-info { flex: 1; min-width: 0; overflow: hidden; display: flex; flex-direction: column; gap: 3px; }
.inactive-channel-row .channel-info-main { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
/* 池卡片较窄，映射块单独一行，别再跟名称抢宽度 */
.inactive-channel-row .channel-info-meta { display: flex; min-width: 0; }
.inactive-channel-row .channel-info-desc { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; line-height: 1.3; max-width: 100%; font-size: 0.78rem; opacity: 0.5; }
.inactive-channel-row .channel-actions { display: flex; align-items: center; gap: 4px; }

/* tooltip */
.metrics-tooltip { font-size: 12px; line-height: 1.5; color: rgb(var(--v-theme-on-surface)); }
.metrics-tooltip-row { display: flex; justify-content: space-between; gap: 16px; padding: 2px 0; }
.metrics-tooltip-row span:first-child { color: rgba(var(--v-theme-on-surface), 0.55); }
.metrics-tooltip-row span:last-child { font-weight: 500; color: rgb(var(--v-theme-on-surface)); }

/* 日志对话框 */
.channel-logs-dialog-card { max-height: calc(100vh - 48px); display: flex; flex-direction: column; }
.channel-logs-title { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 16px 20px; }
.channel-logs-title-main { min-width: 0; display: flex; align-items: center; gap: 10px; }
.channel-logs-title-text { min-width: 0; display: flex; flex-direction: column; gap: 2px; font-size: 16px; font-weight: 650; line-height: 1.25; }
.channel-logs-subtitle { color: rgba(var(--v-theme-on-surface), 0.45); font-size: 12px; font-weight: 500; }
.channel-logs-dialog-body { overflow: auto; padding: 16px 20px 20px; }

.log-trend-panel {
  border: 1px solid rgba(var(--v-theme-outline), 0.3); border-radius: 4px;
  padding: 14px 16px 8px; margin-bottom: 16px;
  background: rgba(var(--v-theme-surface-variant), 0.15);
}
.log-trend-header { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 6px; }

.channel-logs-table-shell {
  max-height: min(46vh, 520px); overflow: auto;
  border: 1px solid rgba(var(--v-theme-outline), 0.3); border-radius: 4px;
  background: rgb(var(--v-theme-surface));
}
.channel-logs-table { min-width: 1160px; }
.channel-logs-table :deep(table) { table-layout: fixed; width: 100%; }
.log-col-time { width: 172px; }
.log-col-status { width: 108px; }
.log-col-model { width: 150px; }
.log-col-token { width: 116px; }
.log-col-cache { width: 142px; }
.log-col-upstream { width: 210px; }
.log-col-key { width: 142px; }
.log-col-duration { width: 90px; }
.log-col-error { width: 230px; }

.channel-logs-table th, .channel-logs-table td { vertical-align: middle; white-space: nowrap; }
.channel-logs-table th {
  position: sticky; top: 0; z-index: 1;
  background: rgb(var(--v-theme-surface));
  color: rgba(var(--v-theme-on-surface), 0.5);
  font-size: 12px; font-weight: 600;
  border-bottom: 1px solid rgba(var(--v-theme-outline), 0.1);
}
.channel-logs-table td {
  height: 48px; color: rgba(var(--v-theme-on-surface), 0.8);
  font-size: 13px; border-bottom: 1px solid rgba(var(--v-theme-outline), 0.06);
}
.log-time-cell, .log-key-cell, .log-duration-cell, .log-number-cell, .log-base-url { font-variant-numeric: tabular-nums; }
.log-key-cell, .log-duration-cell, .log-number-cell, .log-base-url, .log-model { font-family: 'Fira Code', 'JetBrains Mono', 'SF Mono', monospace; }
.log-model, .log-base-url { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.log-error { min-width: 0; }
.log-error-type { overflow: hidden; color: rgb(var(--v-theme-error)); font-size: 12px; font-weight: 700; text-overflow: ellipsis; white-space: nowrap; }
.log-error-message { display: -webkit-box; overflow: hidden; color: rgba(var(--v-theme-on-surface), 0.5); font-size: 12px; line-height: 1.35; white-space: normal; overflow-wrap: break-word; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.log-cell-note { color: rgba(var(--v-theme-on-surface), 0.45); font-size: 11px; line-height: 1.3; }

/* 响应式 */
@media (max-width: 1400px) {
  .channel-row-content { grid-template-columns: 28px 28px 85px minmax(100px, 1fr) auto 50px 50px 50px auto; gap: 6px; }
  .channel-row { padding: 10px 12px; }
  .channel-row-meta { margin-left: 56px; }
}
@media (max-width: 1200px) {
  .channel-row-content { grid-template-columns: 26px 26px 80px minmax(80px, 1fr) auto 45px 45px 45px auto; gap: 5px; }
  .channel-row { padding: 8px 10px; }
  .rpm-tpm-values { font-size: 11px; }
  .rpm-tpm-labels { font-size: 8px; }
}
@media (max-width: 960px) {
  .channel-row-content { grid-template-columns: 26px 26px 75px minmax(60px, 1fr) auto 40px 40px 40px auto; gap: 4px; }
  .channel-row { padding: 8px 8px; }
  .channel-row-meta { margin-left: 44px; }
}
@media (max-width: 600px) {
  .channel-row-content { grid-template-columns: 28px 1fr 60px; gap: 8px; }
  .channel-row { padding: 10px 12px; }
  .channel-metrics, .channel-latency, .channel-keys, .channel-rpm-tpm { display: none; }
  .priority-number, .drag-handle { display: none; }
  /* 小屏没有指标列了，副行不再缩进，靠竖线区分层级 */
  .channel-row-meta { margin-left: 0; }
  .channel-row-meta > .meta-desc { display: none; }
}

@media (prefers-reduced-motion: reduce) {
  .channel-row,
  .channel-row::before,
  .channel-row::after,
  .drag-handle,
  .activity-bar,
  .activity-bar-warn,
  .activity-bar-bad,
  .activity-bar-latest,
  .mmb-fill,
  .cache-mmb-fill,
  .mmb-fill::before,
  .mmb-fill::after,
  .mmb-flash,
  .mmb-value,
  .meta-block,
  .inactive-channel-row { transition: none !important; animation: none !important; }
}
</style>

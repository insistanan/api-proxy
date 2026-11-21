<template>
  <a
    v-if="url"
    :href="url"
    target="_blank"
    rel="noopener noreferrer"
    class="inline-flex size-6 shrink-0 items-center justify-center rounded-control text-muted-foreground no-underline align-middle hover:bg-primary/10 hover:text-primary"
    :title="`打开 ${channel.name} 的网站`"
    :aria-label="`在新标签页打开 ${channel.name} 的网站`"
    @click.stop
  ><UiIcon size="14">mdi-open-in-new</UiIcon></a>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { Channel } from '@/types'
import UiIcon from '@/shared/ui/icon/Icon.vue'
import { getWebsiteUrl } from '@/features/channels/utils/channelMetricsFormat'

const props = defineProps<{ channel: Channel }>()
const url = computed(() => getWebsiteUrl(props.channel))
</script>

<template>
  <div class="art-preview">
    <canvas ref="canvas" class="forest-art" width="320" height="240" role="img" :aria-label="label"></canvas>
    <p v-if="error" role="status" class="art-error text-xs text-muted-foreground">{{ error }}</p>
  </div>
</template>
<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import type { ForestTree, TreeKind, TreeCondition } from '@/types/forest'
import { drawLandmark, drawTree, ellipse, treePosition } from '@/features/usage-journal/forest/map'
import sprites from '@/features/usage-journal/forest/trees.webp'
const props = withDefaults(
  defineProps<{
    kind?: TreeKind
    treeStyle?: number
    accessory?: string
    landscape?: string
    label: string
    variant?: number
    condition?: TreeCondition
  }>(),
  { kind: 'usage', treeStyle: 0, accessory: '', landscape: '', variant: 7936, condition: 'healthy' }
)
const canvas = ref<HTMLCanvasElement>()
const error = ref('')
const sprite = document.createElement('img')
function draw() {
  const ctx = canvas.value?.getContext('2d')
  if (!ctx) {
    error.value = '当前浏览器无法绘制预览'
    return
  }
  ctx.clearRect(0, 0, 320, 240)
  ctx.save()
  ctx.translate(160, 192)
  ctx.scale(3.5, 3.5)
  ellipse(ctx, 0, 0, 26, 9, '#93bda438')
  ellipse(ctx, 0, 0, 20, 5, '#75a88e48')
  if (props.landscape) {
    if (props.landscape === 'trail')
      for (let i = 0; i < 6; i++) ellipse(ctx, Math.sin(i) * 9, -i * 6, 6, 3, i % 2 ? '#d9d5bd' : '#b6b9a1')
    else drawLandmark(ctx, 0, 0, 1.7, props.landscape === 'lantern' ? 'lighthouse' : 'monument')
  } else {
    const tree: ForestTree = {
      id: 1,
      kind: props.kind,
      style: props.treeStyle,
      accessory: props.accessory,
      variant: props.variant,
      name: '',
      island: 1,
      slot: 0,
      planted: 0,
      assetVersion: 1,
      condition: props.condition,
      conditionRevision: 0
    }
    const point = treePosition(tree)
    ctx.translate(-point.x, -point.y)
    drawTree(ctx, tree, sprite, false, false, 0, false)
  }
  ctx.restore()
}
watch(() => [props.kind, props.treeStyle, props.accessory, props.landscape, props.variant, props.condition], draw)
function spriteFailed() {
  error.value = '插画未能加载，暂以简图展示'
}
onMounted(() => {
  sprite.addEventListener('load', draw)
  sprite.addEventListener('error', spriteFailed)
  sprite.src = sprites
  draw()
})
onUnmounted(() => {
  sprite.removeEventListener('load', draw)
  sprite.removeEventListener('error', spriteFailed)
})
</script>
<style scoped>
.art-preview {
  position: relative;
  width: 100%;
  height: 100%;
}
.forest-art {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: contain;
}
.art-error {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  text-align: center;
}
</style>

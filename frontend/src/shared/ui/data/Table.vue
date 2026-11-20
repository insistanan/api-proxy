<template>
  <div
    class="ui-table min-w-0 overflow-x-auto overscroll-x-contain rounded-control [&_table]:w-full [&_table]:border-collapse [&_th]:h-11 [&_th]:bg-muted/45 [&_th]:px-3 [&_th]:text-left [&_th]:text-xs [&_th]:font-medium [&_th]:text-muted-foreground [&_td]:border-t [&_td]:border-border/70 [&_td]:px-3 [&_td]:py-3 [&_td]:text-sm [&_td]:leading-6 [&_td]:tabular-nums [&_tbody_tr]:transition-colors [&_tbody_tr:hover]:bg-muted/30"
  >
    <table>
      <slot></slot>
    </table>
  </div>
</template>
<script setup lang="ts">
defineProps<{ density?: string; fixedHeader?: boolean }>()
</script>

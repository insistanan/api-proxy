<template>
  <svg xmlns="http://www.w3.org/2000/svg" aria-hidden="true" focusable="false" class="theme-arabesque">
    <defs>
      <pattern :id="patternId" width="96" height="96" patternUnits="userSpaceOnUse">
        <g fill="none" stroke="currentColor" stroke-width="1">
          <path d="M48 10 59 23 73 23 73 37 86 48 73 59 73 73 59 73 48 86 37 73 23 73 23 59 10 48 23 37 23 23 37 23Z" />
          <path d="m48 29 19 19-19 19-19-19Zm0-29v10m48 38H86M48 96V86M0 48h10M0 22l22-22m52 0 22 22M0 74l22 22m52 0 22-22" />
        </g>
      </pattern>
    </defs>
    <rect width="100%" height="100%" :fill="`url(#${patternId})`" />
  </svg>
</template>

<script setup lang="ts">
import { useId } from 'vue'
const patternId = useId()
</script>

<style scoped>
.theme-arabesque {
  color: var(--gold, var(--primary));
  opacity: .095;
  mask-image: linear-gradient(125deg, #000, #0005 60%, #000a);
}
</style>

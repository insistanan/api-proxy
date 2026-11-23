<template>
  <svg xmlns="http://www.w3.org/2000/svg" aria-hidden="true" focusable="false" class="theme-arabesque">
    <defs>
      <pattern :id="patternId" width="96" height="96" patternUnits="userSpaceOnUse">
        <g fill="none" stroke="currentColor" stroke-width="1">
          <path d="M48 8 60 24 80 16 72 36 88 48 72 60 80 80 60 72 48 88 36 72 16 80 24 60 8 48 24 36 16 16 36 24Z" />
          <path d="m48 24 24 24-24 24-24-24Zm0-24v8m48 40h-8M48 96v-8M0 48h8" />
          <rect x="33" y="33" width="30" height="30" />
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
.theme-arabesque { color: var(--gold, var(--primary)); opacity: .05; }
</style>

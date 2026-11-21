<template>
  <RouterLink to="/channels/messages" class="app-brand" data-header-hit aria-label="API Proxy 首页">
    <svg class="brand-mark" viewBox="0 0 48 44" fill="none" aria-hidden="true">
      <path class="brand-tile" d="M11 2H37L46 11V33L37 42H11L2 33V11Z" />
      <path class="brand-a" fill-rule="evenodd" d="M7 33L17 11H24L33 33H25L23.5 28H16L14 33H7ZM18 22H22L20 16L18 22Z" clip-rule="evenodd" />
      <path class="brand-p" fill-rule="evenodd" d="M27 33V11H36C46 11 46 26 36 26H34V33H27ZM34 17V20H36C39 20 39 17 36 17H34Z" clip-rule="evenodd" />
      <path class="brand-signal" d="M7 38H18M38 6H42" stroke-linecap="round" />
    </svg>
    <span class="brand-wordmark" aria-hidden="true"><span class="brand-api">API</span><span class="brand-slash">/</span><span class="brand-proxy">Proxy</span><i class="brand-dot"></i></span>
  </RouterLink>
</template>

<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<style scoped>
@reference "@/styles/index.css";

.app-brand { @apply relative mr-1 inline-flex shrink-0 items-center gap-2 rounded-control no-underline; }
.brand-mark { width: 38px; height: 36px; overflow: visible; }
.brand-tile { fill: color-mix(in srgb, var(--primary) 10%, var(--surface)); stroke: color-mix(in srgb, var(--primary) 35%, var(--border)); stroke-width: 1.2; }
.brand-a { fill: var(--primary); }
.brand-p { fill: var(--accent); }
.brand-signal { stroke: var(--primary); stroke-width: 2; opacity: .6; }
.brand-wordmark { display: inline-flex; align-items: baseline; position: relative; gap: 2px; padding-right: 7px; font-size: 19px; letter-spacing: -.9px; line-height: 1; }
.brand-api { font-family: var(--font-code); font-weight: 700; letter-spacing: -1.5px; color: var(--primary); }
.brand-slash { align-self: center; margin-inline: 2px; font-size: 24px; font-weight: 300; color: var(--outline); transform: skewX(-8deg); }
.brand-proxy { font-weight: 800; font-style: italic; color: var(--foreground); }
.brand-dot { position: absolute; right: 0; top: 0; width: 4px; height: 4px; background: var(--accent); border-radius: 1px; }
[data-palette='pop'] .brand-tile { stroke-width: 2; filter: drop-shadow(2px 2px 0 var(--accent)); }
[data-palette='pop'] .brand-wordmark { font-size: 20px; }
[data-palette='breeze'] .brand-proxy { font-style: normal; font-weight: 650; letter-spacing: -.7px; }
[data-radius='square'] .brand-slash { font-family: var(--font-code); font-size: 19px; transform: none; }
@media (max-width: 600px) { .brand-wordmark { display: none; } }
</style>

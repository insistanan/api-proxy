#!/usr/bin/env node
// 图标注册扫描: 所有 mdi-* 用法必须已在 src/plugins/vuetify.ts 的 iconMap 中注册。
// 未注册图标会在运行时降级为 help-circle 占位, 破坏 UI —— 由本脚本在提交前拦截。
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const rootDir = resolve(fileURLToPath(new URL('..', import.meta.url)).replace(/[\\/]scripts[\\/]?$/, ''))
const srcDir = join(rootDir, 'src')
const pluginFile = join(srcDir, 'plugins', 'vuetify.ts')

const EXTS = new Set(['.vue', '.ts', '.js'])
const USAGE_RE = /mdi-[a-z0-9-]+/g
// iconMap 内条目的两种写法: 'kebab-case': mdiXxx  或   bare: mdiXxx
const ENTRY_RE = /^\s*'?([a-z0-9-]+)'?\s*:\s*mdi[A-Za-z0-9_]+/gm

function* walk(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    const st = statSync(p)
    if (st.isDirectory()) yield* walk(p)
    else if (EXTS.has(p.slice(p.lastIndexOf('.')))) yield p
  }
}

function loadRegistered() {
  const text = readFileSync(pluginFile, 'utf8')
  const mapSlice = text.slice(text.indexOf('iconMap'))
  const set = new Set()
  let m
  while ((m = ENTRY_RE.exec(mapSlice))) set.add(m[1])
  return set
}

function collectUsage() {
  const usage = new Map()
  for (const file of walk(srcDir)) {
    if (file === pluginFile) continue
    const text = readFileSync(file, 'utf8')
    for (const match of text.matchAll(USAGE_RE)) {
      const icon = match[0].slice(4)
      if (!usage.has(icon)) usage.set(icon, new Set())
      usage.get(icon).add(relative(srcDir, file))
    }
  }
  return usage
}

const registered = loadRegistered()
const usage = collectUsage()
const unregistered = [...usage.keys()].filter((k) => !registered.has(k))

if (unregistered.length > 0) {
  console.error('[FAIL] 未注册的 mdi 图标 (请先在 src/plugins/vuetify.ts 的 iconMap 注册):')
  for (const icon of unregistered) {
    const files = [...usage.get(icon)].join(', ')
    console.error(`  - mdi-${icon}   (${files})`)
  }
  process.exit(1)
}

const totalIcons = usage.size
const totalUsages = [...usage.values()].reduce((n, s) => n + s.size, 0)
console.log(`[OK] icon check passed: ${totalUsages} usages, ${totalIcons} icons, all registered.`)

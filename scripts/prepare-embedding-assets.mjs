// 发布前准备内置清单需要的资源；固定附件名包含 SHA-256，与应用 tag 无关。
import { createHash } from 'node:crypto'
import { createReadStream, createWriteStream } from 'node:fs'
import { mkdir, readFile, rename, rm } from 'node:fs/promises'
import path from 'node:path'
import { Readable, Transform } from 'node:stream'
import { pipeline } from 'node:stream/promises'

const output = path.resolve(process.argv[2] || 'release-assets')
const catalog = JSON.parse(await readFile(new URL('../deploy/embedding-presets.json', import.meta.url), 'utf8'))
const resources = new Map()
for (const preset of catalog.presets) {
  for (const manifest of Object.values(preset.platforms)) {
    for (const kind of ['runtime', 'model']) {
      const hash = manifest[`${kind}Sha256`]
      if (!/^[a-f0-9]{64}$/.test(hash)) throw new Error(`资源 SHA-256 无效：${preset.id}/${kind}`)
      const extension = kind === 'model' ? 'gguf' : manifest.runtimeFormat === 'tar.gz' ? 'tar.gz' : 'zip'
      const name = `embedding-${kind}-${hash}.${extension}`
      const urls = [manifest[`${kind}Url`], ...(manifest[`${kind}Mirrors`] || [])]
      const primary = new URL(urls[0])
      if (!primary.pathname.endsWith(`/releases/latest/download/${name}`)) throw new Error(`资源固定入口与附件名不一致：${name}`)
      for (const raw of urls) {
        const url = new URL(raw)
        if (url.protocol !== 'https:' || url.username || url.password) throw new Error(`下载源必须是无凭据 HTTPS：${name}`)
      }
      resources.set(name, { hash, urls, limit: kind === 'model' ? 1024 ** 3 : 128 * 1024 ** 2 })
    }
  }
}

await mkdir(output, { recursive: true })
for (const [name, resource] of resources) {
  const target = path.join(output, name)
  try {
    const hash = createHash('sha256')
    for await (const chunk of createReadStream(target)) hash.update(chunk)
    if (hash.digest('hex') !== resource.hash) throw new Error(`现有产物校验失败：${name}`)
    continue
  } catch (error) {
    if (error.code !== 'ENOENT') throw error
  }
  const failures = []
  let downloaded = false
  for (const raw of resource.urls) {
    const temporary = `${target}.part`
    try {
      const signal = AbortSignal.timeout(10 * 60 * 1000)
      let url = new URL(raw)
      let response
      for (let redirects = 0; ; redirects++) {
        response = await fetch(url, { signal, redirect: 'manual' })
        if (![301, 302, 303, 307, 308].includes(response.status)) break
        const location = response.headers.get('location')
        await response.body?.cancel()
        if (!location || redirects >= 8) throw new Error('下载重定向无效或过多')
        url = new URL(location, url)
        if (url.protocol !== 'https:' || url.username || url.password) throw new Error('下载重定向必须保持 HTTPS')
      }
      if (!response.ok || !response.body) {
        await response.body?.cancel()
        throw new Error(`下载返回 HTTP ${response.status}`)
      }
      const hash = createHash('sha256')
      let size = 0
      const verify = new Transform({
        transform(chunk, _encoding, callback) {
          size += chunk.length
          if (size > resource.limit) return callback(new Error('资源超过大小限制'))
          hash.update(chunk)
          callback(null, chunk)
        },
      })
      await pipeline(Readable.fromWeb(response.body), verify, createWriteStream(temporary, { flags: 'wx' }), { signal })
      if (hash.digest('hex') !== resource.hash) throw new Error('SHA-256 不匹配')
      await rename(temporary, target)
      downloaded = true
      break
    } catch (error) {
      failures.push(error)
      console.warn(`${name} 下载源失败：${error.message}`)
    } finally {
      await rm(temporary, { force: true })
    }
  }
  if (!downloaded) throw new AggregateError(failures, `所有下载源失败：${name}`)
}

#!/usr/bin/env node
import { Buffer } from "node:buffer";
import { mkdir, readFile, stat, writeFile } from "node:fs/promises";
import * as http from "node:http";
import * as https from "node:https";
import { basename, dirname, isAbsolute, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const SKILL_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const CONFIG_PATH = join(SKILL_DIR, "config.json");
const TIMEOUT_MS = 10 * 60 * 1000;
const MAX_DOWNLOAD_REDIRECTS = 5;

function fail(message, extra = {}) {
  process.stdout.write(JSON.stringify({ ok: false, error: message, ...extra }) + "\n");
  process.exit(1);
}

function parseArgs(argv) {
  const out = { images: [] };
  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i];
    if (!token.startsWith("--")) {
      fail(`未知参数: ${token}`);
    }
    const key = token.slice(2);
    const value = argv[i + 1];
    if (value === undefined || value.startsWith("--")) {
      fail(`参数 --${key} 需要值`);
    }
    i += 1;
    if (key === "image") {
      out.images.push(value);
      continue;
    }
    if (out[key] !== undefined) fail(`参数 --${key} 重复`);
    out[key] = value;
  }
  return out;
}

async function loadConfig() {
  let parsed;
  try {
    parsed = JSON.parse(await readFile(CONFIG_PATH, "utf8"));
  } catch (error) {
    fail(`读取 ${CONFIG_PATH} 失败: ${error.message}`);
  }
  const url = String(parsed.url ?? "").trim().replace(/\/+$/, "");
  const key = String(parsed.key ?? "").trim();
  const model = String(parsed.model ?? "").trim();
  if (!url) fail("config.json 缺少 url");
  if (!key) fail("config.json 缺少 key");
  if (!model) fail("config.json 缺少 model");
  return { url, key, model, downloadProxy: parsed.download_proxy };
}

function resolveDownloadProxy(raw) {
  if (raw === undefined) return "";
  if (typeof raw !== "string") fail("download_proxy / --download-proxy 必须是 HTTP(S) 代理地址字符串");
  const value = raw.trim();
  if (!value) return "";
  let proxy;
  try {
    proxy = new URL(value);
  } catch {
    fail("download_proxy / --download-proxy 不是有效的代理 URL");
  }
  if (!["http:", "https:"].includes(proxy.protocol) || proxy.pathname !== "/" || proxy.search || proxy.hash) {
    fail("download_proxy / --download-proxy 只支持 http:// 或 https:// 代理地址，不支持路径、查询参数或片段");
  }
  const [major, minor] = process.versions.node.split(".").map(Number);
  if (major < 24 || (major === 24 && minor < 5)) fail("图片下载代理需要 Node.js 24.5.0 或更高版本");
  return value;
}

function errorDetails(error) {
  const details = [];
  const visit = (current) => {
    if (!current) return;
    const message = current.message || String(current);
    const detail = current.code ? `${message} (${current.code})` : message;
    if (!details.includes(detail)) details.push(detail);
    if (current.cause) visit(current.cause);
    if (Array.isArray(current.errors)) current.errors.forEach(visit);
  };
  visit(error);
  return details.join("; ");
}

function fileStem(raw) {
  const tokens = String(raw ?? "")
    .normalize("NFKC")
    .match(/[\p{L}\p{N}]+/gu) ?? [];
  if (tokens.length === 0) return "image";
  const first = tokens[0];
  const cjk = /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}]/u.test(first);
  const parts = cjk ? [first] : tokens.slice(0, 3);
  const stem = parts.join("-").slice(0, 16).replace(/^-+|-+$/g, "");
  return stem || "image";
}

async function uniqueName(dir, stem, ext) {
  const stamp = timestamp();
  let name = `${stem}-${stamp}.${ext}`;
  for (let n = 2; ; n += 1) {
    try {
      await stat(join(dir, name));
    } catch {
      return name;
    }
    name = `${stem}-${stamp}-${n}.${ext}`;
  }
}

function timestamp() {
  const now = new Date();
  const pad = (value) => String(value).padStart(2, "0");
  return [
    now.getFullYear(),
    pad(now.getMonth() + 1),
    pad(now.getDate()),
    "-",
    pad(now.getHours()),
    pad(now.getMinutes()),
    pad(now.getSeconds()),
  ].join("");
}

function detectFormat(buffer) {
  // 渠道可以覆盖请求格式；保存后缀必须来自实际字节，不能相信响应标签。
  if (buffer.subarray(0, 8).equals(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]))) {
    return "png";
  }
  if (buffer.length >= 3 && buffer[0] === 0xff && buffer[1] === 0xd8 && buffer[2] === 0xff) {
    return "jpeg";
  }
  if (
    buffer.length >= 12 &&
    buffer.toString("ascii", 0, 4) === "RIFF" &&
    buffer.toString("ascii", 8, 12) === "WEBP"
  ) {
    return "webp";
  }
  const signature = buffer.toString("ascii", 0, 6);
  if (signature === "GIF87a" || signature === "GIF89a") {
    return "gif";
  }
  const prefix = buffer.toString("utf8", 0, Math.min(buffer.length, 64 * 1024)).replace(/^\uFEFF/, "").trimStart();
  if (/^(?:(?:<\?xml\b[\s\S]*?\?>|<!--[\s\S]*?-->|<!DOCTYPE\s+svg\b[^>]*>)\s*)*<svg(?:\s|\/?>)/.test(prefix)) {
    return "svg";
  }
  return "";
}

function mimeOf(format) {
  if (format === "svg") return "image/svg+xml";
  if (format === "jpg") return "image/jpeg";
  return format ? `image/${format}` : "";
}

// skipGifSubBlocks 跳过 GIF 的数据子块链，返回子块结束后的偏移。
function skipGifSubBlocks(buffer, offset) {
  while (offset < buffer.length) {
    const size = buffer[offset];
    offset += 1;
    if (size === 0) return offset;
    offset += size;
  }
  return offset;
}

// inspectGif 解析 GIF 块结构，得到真实帧数和循环标记。
// 上游可能把静态图当 GIF 返回，只看 MIME 或文件头无法区分，必须数帧。
function inspectGif(buffer) {
  const signature = buffer.toString("ascii", 0, 6);
  if (signature !== "GIF87a" && signature !== "GIF89a") return null;
  let offset = 6;
  if (buffer.length < offset + 7) return { frames: 0, loop: false };
  const screenPacked = buffer[offset + 4];
  offset += 7;
  if (screenPacked & 0x80) offset += 3 * (1 << ((screenPacked & 0x07) + 1));
  let frames = 0;
  let loop = false;
  while (offset < buffer.length) {
    const marker = buffer[offset];
    offset += 1;
    if (marker === 0x3b) break;
    if (marker === 0x21) {
      const label = buffer[offset];
      offset += 1;
      if (label === 0xff) {
        const blockSize = buffer[offset];
        const appId = buffer.toString("ascii", offset + 1, offset + 1 + Math.min(blockSize, 11));
        if (appId.startsWith("NETSCAPE")) loop = true;
      }
      offset = skipGifSubBlocks(buffer, offset);
      continue;
    }
    if (marker === 0x2c) {
      frames += 1;
      if (buffer.length < offset + 9) break;
      const imagePacked = buffer[offset + 8];
      offset += 9;
      if (imagePacked & 0x80) offset += 3 * (1 << ((imagePacked & 0x07) + 1));
      offset += 1;
      offset = skipGifSubBlocks(buffer, offset);
      continue;
    }
    break;
  }
  return { frames, loop };
}

function extensionOf(format) {
  if (format === "jpeg") return "jpg";
  return format;
}

function fileUrl(absPath) {
  const posix = resolve(absPath).replace(/\\/g, "/");
  const href = /^[A-Za-z]:/.test(posix) ? `file:///${posix}` : `file://${posix}`;
  return encodeURI(href);
}

function resolveExistingPath(raw, projectRoot) {
  const input = String(raw ?? "").trim();
  if (!input) fail("图片路径为空");
  const candidates = isAbsolute(input)
    ? [resolve(input)]
    : [resolve(projectRoot, input), resolve(process.cwd(), input)];
  return candidates;
}

async function loadLocalImage(raw, projectRoot, label) {
  const candidates = resolveExistingPath(raw, projectRoot);
  let lastError = "";
  for (const candidate of candidates) {
    try {
      const info = await stat(candidate);
      if (!info.isFile()) {
        lastError = `${candidate} 不是文件`;
        continue;
      }
      const buffer = await readFile(candidate);
      if (buffer.length === 0) fail(`${label} 为空: ${candidate}`);
      const format = detectFormat(buffer);
      if (!format || format === "svg") fail(`${label} 必须是可识别的位图（png/jpeg/webp/gif）: ${candidate}`);
      return {
        path: candidate,
        buffer,
        mime: mimeOf(format),
        filename: basename(candidate) || `${label}.${extensionOf(format)}`,
      };
    } catch (error) {
      if (error.code === "ENOENT") {
        lastError = `找不到 ${candidate}`;
        continue;
      }
      fail(`读取 ${label} 失败: ${error.message}`, { path: candidate });
    }
  }
  fail(`${label} 不存在: ${raw}`, { tried: candidates, lastError });
}

// 网关请求的超时覆盖响应头和正文读取。
async function fetchWithBody(url, options, readBody) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), TIMEOUT_MS);
  try {
    const response = await fetch(url, { ...options, signal: controller.signal });
    return { response, body: await readBody(response) };
  } catch (error) {
    if (controller.signal.aborted) throw new Error(`请求或读取正文超时（>${TIMEOUT_MS / 1000}s）`);
    throw error;
  } finally {
    clearTimeout(timer);
  }
}

async function requestGateway({ url, key, endpoint, headers, body }) {
  let response;
  let text;
  try {
    const result = await fetchWithBody(`${url}${endpoint}`, {
      method: "POST",
      headers: {
        "x-api-key": key,
        Authorization: `Bearer ${key}`,
        ...headers,
      },
      body,
    }, (reply) => reply.text());
    response = result.response;
    text = result.body;
  } catch (error) {
    fail(`请求网关失败: ${errorDetails(error)}`, { url, endpoint });
  }
  let payload;
  try {
    payload = JSON.parse(text);
  } catch {
    fail(`网关返回非 JSON（HTTP ${response.status}）`, { status: response.status, endpoint, body: text.slice(0, 500) });
  }
  if (!response.ok) {
    const message =
      payload.error?.message ||
      payload.message ||
      payload.details ||
      (typeof payload.error === "string" ? payload.error : "") ||
      `HTTP ${response.status}`;
    fail(String(message), { status: response.status, endpoint });
  }
  return payload;
}

async function downloadImage(rawURL, proxyURL) {
  if (typeof rawURL !== "string") throw new Error("图片 url 必须是字符串");
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), TIMEOUT_MS);
  // 独立代理只作用于图片下载，不把网关访问密钥发给存储服务。
  const proxyEnv = proxyURL ? { HTTP_PROXY: proxyURL, HTTPS_PROXY: proxyURL } : {};
  const agents = {};
  try {
    agents["http:"] = new http.Agent({ proxyEnv });
    agents["https:"] = new https.Agent({ proxyEnv });
    let target = rawURL;
    for (let redirects = 0; ; redirects += 1) {
      let parsed;
      try {
        parsed = new URL(target);
      } catch {
        throw new Error("图片 url 不是有效的绝对 URL");
      }
      if (!["http:", "https:"].includes(parsed.protocol) || parsed.username || parsed.password) {
        throw new Error("图片 url 必须是 HTTP(S) 地址，且不能包含用户名或密码");
      }
      const client = parsed.protocol === "https:" ? https : http;
      // 签名 URL 原样请求；不解码、重排或删改 X-Amz-* 等查询参数。
      const response = await new Promise((resolveResponse, reject) => {
        const request = client.get(target, {
          agent: agents[parsed.protocol],
          signal: controller.signal,
          headers: { "Accept-Encoding": "identity" },
        }, resolveResponse);
        request.on("error", reject);
      });
      const status = response.statusCode;
      if ([301, 302, 303, 307, 308].includes(status)) {
        const location = response.headers.location;
        response.destroy();
        if (!location) throw new Error(`图片 URL 重定向缺少 Location（HTTP ${status}）`);
        if (redirects >= MAX_DOWNLOAD_REDIRECTS) throw new Error(`图片 URL 重定向超过 ${MAX_DOWNLOAD_REDIRECTS} 次`);
        try {
          target = new URL(location, target).href;
        } catch {
          throw new Error("图片 URL 重定向的 Location 无效");
        }
        continue;
      }
      if (status < 200 || status >= 300) {
        response.destroy();
        const hint = status === 403 ? "；签名可能已过期或被修改，需检查签名及存储权限" : "";
        throw new Error(`HTTP ${status}${hint}`);
      }
      const chunks = [];
      for await (const chunk of response) chunks.push(chunk);
      return Buffer.concat(chunks);
    }
  } catch (error) {
    if (controller.signal.aborted) throw new Error(`下载图片 URL 超时（>${TIMEOUT_MS / 1000}s，包含重定向与正文读取）`);
    throw new Error(`下载图片 URL 失败: ${errorDetails(error)}`, { cause: error });
  } finally {
    clearTimeout(timer);
    Object.values(agents).forEach((agent) => agent.destroy());
  }
}

async function decodeImage(item, downloadProxy) {
  if (item?.b64_json) {
    return Buffer.from(item.b64_json, "base64");
  }
  if (item?.url) {
    return downloadImage(item.url, downloadProxy);
  }
  throw new Error("网关响应既没有 b64_json 也没有 url");
}

const SIZE_PRESETS = {
  "1K": {
    "1:1": "1024x1024",
    "3:2": "1536x1024",
    "2:3": "1024x1536",
    "16:9": "1280x720",
    "9:16": "720x1280",
    "4:3": "1024x768",
    "3:4": "768x1024",
    "21:9": "1280x544",
  },
  "2K": {
    "1:1": "2048x2048",
    "3:2": "2160x1440",
    "2:3": "1440x2160",
    "16:9": "2560x1440",
    "9:16": "1440x2560",
    "4:3": "2048x1536",
    "3:4": "1536x2048",
    "21:9": "2560x1088",
  },
  "4K": {
    "1:1": "2880x2880",
    "3:2": "3456x2304",
    "2:3": "2304x3456",
    "16:9": "3840x2160",
    "9:16": "2160x3840",
    "4:3": "3200x2400",
    "3:4": "2400x3200",
    "21:9": "3840x1600",
  },
};

function pick(value, fallback) {
  const text = String(value ?? "").trim();
  return text || fallback;
}

function normalizeRatio(raw) {
  const text = String(raw ?? "").trim().replace(/[xX×]/g, ":");
  const match = text.match(/^(\d+(?:\.\d+)?):(\d+(?:\.\d+)?)$/);
  if (!match) return "";
  return `${Number(match[1])}:${Number(match[2])}`;
}

function normalizeTier(raw) {
  const text = String(raw ?? "").trim().toUpperCase().replace(/\s+/g, "");
  if (text === "1K" || text === "2K" || text === "4K") return text;
  return "";
}

function resolveSize(args, outputFormat) {
  const explicit = String(args.size ?? "").trim();
  if (/^\d+\s*[xX×]\s*\d+$/.test(explicit)) {
    return explicit.replace(/\s+/g, "").replace(/[X×]/g, "x");
  }
  if (explicit.toLowerCase() === "auto") return "auto";
  // GIF 帧要拼进同一张画布，9 帧 1024x1024 会超出上游像素预算并报错。
  // 用户没指定尺寸时给 GIF 用 512x512，避免默认 1K 直接失败。
  if (outputFormat === "gif" && !args.tier && !args.ratio && !normalizeTier(explicit)) {
    return "512x512";
  }
  const tier = normalizeTier(args.tier) || normalizeTier(explicit) || "1K";
  const ratio = normalizeRatio(args.ratio) || "1:1";
  const size = SIZE_PRESETS[tier]?.[ratio];
  if (!size) fail(`不支持的尺寸: tier=${tier} ratio=${ratio}`);
  return size;
}

function resolveCount(raw) {
  const text = String(raw ?? "").trim();
  if (!text) return 1;
  const value = Number(text);
  if (!Number.isInteger(value) || value < 1 || value > 10) fail(`--n 必须是 1-10 的整数: ${raw}`);
  return value;
}

function resolveCompression(raw, outputFormat) {
  const text = String(raw ?? "").trim();
  if (!text) return "";
  if (["png", "gif", "svg"].includes(outputFormat)) fail(`${outputFormat} 不支持 --output-compression`);
  const value = Number(text);
  if (!Number.isInteger(value) || value < 0 || value > 100) fail(`--output-compression 必须是 0-100 的整数: ${raw}`);
  return String(value);
}

function resolveFrameInteger(raw, option, minimum, maximum) {
  if (raw === undefined) return "";
  const text = String(raw).trim();
  const value = Number(text);
  if (!text || !Number.isSafeInteger(value) || value < minimum || (maximum !== undefined && value > maximum)) {
    fail(`--${option} 必须是 ${maximum === undefined ? `至少 ${minimum}` : `${minimum}-${maximum}`} 的整数: ${raw}`);
  }
  return value;
}

function imageParams(args) {
  // 未指定格式时不发参数，允许网关渠道决定输出；帧参数同样按需发送。
  const outputFormat = pick(args["output-format"], "").toLowerCase();
  if (!["", "png", "jpeg", "jpg", "webp", "gif", "svg"].includes(outputFormat)) fail(`不支持的 --output-format: ${outputFormat}`);
  const format = outputFormat === "jpg" ? "jpeg" : outputFormat;
  const frameCount = resolveFrameInteger(args["frame-count"], "frame-count", 2, 16);
  const frameDuration = resolveFrameInteger(args["frame-duration-ms"], "frame-duration-ms", 20);
  if ((frameCount !== "" || frameDuration !== "") && format !== "" && format !== "gif") {
    fail("--frame-count / --frame-duration-ms 仅适用于 GIF；请选择 gif 或跟随渠道格式");
  }
  const count = resolveCount(args.n);
  if (format === "svg" && count !== 1) fail("SVG 仅支持 --n 1");
  if (format === "gif" && args.mask) fail("GIF 不支持 --mask");
  if (args.mask && args.images.length === 0) fail("提供 --mask 时必须同时提供 --image");
  return {
    size: resolveSize(args, format),
    quality: pick(args.quality, "auto").toLowerCase(),
    output_format: format,
    output_compression: resolveCompression(args["output-compression"], format),
    frame_count: frameCount,
    frame_duration_ms: frameDuration,
    moderation: pick(args.moderation, "auto").toLowerCase(),
    background: pick(args.background, "auto").toLowerCase(),
    input_fidelity: String(args["input-fidelity"] ?? "").trim().toLowerCase(),
    n: count,
  };
}

function appendText(form, key, value) {
  if (value === undefined || value === "") return;
  form.append(key, String(value));
}

function filePart(image) {
  return new File([image.buffer], image.filename, { type: image.mime });
}

function applyCommonParams(target, params, { isEdit = false } = {}) {
  const set = (key, value) => {
    if (!value) return;
    if (target instanceof FormData) {
      target.append(key, String(value));
      return;
    }
    target[key] = value;
  };
  set("size", params.size);
  set("quality", params.quality);
  set("output_format", params.output_format);
  set("frame_count", params.frame_count);
  set("frame_duration_ms", params.frame_duration_ms);
  set("moderation", params.moderation);
  set("background", params.background);
  if (params.n > 1) set("n", params.n);
  if (params.output_compression) set("output_compression", params.output_compression);
  if (isEdit) set("input_fidelity", params.input_fidelity);
}

const args = parseArgs(process.argv.slice(2));
const prompt = String(args.prompt ?? "").trim();
const project = String(args.project ?? "").trim();
if (!prompt) fail("缺少 --prompt");
if (!project) fail("缺少 --project");

const projectRoot = resolve(isAbsolute(project) ? project : resolve(process.cwd(), project));
const config = await loadConfig();
const downloadProxy = resolveDownloadProxy(args["download-proxy"] ?? config.downloadProxy);
const model = String(args.model ?? config.model).trim();
if (!model) fail("缺少 model");
const params = imageParams(args);

const references = [];
for (const raw of args.images) {
  references.push(await loadLocalImage(raw, projectRoot, "参考图"));
}
const mask = args.mask ? await loadLocalImage(args.mask, projectRoot, "蒙版") : null;

const isEdit = references.length > 0;
const endpoint = isEdit ? "/v1/images/edits" : "/v1/images/generations";

let result;
if (isEdit) {
  const form = new FormData();
  appendText(form, "model", model);
  appendText(form, "prompt", prompt);
  applyCommonParams(form, params, { isEdit: true });
  for (const image of references) {
    form.append("image[]", filePart(image));
  }
  if (mask) form.append("mask", filePart(mask));
  result = await requestGateway({ url: config.url, key: config.key, endpoint, headers: {}, body: form });
} else {
  const body = { model, prompt };
  applyCommonParams(body, params);
  result = await requestGateway({
    url: config.url,
    key: config.key,
    endpoint,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

const items = Array.isArray(result.data) ? result.data : [];
if (items.length === 0) fail("网关响应 data 为空", { endpoint });

const outDir = join(projectRoot, "temp", "images");
const images = [];
let stage = "save";
try {
  await mkdir(outDir, { recursive: true });
  for (const [index, item] of items.entries()) {
    stage = !item?.b64_json && item?.url ? "download" : "decode";
    const buffer = await decodeImage(item, downloadProxy);
    stage = "decode";
    if (buffer.length === 0) throw new Error(`第 ${index + 1} 张图片解码后为空`);
    const format = detectFormat(buffer);
    if (!format) throw new Error(`第 ${index + 1} 张图片格式无法识别`);
    stage = "save";
    const name = await uniqueName(outDir, fileStem(args.name || prompt), extensionOf(format));
    const outPath = join(outDir, name);
    await writeFile(outPath, buffer, { flag: "wx" });
    // 上游可能忽略请求的 output_format，用真实字节判定，不信任响应标签。
    const gifInfo = format === "gif" ? inspectGif(buffer) : null;
    images.push({
      path: outPath,
      url: fileUrl(outPath),
      name,
      relative: `temp/images/${name}`,
      bytes: buffer.length,
      format,
      mime: mimeOf(format),
      revised_prompt: item.revised_prompt || "",
      ...(gifInfo ? { frames: gifInfo.frames, loop: gifInfo.loop } : {}),
    });
  }
} catch (error) {
  const action = { download: "下载", decode: "解码或识别", save: "保存" }[stage];
  fail(`网关已返回图片结果，${action}第 ${images.length + 1} 张图片失败: ${error.message}`, {
    generation_completed: true,
    stage,
    requested_count: params.n,
    returned_count: items.length,
    images,
  });
}

if (items.length !== params.n) {
  fail(`请求 ${params.n} 张图片，网关实际返回 ${items.length} 张；已保存全部返回图片`, {
    requested_count: params.n,
    returned_count: items.length,
    images,
  });
}

// 请求的 output_format 与真实字节不一致时必须显式报出，不能让调用方以为拿到了目标格式。
const requestedFormat = params.output_format;
const mismatched = requestedFormat
  ? images.filter((image) => image.format !== (requestedFormat === "jpg" ? "jpeg" : requestedFormat))
  : [];
const warnings = [];
if (mismatched.length > 0) {
  warnings.push(
    `请求 output_format=${requestedFormat}，实际返回 ${mismatched.map((image) => image.format).join("、")}；上游可能忽略了该参数`,
  );
}
for (const image of images) {
  if (image.format === "gif" && (image.frames ?? 0) < 2) {
    warnings.push(`${image.name} 只有 ${image.frames ?? 0} 帧，不是真正的动画 GIF`);
  }
}

process.stdout.write(
  JSON.stringify({
    ok: true,
    // 顶层首图字段兼容旧调用方，多图读取 images。
    ...images[0],
    images,
    requested_count: params.n,
    returned_count: items.length,
    model: result.model || model,
    endpoint: isEdit ? "edits" : "generations",
    references: references.map((image) => image.path),
    mask: mask ? mask.path : "",
    ...(warnings.length > 0 ? { warnings } : {}),
  }) + "\n",
);

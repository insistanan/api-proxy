// 将前端构建产物复制到 Go 的资源嵌入目录。
// Makefile、build.sh 和 release workflow 统一通过本脚本复制产物。
// 路径根据脚本位置解析，不依赖当前工作目录，避免手动复制时使用错误的相对路径，
// 产生 backend-go/backend-go/... 或 frontend/backend-go/... 这类嵌套目录。
//
// 行为约定：
// - 仓库根 = scripts/ 所在目录的父级（按 import.meta.url 解析，与 cwd 无关）。
// - 源：<repo>/frontend/dist（Vite build 产物）
// - 目标：<repo>/backend-go/frontend/dist（被 backend-go/webassets.go 的 //go:embed all:frontend/dist 引用）
// - 先清空目标目录，再复制源目录的全部内容；旧产物和 .gitkeep 也会被替换。
// - 源目录不存在时以状态码 2 退出，并提示先执行 cd frontend && npm run build。
//
// 用法：
//   node scripts/copy-frontend.mjs           # 执行复制
//   node scripts/copy-frontend.mjs --check   # 检查嵌套目录和目标目录类型，不校验资源内容

import { existsSync, mkdirSync, rmSync, cpSync, readdirSync, statSync } from "node:fs";
import { dirname, resolve, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import process from "node:process";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "..");
const srcDir = join(repoRoot, "frontend", "dist");
const dstDir = join(repoRoot, "backend-go", "frontend", "dist");

const mode = process.argv[2];

function findNestedDists() {
  // 任意位置出现的 backend-go/backend-go 或 frontend/backend-go 子树都视为脏
  const hits = [];
  for (const candidate of [
    join(repoRoot, "backend-go", "backend-go"),
    join(repoRoot, "frontend", "backend-go"),
  ]) {
    if (existsSync(candidate)) hits.push(relative(repoRoot, candidate));
  }
  return hits;
}

function copyDirContents(from, to) {
  // 清空或创建目标目录，再逐项复制源目录内容；路径由调用方提前解析。
  if (existsSync(to)) {
    for (const entry of readdirSync(to)) {
      rmSync(join(to, entry), { recursive: true, force: true });
    }
  } else {
    mkdirSync(to, { recursive: true });
  }
  for (const entry of readdirSync(from)) {
    const s = join(from, entry);
    const d = join(to, entry);
    cpSync(s, d, { recursive: true });
  }
}

if (mode === "--check") {
  const nested = findNestedDists();
  if (nested.length) {
    console.error("[copy-frontend] 检测到嵌套 dist 残留：");
    for (const p of nested) console.error("  - " + p);
    console.error("[copy-frontend] 请 `rm -rf` 后再提交。");
    process.exit(1);
  }
  // 同时校验目标目录是否就是 embed 指向的那一份
  const expectedDst = dstDir;
  if (!existsSync(expectedDst)) {
    // 目标不存在不算错（首次构建前），但提示一下
    console.log("[copy-frontend] OK（目标暂不存在）");
    process.exit(0);
  }
  const stat = statSync(expectedDst);
  if (!stat.isDirectory()) {
    console.error(`[copy-frontend] 目标不是目录: ${expectedDst}`);
    process.exit(1);
  }
  console.log("[copy-frontend] OK");
  process.exit(0);
}

if (mode !== undefined && mode !== "--help" && mode !== "-h") {
  console.error(`[copy-frontend] 未知参数: ${mode}`);
  console.error("用法: node scripts/copy-frontend.mjs [--check|--help]");
  process.exit(2);
}

if (mode === "--help" || mode === "-h") {
  console.log("用法:");
  console.log("  node scripts/copy-frontend.mjs           复制 frontend/dist → backend-go/frontend/dist");
  console.log("  node scripts/copy-frontend.mjs --check   仅做嵌套 dist 体检");
  process.exit(0);
}

if (!existsSync(srcDir)) {
  console.error(`[copy-frontend] 源目录不存在: ${srcDir}`);
  console.error("  请先构建前端: cd frontend && npm run build");
  process.exit(2);
}

const start = Date.now();
copyDirContents(srcDir, dstDir);
const ms = Date.now() - start;
console.log(`[copy-frontend] 已复制 ${srcDir} -> ${dstDir} (${ms} ms)`);

// 复制完再体检一次，留存快照防漂移
const nested = findNestedDists();
if (nested.length) {
  console.error("[copy-frontend] 复制后检测到嵌套 dist（异常，请检查复制脚本）：");
  for (const p of nested) console.error("  - " + p);
  process.exit(1);
}

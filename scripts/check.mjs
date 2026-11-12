// 执行全量门禁：先检查行尾和嵌套 dist，再校验目录在 AGENTS.md 中的登记，
// 然后运行后端 gofmt、vet、test，以及前端类型检查、图标扫描、Vitest 和 ESLint。
// 根 Makefile 的 check 目标也调用本脚本，未安装 make 时可直接使用 npm run check。
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import process from "node:process";

const isWin = process.platform === "win32";
const npmCmd = isWin ? "npm.cmd" : "npm";

function ensureHooksPath() {
  const r = spawnSync("git", ["config", "--local", "--get", "core.hooksPath"], { encoding: "utf8" });
  if (r.status === 0 && r.stdout.trim() === ".githooks") return;
  const set = spawnSync("git", ["config", "--local", "core.hooksPath", ".githooks"], { encoding: "utf8" });
  if (set.status !== 0) fail("设置 core.hooksPath", set.stderr);
}

function fail(name, detail) {
  console.error(`\n[check] FAILED: ${name}`);
  if (detail) console.error(detail);
  process.exit(1);
}

// 带输出校验的步骤：命令本身退出码为 0 不代表通过（如 gofmt -l）。
function runValidated(name, cmd, args, cwd, validate) {
  console.log(`\n==> ${name}`);
  const r = spawnSync(cmd, args, { cwd, encoding: "utf8" });
  if (r.error) fail(name, r.error.message);
  if (r.status !== 0) fail(name, r.stdout + r.stderr);
  const problem = validate(r.stdout);
  if (problem) fail(name, problem);
  console.log("[check] OK");
}

function run(name, cmd, args, cwd, opts = {}) {
  console.log(`\n==> ${name}`);
  const r = spawnSync(cmd, args, { cwd, stdio: "inherit", ...opts });
  if (r.error) fail(name, r.error.message);
  if (r.status !== 0) fail(name);
  console.log("[check] OK");
}

ensureHooksPath();

runValidated(
  "工作区换行校验",
  "node",
  ["scripts/eol.mjs", "--check"],
  ".",
  (out) => null,
);

runValidated(
  "前端产物复制脚本嵌套体检",
  "node",
  ["scripts/copy-frontend.mjs", "--check"],
  ".",
  (out) => null,
);

// 文档-代码一致性门禁：目录会持续增生，人肉维护 AGENTS.md 必然漏登，
// 新增目录未登记到对应文档时在此直接失败。仅做正向校验（实际目录 → 文档登记），
// 文档中已失效的条目不在此拦截，避免对正文表述造成误报。
// 文档中目录名可能带尾部斜杠（如 `docs/`），两种写法都视为已登记。
function docHasEntry(docText, name) {
  return docText.includes("`" + name + "`") || docText.includes("`" + name + "/`");
}

function failMissing(gateName, docPath, names) {
  fail(gateName, `以下目录未在 ${docPath} 登记（新增包或目录后请同步文档）：\n${names.join("\n")}`);
}

function checkDocRegistry(gateName, baseDir, docPath, ignore, prefix) {
  console.log(`\n==> ${gateName}`);
  const docText = fs.readFileSync(docPath, "utf8");
  const missing = fs
    .readdirSync(baseDir, { withFileTypes: true })
    .filter((e) => e.isDirectory() && !ignore.has(e.name))
    .map((e) => prefix + e.name)
    .filter((name) => !docHasEntry(docText, name));
  if (missing.length) failMissing(gateName, docPath, missing);
  console.log("[check] OK");
}

// internal/cmd 下的包目录全部入仓且不会出现杂物，直接扫描文件系统。
checkDocRegistry(
  "后端 internal 包目录登记校验",
  "backend-go/internal",
  "backend-go/AGENTS.md",
  new Set(["handlers", "core"]),
  "",
);
checkDocRegistry(
  "后端 handlers 包目录登记校验",
  "backend-go/internal/handlers",
  "backend-go/AGENTS.md",
  new Set(),
  "handlers/",
);
checkDocRegistry(
  "后端 core 包目录登记校验",
  "backend-go/internal/core",
  "backend-go/AGENTS.md",
  new Set(),
  "core/",
);
checkDocRegistry(
  "后端 cmd 目录登记校验",
  "backend-go/cmd",
  "backend-go/AGENTS.md",
  new Set(),
  "cmd/",
);

// 仓库根目录存在不入仓的工具与产物目录，只校验 git 跟踪的目录，
// 新目录必须先在根 AGENTS.md 仓库结构表中登记才算入仓。
{
  console.log("\n==> 仓库根目录登记校验");
  const tracked = spawnSync("git", ["ls-tree", "--name-only", "HEAD"], { encoding: "utf8", cwd: "." });
  if (tracked.error || tracked.status !== 0) fail("仓库根目录登记校验", "git ls-tree 失败：" + (tracked.error?.message ?? tracked.stderr));
  const docText = fs.readFileSync("AGENTS.md", "utf8");
  const missing = tracked.stdout
    .split("\n")
    .filter((line) => line.length > 0 && fs.existsSync(line) && fs.statSync(line).isDirectory() && !docHasEntry(docText, line));
  if (missing.length) failMissing("仓库根目录登记校验", "AGENTS.md", missing);
  console.log("[check] OK");
}

runValidated(
  "后端 gofmt 校验",
  "gofmt",
  ["-l", "."],
  "backend-go",
  (out) => out.trim() && `以下文件未格式化:\n${out}`,
);

run("后端 go vet", "go", ["vet", "./..."], "backend-go");
run("后端 go test", "go", ["test", "./..."], "backend-go");
// Windows 下 npm 是 npm.cmd，必须经 shell 启动；命令串 + 空 args 避免 DEP0190。
run("前端 type-check + 图标扫描", isWin ? "npm run check" : "npm", isWin ? [] : ["run", "check"], "frontend", isWin ? { shell: true } : {});
run("前端单元测试", isWin ? "npm test" : "npm", isWin ? [] : ["test"], "frontend", isWin ? { shell: true } : {});
// ESLint 接入门禁（R6）：存量已清零，--max-warnings 0 阻断任何新增
// 未使用变量 / any / 模板遮蔽问题，不保留只输出警告的假门禁。
// Windows 下 npm/npx 是 .cmd，必须经 shell 启动；命令串 + 空 args 避免 DEP0190。
run(
  "前端 ESLint 校验",
  isWin ? "npx eslint . --ext .vue,.js,.ts --max-warnings 0" : "npx",
  isWin ? [] : ["eslint", ".", "--ext", ".vue,.js,.ts", "--max-warnings", "0"],
  "frontend",
  isWin ? { shell: true } : {}
);

console.log("\n[check] 全量门禁通过");

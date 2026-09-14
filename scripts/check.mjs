// 全量门禁：前端 type-check + 图标扫描；后端 gofmt + vet + test。
// 等价于根 Makefile 的 check 目标，供未安装 make 的环境直接 `npm run check` 使用。
import { spawnSync } from "node:child_process";
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

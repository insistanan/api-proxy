// 桌面构建和检查共用的路径、工具版本与进程调用；所有路径相对于脚本解析。
import { spawnSync } from "node:child_process";
import { readFileSync, existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import process from "node:process";

export const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
export const backendRoot = join(repoRoot, "backend-go");
export const desktopRoot = join(backendRoot, "desktop");
const goModule = readFileSync(join(backendRoot, "go.mod"), "utf8");
export const wailsVersion = goModule.match(/^\s*github\.com\/wailsapp\/wails\/v3\s+(v\S+)$/m)?.[1];
export const goToolchain = goModule.match(/^toolchain (go\S+)$/m)?.[1];
if (!wailsVersion || !goToolchain) throw new Error("go.mod 必须固定 Wails 和 Go 工具链版本");

export function run(command, args, { cwd = backendRoot, env = process.env, capture = false, includeStderr = false } = {}) {
  const result = spawnSync(command, args, { cwd, env, encoding: "utf8", stdio: capture ? "pipe" : "inherit" });
  if (result.error) throw new Error(`执行 ${command} 失败: ${result.error.message}`);
  if (result.status !== 0) throw new Error(`${command} 失败（退出码 ${result.status ?? result.signal}）${capture ? ": " + result.stdout + result.stderr : ""}`);
  return capture ? (result.stdout + (includeStderr ? result.stderr : "")).trim() : "";
}

export function wailsCommand() {
  const executable = process.platform === "win32" ? "wails3.exe" : "wails3";
  // stdout 开头的空 GOBIN 需要保留；直接读取 JSON，避免 trim 丢失字段位置。
  const environment = JSON.parse(run("go", ["env", "-json", "GOBIN", "GOPATH"], { capture: true }));
  const directory = environment.GOBIN || join(environment.GOPATH.split(process.platform === "win32" ? ";" : ":")[0], "bin");
  const installed = join(directory, executable);
  return existsSync(installed) ? installed : executable;
}

export function requireDesktopTools(platform) {
  const version = run("go", ["version"], { capture: true });
  if (!version.includes(` ${goToolchain} `)) throw new Error(`桌面构建要求 ${goToolchain}，当前为 ${version}`);
  const wails = wailsCommand();
  // Wails CLI 的版本信息输出到 stderr；Go 环境 JSON 仍只读取 stdout。
  const info = run(wails, ["version"], { capture: true, includeStderr: true });
  const actualVersion = info.match(/\bv?\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?\b/)?.[0];
  if (actualVersion?.replace(/^v/, "") !== wailsVersion.replace(/^v/, "")) {
    throw new Error(`Wails CLI 必须与 go.mod 一致（${wailsVersion}），请运行 go install github.com/wailsapp/wails/v3/cmd/wails3@${wailsVersion}`);
  }
  if (platform === "linux" && process.platform !== "linux") throw new Error("Linux 桌面构建请在 Linux 或对应 CI runner 上执行");
  if (platform === "darwin" && process.platform !== "darwin") throw new Error("macOS 桌面构建请在 macOS 或对应 CI runner 上执行");
  return wails;
}

export function currentPlatform() {
  const os = { win32: "windows", darwin: "darwin", linux: "linux" }[process.platform];
  const arch = { x64: "amd64", arm64: "arm64" }[process.arch];
  if (!os || !arch) throw new Error(`桌面版不支持当前平台 ${process.platform}/${process.arch}`);
  return `${os}/${arch}`;
}

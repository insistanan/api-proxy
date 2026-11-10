// 共用现有前端产物链路；在临时 Go module 副本中生成资源，避免 .syso 污染服务构建。
import { cpSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync, chmodSync, statSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, dirname, join, relative, resolve, sep } from "node:path";
import process from "node:process";
import { backendRoot, currentPlatform, desktopRoot, repoRoot, requireDesktopTools, run } from "./desktop-common.mjs";

function platformArgument() {
  const args = process.argv.slice(2);
  if (args.length === 0) return currentPlatform();
  if (args.length === 2 && args[0] === "--platform" && /^(windows|darwin|linux)\/(amd64|arm64)$/.test(args[1])) return args[1];
  throw new Error("用法: npm run build:desktop -- [--platform windows/amd64|windows/arm64|darwin/amd64|darwin/arm64|linux/amd64|linux/arm64]");
}

function windowsIcon(png) {
  const header = Buffer.alloc(22);
  header.writeUInt16LE(1, 2); header.writeUInt16LE(1, 4);
  header.writeUInt16LE(1, 10); header.writeUInt16LE(32, 12);
  header.writeUInt32LE(png.length, 14); header.writeUInt32LE(22, 18);
  return Buffer.concat([header, png]);
}

function macIcon(png) {
  const header = Buffer.alloc(16);
  header.write("icns", 0); header.writeUInt32BE(16 + png.length, 4);
  header.write("ic08", 8); header.writeUInt32BE(8 + png.length, 12);
  return Buffer.concat([header, png]);
}

function copySources(stage) {
  for (const entry of readdirSync(backendRoot, { withFileTypes: true })) {
    if (entry.isFile() && (entry.name.endsWith(".go") || entry.name === "go.mod" || entry.name === "go.sum")) {
      cpSync(join(backendRoot, entry.name), join(stage, entry.name));
    }
  }
  cpSync(join(backendRoot, "internal"), join(stage, "internal"), {
    recursive: true,
    // internal 当前无 go:embed，只复制目录和 Go 源文件，运行配置与密钥不会进入副本。
    filter: (path) => statSync(path).isDirectory() ? !/^(\.config|\.ccb_config|\.git|node_modules)$/.test(basename(path)) : path.endsWith(".go"),
  });
  cpSync(join(backendRoot, "frontend", "dist"), join(stage, "frontend", "dist"), { recursive: true });
  mkdirSync(join(stage, "desktop"));
  cpSync(join(desktopRoot, "appicon.png"), join(stage, "desktop", "appicon.png"));
}

function xml(value) {
  return value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;");
}

function packageDesktop(os, arch, stage, binary, version, output) {
  const name = `api-proxy-desktop-${os}-${arch}`;
  const packageRoot = join(stage, "package");
  mkdirSync(packageRoot);
  if (os === "windows") {
    cpSync(binary, join(output, `${name}.exe`));
    return;
  }
  const png = readFileSync(join(desktopRoot, "appicon.png"));
  if (os === "darwin") {
    const bundle = join(packageRoot, "API Proxy.app");
    const contents = join(bundle, "Contents");
    mkdirSync(join(contents, "MacOS"), { recursive: true });
    mkdirSync(join(contents, "Resources"));
    const executable = join(contents, "MacOS", "api-proxy-desktop");
    cpSync(binary, executable); chmodSync(executable, 0o755);
    writeFileSync(join(contents, "Resources", "appicon.icns"), macIcon(png));
    writeFileSync(join(contents, "Info.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>API Proxy</string><key>CFBundleDisplayName</key><string>API Proxy</string>
<key>CFBundleIdentifier</key><string>com.insistanan.api-proxy</string><key>CFBundleExecutable</key><string>api-proxy-desktop</string>
<key>CFBundlePackageType</key><string>APPL</string><key>CFBundleIconFile</key><string>appicon.icns</string>
<key>CFBundleShortVersionString</key><string>${xml(version)}</string><key>CFBundleVersion</key><string>${xml(version)}</string>
<key>LSMinimumSystemVersion</key><string>12.0</string>
<key>NSAppTransportSecurity</key><dict><key>NSAllowsLocalNetworking</key><true/><key>NSAllowsArbitraryLoadsInWebContent</key><true/></dict>
<key>NSHighResolutionCapable</key><true/><key>NSHumanReadableCopyright</key><string>MIT License</string>
</dict></plist>
`);
    run("codesign", ["--force", "--deep", "--sign", "-", bundle], { cwd: stage });
    run("ditto", ["-c", "-k", "--sequesterRsrc", "--keepParent", bundle, join(output, `${name}.zip`)], { cwd: stage });
    return;
  }
  const directory = join(packageRoot, name);
  mkdirSync(directory);
  cpSync(binary, join(directory, "api-proxy-desktop")); chmodSync(join(directory, "api-proxy-desktop"), 0o755);
  writeFileSync(join(directory, "appicon.png"), png);
  writeFileSync(join(directory, "api-proxy-desktop.desktop"), `[Desktop Entry]\nName=API Proxy\nComment=LLM API 代理网关\nExec=api-proxy-desktop\nIcon=api-proxy-desktop\nType=Application\nCategories=Development;Utility;\nTerminal=false\nStartupWMClass=api-proxy-desktop\n`);
  run("tar", ["-czf", join(output, `${name}.tar.gz`), "-C", packageRoot, name], { cwd: stage });
}

let stage;
try {
  const target = platformArgument();
  const [os, arch] = target.split("/");
  const wails = requireDesktopTools(os);
  if (os === "linux" && arch !== currentPlatform().split("/")[1] && !process.env.CC) throw new Error("Linux 跨架构 CGO 构建需要显式设置 CC，或使用对应架构 runner");
  const requiredNode = readFileSync(join(repoRoot, ".node-version"), "utf8").trim();
  if (Number(process.versions.node.split(".")[0]) < Number(requiredNode.split(".")[0])) throw new Error(`请使用 Node ${requiredNode} 或更新的受支持版本`);
  const version = readFileSync(join(repoRoot, "VERSION"), "utf8").trim().replace(/^v/, "");
  if (!/^\d+\.\d+\.\d+$/.test(version)) throw new Error("VERSION 必须包含三段数字版本号");
  if (process.env.RELEASE_TAG && process.env.RELEASE_TAG !== `v${version}`) throw new Error("发布 tag 与 VERSION 不一致");
  const commit = run("git", ["rev-parse", "--short", "HEAD"], { cwd: repoRoot, capture: true });
  const buildTime = new Date().toISOString();
  // 直接执行 npm CLI，避免 Windows .cmd 和 shell 对路径的二次解释。
  const npmCLI = process.env.npm_execpath || join(dirname(process.execPath), "node_modules", "npm", "bin", "npm-cli.js");
  if (!npmCLI.endsWith(".js") || !existsSync(npmCLI)) throw new Error("找不到 npm CLI，请通过 npm run build:desktop 执行构建");
  run(process.execPath, [npmCLI, "run", "build"], { cwd: join(repoRoot, "frontend") });
  run(process.execPath, [join(repoRoot, "scripts", "copy-frontend.mjs")], { cwd: repoRoot });
  stage = mkdtempSync(join(tmpdir(), "api-proxy-desktop-"));
  copySources(stage);
  if (os === "windows") {
    writeFileSync(join(stage, "desktop", "icon.ico"), windowsIcon(readFileSync(join(desktopRoot, "appicon.png"))));
    writeFileSync(join(stage, "desktop", "info.json"), JSON.stringify({ fixed: { file_version: version }, info: { "0000": { ProductVersion: version, ProductName: "API Proxy", FileDescription: "API Proxy 桌面应用", CompanyName: "insistanan", LegalCopyright: "MIT License" } } }));
    run(wails, ["generate", "syso", "-arch", arch, "-icon", join(stage, "desktop", "icon.ico"), "-manifest", join(desktopRoot, "windows.manifest"), "-info", join(stage, "desktop", "info.json"), "-out", join(stage, `wails_windows_${arch}.syso`)], { cwd: stage });
  }
  const binary = join(stage, os === "windows" ? "api-proxy-desktop.exe" : "api-proxy-desktop");
  const ldflags = `-s -w -X main.Version=v${version} -X main.BuildTime=${buildTime} -X main.GitCommit=${commit}${os === "windows" ? " -H windowsgui" : ""}`;
  const env = { ...process.env, GOOS: os, GOARCH: arch, CGO_ENABLED: os === "windows" ? "0" : "1" };
  run("go", ["build", "-mod=readonly", "-tags", "desktop,production", "-trimpath", "-ldflags", ldflags, "-o", binary, "."], { cwd: stage, env });
  const output = join(repoRoot, "dist", "desktop");
  mkdirSync(output, { recursive: true });
  packageDesktop(os, arch, stage, binary, version, output);
  console.log(`[desktop] 构建完成: ${target} → ${output}`);
} catch (error) {
  console.error(`[desktop] ${error.message}`);
  process.exitCode = 1;
} finally {
  if (stage) {
    const parent = resolve(tmpdir());
    const child = relative(parent, resolve(stage));
    if (!child || child.startsWith("..") || child.includes(sep) || !basename(stage).startsWith("api-proxy-desktop-")) {
      throw new Error("临时构建目录超出预期范围，拒绝清理");
    }
    rmSync(stage, { recursive: true });
  }
}

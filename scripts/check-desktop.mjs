// 默认门禁保持服务版检查；桌面标签由显式命令在具备原生依赖的机器上检查。
import process from "node:process";
import { currentPlatform, requireDesktopTools, run } from "./desktop-common.mjs";

try {
  const [os] = currentPlatform().split("/");
  requireDesktopTools(os);
  const env = { ...process.env, CGO_ENABLED: os === "windows" ? "0" : "1" };
  run("go", ["vet", "-mod=readonly", "-tags", "desktop,production", "./..."], { env });
  run("go", ["test", "-mod=readonly", "-tags", "desktop,production", "./..."], { env });
  console.log("[desktop] 桌面标签检查通过");
} catch (error) {
  console.error(`[desktop] ${error.message}`);
  process.exitCode = 1;
}

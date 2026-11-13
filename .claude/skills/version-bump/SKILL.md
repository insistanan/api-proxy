---
name: version-bump
description: 升级项目版本号，支持 patch/minor/major 或指定版本，并按用户授权提交、打 tag、推送。仅提交代码或咨询版本时不触发。
version: 1.1.0
author: https://github.com/insistanan/api-proxy/
allowed-tools: Bash, Read, Write
context: fork
---

# 版本升级

## 版本与文件

- `VERSION` 是当前版本入口，格式为 `v{major}.{minor}.{patch}`，不含后缀。
- 默认升 `patch`；`minor` 将 patch 归零，`major` 将 minor/patch 归零；指定版本时使用该版本。
- 同步 `frontend/package.json` 的无 `v` 版本号，以及锁文件中已有的根包版本记录；不改变依赖版本。
- 将 `CHANGELOG.md` 的 `[Unreleased]` 内容归入新版本，使用当前日期；变更记录以该文件为准，不编造条目。
- README 和技术文档引用 `VERSION`，不新增当前版本号副本；核对根 `AGENTS.md` 的版本核对标记及项目口径。

## 流程

1. 查看工作区与暂存区差异，读取版本文件和 CHANGELOG，确定目标版本及本次授权范围。仅要求给当前版本打 tag 时，跳过版本文件修改。
2. 更新上述文件，核对版本一致性和 diff；保留已有修改及其暂存状态。
3. 需要提交时，确认项目要求的 `npm run check`（或 `make check`）已在本次待提交内容上通过；执行检查仍须用户授权，未运行或失败时明确说明并停止提交。
4. 按已获授权完成提交、tag 和推送。仅暂存、提交本次授权的文件或片段；提交信息可用 `chore: bump version to v{版本}`。从当前分支及远程配置确定推送目标，不写死 `main` 或 `origin`。
5. 简述新旧版本、修改文件、检查状态及实际完成的 Git 操作。

## 发布边界

- 升版本不默认授权提交、创建 tag 或推送；已明确授权的步骤直接执行，不重复确认。`--no-tag` / “不要 tag”时跳过 tag。
- 推送 `v*` tag 会触发三平台发布 workflow，生成 Linux/macOS/Windows × amd64/arm64 的 Draft Release 产物；不会自动发布容器镜像。
- 公开发布及整理公告由 `github-release` 处理。发布链路详见 [开发说明](../../../docs/开发说明.md)。

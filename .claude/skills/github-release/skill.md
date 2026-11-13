---
name: github-release
description: 从 CHANGELOG 整理 GitHub Release 公告、更新发布草稿或正式发布。用于发布说明、编辑 Draft 或发布 Release；不负责升级版本号。
version: 1.2.0
author: https://github.com/insistanan/api-proxy/
allowed-tools: Bash, Read
context: fork
---

# GitHub Release

## 流程

1. 核对 Git 远程仓库、目标 tag、Release 状态及 `gh` 登录权限。未指定版本时选择最新版本，不以提交顺序猜测版本先后。
2. 找到目标版本之前最近的公开发布版本，从 `CHANGELOG.md` 汇总两者之间的全部变更，包括中间未公开版本；不以上一个 Draft 作为公告起点。
3. 按下方约定生成公告。仅要求发布说明时交付正文；要求更新 Draft 时保留草稿状态；已明确授权正式发布时执行发布，无需重复确认。
4. 发布前核对三个平台的构建已成功、目标 Release 产物齐备。优先编辑现有 Draft；缺少 Draft 时先查明构建与产物状态，不创建空 Release 冒充完整发布。
5. 操作后重新读取 Release 状态；只报告已核实的版本、草稿或公开状态、链接，以及实际清理的 Draft。

## 公告约定

- 面向最终用户，中文简述可感知的变化，省略文件路径、内部类型和实现细节；内容以 CHANGELOG 为准。
- 按 `✨ 新功能`、`🐛 修复`、`🔧 改进` 分组，无内容的分组省略；合并重复条目。
- 末尾用 `---` 分隔，并附 `**Full Changelog**: <仓库 URL>/compare/<上次公开 tag>...<目标 tag>`；仓库 URL 从实际远程确定。
- 首次公开发布没有比较起点时，明确说明，不编造旧 tag 或 compare 链接。
- 公告正文写入临时文件，通过 `--notes-file` 传给 `gh release edit/create`，避免多行文本的 shell 转义问题。

## 操作边界

- 已有 Draft 用 `gh release edit` 更新；只有授权正式发布时才设置 `--draft=false`。新建草稿时显式使用 `--draft`。
- 多个 Draft 的变更可以汇总到目标版本公告，但仅在用户要求清理并确认删除范围后删除旧 Draft；不删除对应 Git tag。
- 失败时说明实际错误和已完成步骤，不宣称发布成功。版本升级与 tag 构建链路见 [开发说明](../../../docs/开发说明.md)。

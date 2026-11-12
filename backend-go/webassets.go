// Package apiproxy 是模块根包，只负责嵌入前端构建产物。
//
// go:embed 只能引用声明文件所在目录及其子目录中的文件，而前端产物位于 backend-go/frontend/dist，
// 因此嵌入声明必须留在 backend-go/ 根目录；入口程序位于 cmd/server，通过 FrontendFS 取用。
package apiproxy

import "embed"

// FrontendFS 为前端构建产物（由 scripts/copy-frontend.mjs 复制到 frontend/dist）。
//
//go:embed all:frontend/dist
var FrontendFS embed.FS

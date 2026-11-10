package main

import "path/filepath"

// appPaths 把相对数据路径固定到运行根目录；空根目录保留服务版的工作目录语义。
// 自定义绝对日志路径不会被拼接，也不会把解析后的路径回写到用户配置。
type appPaths struct {
	root string
}

func (p appPaths) resolve(path string) string {
	if p.root == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(p.root, path)
}

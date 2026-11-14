//go:build !windows

package main

// setConsoleTitle 仅在 Windows 控制台有意义，其他平台保持空实现。
func setConsoleTitle(string) {}

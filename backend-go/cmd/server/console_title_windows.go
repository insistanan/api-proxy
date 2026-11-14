//go:build windows

package main

import (
	"log"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procSetConsoleTitleW = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleTitleW")

// setConsoleTitle 覆盖控制台/终端标签标题。
// Windows Terminal 会沿用上一个命令留下的标题，直接双击 exe 时也可能显示路径；
// 启动时显式设置一次，窗口名就与服务一致。
func setConsoleTitle(title string) {
	ptr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		log.Printf("[Server-Init] 控制台标题编码失败: %v", err)
		return
	}
	if result, _, callErr := procSetConsoleTitleW.Call(uintptr(unsafe.Pointer(ptr))); result == 0 {
		log.Printf("[Server-Init] 设置控制台标题失败: %v", callErr)
	}
}

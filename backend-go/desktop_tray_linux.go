//go:build desktop && linux

package main

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)

// Linux 托盘依赖桌面环境的 StatusNotifierWatcher；不可用时保留窗口的退出入口。
func desktopTrayAvailable(parent context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	connection, err := dbus.ConnectSessionBus(dbus.WithContext(ctx))
	if err != nil {
		return false, "连接桌面 D-Bus 失败: " + err.Error()
	}
	defer connection.Close()
	var names []string
	if err := connection.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return false, "查询系统托盘支持失败: " + err.Error()
	}
	for _, name := range names {
		if name == "org.kde.StatusNotifierWatcher" {
			return true, ""
		}
	}
	return false, "当前桌面环境未提供 StatusNotifier 系统托盘"
}

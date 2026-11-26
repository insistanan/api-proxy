package intent

// localProcess 让运行时只依赖启动、等待与停止；平台层负责宿主退出约束。
type localProcess struct {
	Wait func() error
	Kill func() error
}

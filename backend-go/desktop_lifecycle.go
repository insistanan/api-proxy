//go:build desktop

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/insistanan/api-proxy/internal/skills"
	"github.com/joho/godotenv"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func (d *desktopApp) runGateway(ctx context.Context) {
	defer close(d.done)
	select {
	case <-d.started:
	case <-ctx.Done():
		return
	}
	if d.startupErr != nil {
		d.reportError(d.startupErr)
		return
	}
	if err := d.preparePaths(); err != nil {
		d.reportError(err)
		return
	}
	if _, err := fs.ReadFile(frontendFS, "frontend/dist/index.html"); err != nil {
		d.reportError(fmt.Errorf("内嵌管理页面缺失，请使用完整桌面构建命令: %w", err))
		return
	}
	gateway, err := newAppWithPaths(d.options.paths)
	if err != nil {
		d.reportError(err)
		return
	}
	defer func() {
		if err := gateway.shutdown(); err != nil {
			d.reportError(err)
		}
	}()
	if ctx.Err() != nil {
		return
	}
	if !gateway.envCfg.EnableWebUI {
		d.reportError(errors.New("桌面版需要启用管理页面，请设置 ENABLE_WEB_UI=true"))
		return
	}
	if gateway.envCfg.Port < 1 || gateway.envCfg.Port > 65535 {
		d.reportError(errors.New("PORT 必须处于 1 到 65535 之间"))
		return
	}
	address := fmt.Sprintf("127.0.0.1:%d", gateway.envCfg.Port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		d.reportError(fmt.Errorf("本地代理监听失败（端口 %d），不会自动更换端口: %w", gateway.envCfg.Port, err))
		return
	}
	// listener 已绑定，Serve 随即接管它；关闭窗口与系统退出共用 ctx 取消信号。
	stopped := make(chan error, 1)
	go func() {
		stopped <- serveListenerUntilStopped(ctx, gateway.newHTTPServer(address), listener, 10*time.Second)
	}()
	d.mu.Lock()
	d.address = "http://" + address
	d.mu.Unlock()
	gateway.logStartupInfo()
	supported, reason := desktopTrayAvailable(ctx)
	application.InvokeAsync(func() {
		if ctx.Err() != nil || d.quit.Load() {
			return
		}
		d.mu.Lock()
		ready := d.address == "http://"+address
		d.mu.Unlock()
		if !ready {
			return
		}
		d.window.SetURL("http://" + address)
		if supported {
			d.tray.Store(true)
			tray := d.app.SystemTray.New()
			tray.SetIcon(desktopIcon)
			tray.SetTooltip("API Proxy · 本地代理运行中")
			tray.SetMenu(d.trayMenu)
			tray.OnClick(d.showWindow)
		} else {
			d.app.Dialog.Warning().SetTitle("系统托盘不可用").SetMessage(reason + "\n代理服务仍在运行，关闭管理窗口将退出应用。").Show()
		}
	})
	serveErr := <-stopped
	d.mu.Lock()
	d.address = ""
	d.mu.Unlock()
	if serveErr != nil {
		d.reportError(serveErr)
	}
}

func (d *desktopApp) preparePaths() error {
	if err := os.MkdirAll(d.options.paths.root, 0700); err != nil {
		return fmt.Errorf("创建桌面数据目录失败: %w", err)
	}
	// main 已按旧顺序加载 exe 同目录和工作目录 .env；数据目录只补充未设置的项。
	envPath := filepath.Join(d.options.paths.root, ".env")
	if _, err := os.Stat(envPath); err == nil {
		if err := godotenv.Load(envPath); err != nil {
			return fmt.Errorf("加载桌面数据目录 .env 失败: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("读取桌面数据目录 .env 失败: %w", err)
	}
	return skills.BindProjectDirectory(d.options.projectDir)
}

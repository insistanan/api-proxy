//go:build desktop

package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed desktop/appicon.png
var desktopIcon []byte

type desktopOptions struct {
	paths      appPaths
	projectDir string
}

func parseDesktopOptions(args []string) (desktopOptions, error) {
	flags := flag.NewFlagSet("api-proxy-desktop", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dataDir := flags.String("data-dir", "", "运行数据根目录，其下保存 .config")
	projectDir := flags.String("project-dir", "", "项目 Skills 的父目录")
	if err := flags.Parse(args); err != nil {
		return desktopOptions{}, fmt.Errorf("桌面启动参数错误（支持 --data-dir、--project-dir）: %w", err)
	}
	if flags.NArg() != 0 {
		return desktopOptions{}, errors.New("桌面启动不接受位置参数；请使用 --data-dir 或 --project-dir")
	}
	if *dataDir == "" {
		executable, err := os.Executable()
		if err != nil {
			return desktopOptions{}, fmt.Errorf("读取桌面程序路径失败: %w", err)
		}
		*dataDir = desktopDataRoot(executable, runtime.GOOS)
	}
	root, err := filepath.Abs(*dataDir)
	if err != nil {
		return desktopOptions{}, fmt.Errorf("解析数据目录失败: %w", err)
	}
	if *projectDir == "" {
		*projectDir = root
	}
	project, err := filepath.Abs(*projectDir)
	if err != nil {
		return desktopOptions{}, fmt.Errorf("解析项目目录失败: %w", err)
	}
	return desktopOptions{paths: appPaths{root: root}, projectDir: project}, nil
}

// 便携数据跟随程序目录，不依赖工作目录；macOS 数据放在 .app 旁，避免修改应用包。
func desktopDataRoot(executable, goos string) string {
	directory := filepath.Dir(executable)
	if goos == "darwin" && filepath.Base(directory) == "MacOS" {
		contents := filepath.Dir(directory)
		bundle := filepath.Dir(contents)
		if filepath.Base(contents) == "Contents" && strings.HasSuffix(filepath.Base(bundle), ".app") {
			return filepath.Dir(bundle)
		}
	}
	return directory
}

// desktopApp 的后台 worker 是网关的唯一所有者；Wails 退出时必须等待它完成清理。
type desktopApp struct {
	app        *application.App
	window     *application.WebviewWindow
	trayMenu   *application.Menu
	options    desktopOptions
	startupErr error
	started    chan struct{}
	done       chan struct{}
	cancel     context.CancelFunc
	startOnce  sync.Once
	quit       atomic.Bool
	drained    atomic.Bool
	tray       atomic.Bool
	mu         sync.Mutex
	address    string
	err        error
}

func launchApplication() error {
	options, startupErr := parseDesktopOptions(os.Args[1:])
	if startupErr != nil {
		// 参数错误仍在窗口中展示，但 WebView 和单实例锁使用有效的默认数据目录。
		defaults, err := parseDesktopOptions(nil)
		if err != nil {
			return errors.Join(startupErr, err)
		}
		options = defaults
	}
	d := &desktopApp{options: options, startupErr: startupErr, started: make(chan struct{}), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	defer cancel()
	identityRoot := options.paths.root
	if runtime.GOOS == "windows" {
		identityRoot = strings.ToLower(identityRoot)
	}
	identity := sha256.Sum256([]byte(identityRoot))
	d.app = application.New(application.Options{
		Name: "API Proxy", Description: "LLM API 代理网关", Icon: desktopIcon,
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true, WebviewUserDataPath: options.paths.resolve(".config/webview")},
		Linux:   application.LinuxOptions{DisableQuitOnLastWindowClosed: true, ProgramName: "api-proxy-desktop"},
		Mac:     application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               fmt.Sprintf("com.insistanan.api-proxy.%x", identity[:12]),
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { d.showWindow() },
		},
		ShouldQuit:        d.shouldQuit,
		OnShutdown:        func() { d.quit.Store(true); cancel(); <-d.done },
		ErrorHandler:      d.frameworkError,
		RawMessageHandler: d.handleRawMessage,
	})
	// application.New 已完成单实例判定，此后才创建网关资源或启动后台 worker。
	window := d.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "management", Title: "API Proxy", Width: 1280, Height: 860, MinWidth: 900, MinHeight: 600,
		HTML: desktopPage("正在启动 API Proxy", "正在加载配置并启动本地代理服务…"), JS: desktopLinkScript,
	})
	d.mu.Lock()
	d.window = window
	d.mu.Unlock()
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if d.drained.Load() {
			return
		}
		event.Cancel()
		if !d.quit.Load() && d.tray.Load() {
			window.Hide()
		} else if !d.quit.Load() {
			d.app.Quit()
		}
	})
	d.installMenu()
	d.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		d.startOnce.Do(func() { close(d.started) })
	})
	go d.runGateway(ctx)
	runErr := d.app.Run()
	cancel()
	<-d.done
	d.mu.Lock()
	defer d.mu.Unlock()
	return errors.Join(runErr, d.err)
}

// 第一次退出只发出取消信号；保持界面线程运行，待后台排空后再执行真正退出。
func (d *desktopApp) shouldQuit() bool {
	if d.drained.Load() {
		return true
	}
	if d.quit.CompareAndSwap(false, true) {
		d.cancel()
		go func() {
			<-d.done
			application.InvokeAsync(func() {
				d.mu.Lock()
				err := d.err
				d.mu.Unlock()
				if err != nil {
					d.app.Dialog.Error().SetTitle("API Proxy 退出时报告错误").SetMessage(err.Error()).Show()
				}
				d.drained.Store(true)
				d.app.Quit()
			})
		}()
	}
	return false
}

func desktopPage(title, message string) string {
	return `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>API Proxy</title>
<style>body{margin:0;background:#17191d;color:#eee;font:16px system-ui;display:grid;min-height:100vh;place-items:center}main{max-width:760px;padding:40px}h1{font-size:26px}pre{white-space:pre-wrap;line-height:1.8;color:#c9cbd1}</style>
<main><h1>` + html.EscapeString(title) + `</h1><pre>` + html.EscapeString(message) + `</pre></main></html>`
}

func (d *desktopApp) showWindow() {
	application.InvokeAsync(func() {
		d.mu.Lock()
		w := d.window
		d.mu.Unlock()
		if w != nil && !d.quit.Load() {
			w.Show()
			w.UnMinimise()
			w.Focus()
		}
	})
}

func (d *desktopApp) reportError(err error) {
	d.mu.Lock()
	d.err = errors.Join(d.err, err)
	d.mu.Unlock()
	log.Printf("[Desktop-Error] %v", err)
	// worker 只投递界面更新，不能等待主线程，否则 OnShutdown 的等待会死锁。
	application.InvokeAsync(func() {
		d.mu.Lock()
		w := d.window
		d.mu.Unlock()
		if w != nil && !d.quit.Load() {
			w.SetHTML(desktopPage("API Proxy 发生错误", err.Error()+"\n请关闭窗口或通过菜单退出后，修正配置并重新启动。"))
			d.showWindow()
		}
	})
}

func (d *desktopApp) frameworkError(err error) {
	if strings.Contains(strings.ToLower(err.Error()), "systray") {
		d.tray.Store(false)
		log.Printf("[Desktop-Tray] 托盘不可用: %v", err)
		application.InvokeAsync(func() {
			if !d.quit.Load() {
				d.showWindow()
				d.app.Dialog.Error().SetTitle("系统托盘不可用").SetMessage("代理服务仍在运行，关闭管理窗口将退出应用。\n" + err.Error()).Show()
			}
		})
		return
	}
	d.reportError(err)
}

func (d *desktopApp) installMenu() {
	menu := application.NewMenu()
	menu.Add("打开管理窗口").OnClick(func(*application.Context) { d.showWindow() })
	menu.Add("在浏览器打开").OnClick(func(*application.Context) {
		d.mu.Lock()
		address := d.address
		d.mu.Unlock()
		if address == "" {
			d.app.Dialog.Info().SetTitle("服务尚未就绪").SetMessage("请等待代理启动，或查看管理窗口中的错误信息。").Show()
			return
		}
		if err := d.app.Browser.OpenURL(address); err != nil {
			d.app.Dialog.Error().SetTitle("打开浏览器失败").SetMessage(err.Error()).Show()
		}
	})
	menu.AddSeparator()
	menu.Add("退出 API Proxy").SetAccelerator("CmdOrCtrl+q").OnClick(func(*application.Context) { d.app.Quit() })
	d.trayMenu = menu
	// macOS 顶层菜单需要子菜单；托盘另用原菜单，避免共享同一原生菜单实例。
	menubar := application.NewMenuFromItems(application.NewSubmenu("API Proxy", menu.Clone()))
	d.app.Menu.Set(menubar)
	d.window.SetMenu(menubar)
}

// 外链通过原生 postMessage 交给系统浏览器，不改变现有 Vue 页面和 API 调用。
const desktopLinkScript = `(function() {
if (window !== window.top || window.__apiProxyDesktopLinks) return;
window.__apiProxyDesktopLinks = true;
document.addEventListener('click', function(event) {
  const link = event.target instanceof Element ? event.target.closest('a[href]') : null;
  if (!link || link.hasAttribute('download')) return;
  const url = new URL(link.href, location.href);
  if (url.origin === location.origin && link.target !== '_blank') return;
  if (!['http:', 'https:', 'ccswitch:'].includes(url.protocol)) return;
  event.preventDefault(); window._wails.invoke('api-proxy:open-url:' + url.href);
}, true);
window.open = function(value) {
  if (value == null) return null;
  const url = new URL(String(value), location.href);
  if (!['http:', 'https:', 'ccswitch:'].includes(url.protocol)) throw new Error('不支持的外部链接协议');
  window._wails.invoke('api-proxy:open-url:' + url.href); return null;
};
})();`

func (d *desktopApp) handleRawMessage(window application.Window, message string, origin *application.OriginInfo) {
	const prefix = "api-proxy:open-url:"
	if !strings.HasPrefix(message, prefix) {
		return
	}
	d.mu.Lock()
	address, management := d.address, d.window
	d.mu.Unlock()
	if d.quit.Load() || origin == nil || window != management || !desktopSameOrigin(origin.Origin, address) ||
		(runtime.GOOS == "darwin" && !origin.IsMainFrame) ||
		(runtime.GOOS == "windows" && !desktopSameOrigin(origin.TopOrigin, address)) {
		d.app.Dialog.Error().SetTitle("无法打开外部链接").SetMessage("外部链接请求并非来自当前管理页面。").Show()
		return
	}
	target, err := url.Parse(strings.TrimPrefix(message, prefix))
	if err != nil || (target.Scheme != "http" && target.Scheme != "https" && target.Scheme != "ccswitch") || target.Host == "" || target.User != nil {
		d.app.Dialog.Error().SetTitle("无法打开外部链接").SetMessage("链接格式错误或协议不受支持。").Show()
		return
	}
	if err := d.app.Browser.OpenURL(target.String()); err != nil {
		d.app.Dialog.Error().SetTitle("打开外部链接失败").SetMessage(err.Error()).Show()
	}
}

// Wails 在 Windows/Linux 传入完整页面 URL，在 macOS 传入 origin；统一比较协议与主机。
// Linux 原生消息不提供 frame 身份，注入脚本只安装在主页面，管理页不得加载不可信 iframe。
func desktopSameOrigin(source, address string) bool {
	actual, err := url.Parse(source)
	if err != nil || actual.User != nil {
		return false
	}
	expected, err := url.Parse(address)
	return err == nil && expected.Scheme == "http" && expected.Host != "" && actual.Scheme == expected.Scheme && actual.Host == expected.Host
}

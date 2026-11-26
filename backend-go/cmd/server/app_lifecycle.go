// app_lifecycle.go 管理启动摘要、HTTP 排空与分阶段停机。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"insistanan/api-proxy/internal/infra/config"
)

// logStartupInfo 输出紧凑的启动摘要：地址与构建信息一次展示。
func (a *app) logStartupInfo() {
	setConsoleTitle("API Proxy " + Version)
	build := BuildTime
	if GitCommit != "unknown" {
		build += " · " + GitCommit
	}
	if build != "unknown" {
		fmt.Printf("\nAPI Proxy %s · %s · %s\n", Version, a.envCfg.Env, build)
	} else {
		fmt.Printf("\nAPI Proxy %s · %s\n", Version, a.envCfg.Env)
	}
	fmt.Printf("  管理台  http://localhost:%d\n", a.envCfg.Port)
	fmt.Printf("  API     http://localhost:%d/v1\n", a.envCfg.Port)
	fmt.Printf("  健康    http://localhost:%d/health\n", a.envCfg.Port)
	// 检查是否使用默认密码，给予提示
	if a.envCfg.ProxyAccessKey == "your-proxy-access-key" {
		fmt.Printf("  警告    访问密钥仍是默认值，建议在 .env 中修改\n")
	}
	fmt.Println()
}

// logInitf 输出装配细节。常规启动只保留摘要，LOG_LEVEL=debug 时才展开。
func logInitf(envCfg *config.EnvConfig, format string, args ...any) {
	if envCfg.LogLevel == "debug" {
		log.Printf(format, args...)
	}
}

// appCleanup 记录释放责任，同一阶段内按登记顺序逆序执行。
type appCleanup struct {
	name  string
	close func() error
}

func (a *app) registerCleanup(name string, closeResource func() error) {
	a.cleanups = append(a.cleanups, appCleanup{name: name, close: closeResource})
}

func (a *app) registerWorker(name string, stopWorker func() error) {
	a.workers = append(a.workers, appCleanup{name: name, close: stopWorker})
}

// shutdown 先停止全部后台生产者，再关闭存储；启动失败也遵循同一依赖约束。
// 每个资源只清理一次，一项失败不阻断其余清理。
func (a *app) shutdown() error {
	a.shutdownOnce.Do(func() {
		for _, phase := range [][]appCleanup{a.workers, a.cleanups} {
			for i := len(phase) - 1; i >= 0; i-- {
				cleanup := phase[i]
				log.Printf("[Server-Shutdown] 正在关闭%s", cleanup.name)
				if err := cleanup.close(); err != nil {
					a.shutdownErr = errors.Join(a.shutdownErr, fmt.Errorf("关闭%s失败: %w", cleanup.name, err))
				}
			}
		}
	})
	return a.shutdownErr
}

// serveUntilStopped 由调用方等待信号和监听结果；监听失败也会返回装配层释放资源。
func serveUntilStopped(ctx context.Context, srv *http.Server, shutdownTimeout time.Duration) error {
	// Shutdown 超时后 Close 只取消连接，不等待 handler；存储仍须等请求实际退出再关闭。
	handler := srv.Handler
	if handler == nil {
		handler = http.DefaultServeMux
	}
	var requestMu sync.Mutex
	var requests sync.WaitGroup
	accepting := true
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMu.Lock()
		if !accepting {
			requestMu.Unlock()
			http.Error(w, "服务器正在关闭", http.StatusServiceUnavailable)
			return
		}
		requests.Add(1)
		requestMu.Unlock()
		defer requests.Done()
		handler.ServeHTTP(w, r)
	})
	defer func() {
		requestMu.Lock()
		accepting = false
		requestMu.Unlock()
		requests.Wait()
	}()
	listenResult := make(chan error, 1)
	go func() { listenResult <- srv.ListenAndServe() }()
	select {
	case err := <-listenResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("服务器监听失败: %w", err)
	case <-ctx.Done():
	}

	log.Println("[Server-Shutdown] 收到关闭信号，正在优雅关闭服务器...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		// 超时后关闭连接，触发请求 context 取消；调用方仍须处理关闭错误。
		closeErr := srv.Close()
		<-listenResult
		return errors.Join(fmt.Errorf("服务器优雅关闭失败: %w", err), closeErr)
	}
	if err := <-listenResult; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("服务器监听失败: %w", err)
	}
	log.Println("[Server-Shutdown] 服务器已安全关闭")
	return nil
}

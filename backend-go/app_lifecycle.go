// app_lifecycle.go 进程生命周期（R4-b 按初始化职责分文件）：
// 启动横幅、后台模式跟随、性能画像同步与逆序停机。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/metrics"
	"github.com/insistanan/api-proxy/internal/sensitive"
)

// logStartupInfo 输出启动横幅与服务地址信息。
func (a *app) logStartupInfo() {
	fmt.Printf("\n[Server-Startup] API代理服务器正在启动\n")
	fmt.Printf("[Server-Info] 版本: %s\n", Version)
	if BuildTime != "unknown" {
		fmt.Printf("[Server-Info] 构建时间: %s\n", BuildTime)
	}
	if GitCommit != "unknown" {
		fmt.Printf("[Server-Info] Git提交: %s\n", GitCommit)
	}
	fmt.Printf("[Server-Info] 管理界面: http://localhost:%d\n", a.envCfg.Port)
	fmt.Printf("[Server-Info] API 地址: http://localhost:%d/v1\n", a.envCfg.Port)
	fmt.Printf("[Server-Info] Messages: POST /v1/messages\n")
	fmt.Printf("[Server-Info] Responses: POST /v1/responses\n")
	fmt.Printf("[Server-Info] OpenAI Chat: POST /v1/chat/completions\n")
	fmt.Printf("[Server-Info] OpenAI Images: POST /v1/images/{generations|edits|variations}\n")
	fmt.Printf("[Server-Info] Gemini API: POST /v1beta/models/{model}:generateContent\n")
	fmt.Printf("[Server-Info] Gemini API: POST /v1beta/models/{model}:streamGenerateContent\n")
	fmt.Printf("[Server-Info] 健康检查: GET /health\n")
	fmt.Printf("[Server-Info] 环境: %s\n", a.envCfg.Env)
	// 检查是否使用默认密码，给予提示
	if a.envCfg.ProxyAccessKey == "your-proxy-access-key" {
		fmt.Printf("[Server-Warn] 访问密钥: your-proxy-access-key (默认值，建议通过 .env 文件修改)\n")
	}
	fmt.Printf("\n")
}

// watchMappingStoreMode 跟随 sensitiveData.mode 的热切换：
// mask → 为新请求注入持久库；mask 之外 → 新请求停止使用持久映射。
// 已开始的请求保留自己的存储与密钥快照，直到请求结束。
func (a *app) watchMappingStoreMode() {
	defer close(a.mappingWatchDone)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	retentionTicker := time.NewTicker(time.Minute)
	defer retentionTicker.Stop()
	for {
		select {
		case <-a.mappingWatchStop:
			return
		case <-retentionTicker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := a.blockedStore.Purge(ctx); err != nil {
				log.Printf("[ContentSafety-Purge] 清理内容安全历史失败: %v", err)
			}
			cancel()
			ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			if err := a.gateStore.Purge(ctx); err != nil {
				log.Printf("[Gate-Purge] 清理命令确认历史失败: %v", err)
			}
			cancel()
			continue
		case <-ticker.C:
		}
		mode := a.cfgManager.GetSettings().ContentSafety.SensitiveData.Mode
		enabled := sensitive.PersistentMappingStore() != nil
		want := mode == config.ContentSafetyModeMask
		if want == enabled {
			continue
		}
		if want {
			sensitive.SetPersistentMappingStore(a.mappingStore)
			log.Printf("[SensitiveData-Watch] 已切换到可逆脱敏，持久映射库启用")
		} else {
			sensitive.SetPersistentMappingStore(nil)
			log.Printf("[SensitiveData-Watch] 已离开可逆脱敏，新请求停用持久映射，已有请求沿用原快照")
		}
	}
}

// appCleanup 记录资源的释放责任；清理顺序由实际构造时的登记顺序决定。
type appCleanup struct {
	name  string
	close func() error
}

func (a *app) registerCleanup(name string, closeResource func() error) {
	a.cleanups = append(a.cleanups, appCleanup{name: name, close: closeResource})
}

// shutdown 供启动失败和正常关闭共用。每个资源只清理一次；一项失败不阻断其余清理。
func (a *app) shutdown() error {
	if a == nil {
		return nil
	}
	a.shutdownOnce.Do(func() {
		for i := len(a.cleanups) - 1; i >= 0; i-- {
			cleanup := a.cleanups[i]
			if err := cleanup.close(); err != nil {
				a.shutdownErr = errors.Join(a.shutdownErr, fmt.Errorf("关闭%s失败: %w", cleanup.name, err))
			}
		}
	})
	return a.shutdownErr
}

func (a *app) newHTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           a.engine,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// serveUntilStopped 保留服务版入口；桌面版先绑定 listener，成功后才显示管理页面。
func serveUntilStopped(ctx context.Context, srv *http.Server, shutdownTimeout time.Duration) error {
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("服务器监听失败: %w", err)
	}
	return serveListenerUntilStopped(ctx, srv, listener, shutdownTimeout)
}

// serveListenerUntilStopped 拥有 listener，返回前等待请求退出，调用方随后关闭应用资源。
func serveListenerUntilStopped(ctx context.Context, srv *http.Server, listener net.Listener, shutdownTimeout time.Duration) error {
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
	go func() { listenResult <- srv.Serve(listener) }()
	select {
	case err := <-listenResult:
		// Serve 异常返回仍可能留下活跃连接，先取消连接再等 handler，避免清理永久等待。
		closeErr := srv.Close()
		if errors.Is(err, http.ErrServerClosed) {
			return closeErr
		}
		return errors.Join(fmt.Errorf("服务器监听失败: %w", err), closeErr)
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

func syncUpstreamProfiles(mm *metrics.MetricsManager, pm *metrics.ProfileManager, upstream *config.UpstreamConfig, channelIdx int) {
	if mm == nil || pm == nil || upstream == nil || len(upstream.APIKeys) == 0 {
		return
	}

	models := make(map[string]struct{})
	if model := strings.TrimSpace(upstream.DefaultModel); model != "" {
		models[model] = struct{}{}
	}
	for sourceModel, targetModels := range upstream.ModelMapping {
		if source := strings.TrimSpace(sourceModel); source != "" && source != "*" {
			models[source] = struct{}{}
		}
		for _, targetModel := range targetModels {
			if target := strings.TrimSpace(targetModel); target != "" {
				models[target] = struct{}{}
			}
		}
	}
	if len(models) == 0 {
		return
	}

	for _, baseURL := range upstream.GetAllBaseURLs() {
		for model := range models {
			mm.SyncToProfile(pm, baseURL, upstream.APIKeys, model, channelIdx)
		}
	}
}

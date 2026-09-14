// app_lifecycle.go 进程生命周期（R4-b 按初始化职责分文件）：
// 启动横幅、后台模式跟随、性能画像同步与逆序停机。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/metrics"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
)

// logStartupInfo 输出启动横幅与服务地址信息。
func (a *app) logStartupInfo() {
	fmt.Printf("\n[Server-Startup] API代理服务器已启动\n")
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

// shutdown 按依赖逆序停止后台组件并关闭持久化存储。
// 顺序约束：必须先停调度器（数据生产者）再关指标存储，
// 避免关闭期间后台循环与存储 Close 产生写并发。
func (a *app) shutdown() {
	if a.mappingWatchStop != nil {
		close(a.mappingWatchStop)
		<-a.mappingWatchDone
	}
	sensitive.SetPersistentMissCallback(nil)
	sensitive.SetPersistentMappingStore(nil)
	a.channelScheduler.Stop()

	if a.evalService != nil {
		a.evalService.Stop()
	}

	if a.metricsStore != nil {
		if err := a.metricsStore.Close(); err != nil {
			log.Printf("[Metrics-Shutdown] 警告: 关闭指标存储时发生错误: %v", err)
		} else {
			log.Println("[Metrics-Shutdown] 指标存储已安全关闭")
		}
	}

	a.conversationRegistry.Stop()
	a.sessionManager.Stop()
	if a.assistantService != nil {
		if err := a.assistantService.Close(); err != nil {
			log.Printf("[Assistant-Shutdown] 关闭 AI 配置助手存储失败: %v", err)
		}
	}
	if err := a.blockedStore.Close(); err != nil {
		log.Printf("[ContentSafety-Shutdown] 关闭拦截记录存储失败: %v", err)
	}
	if a.gateStore != nil {
		if err := a.gateStore.Close(); err != nil {
			log.Printf("[Gate-Shutdown] 关闭闸门存储失败: %v", err)
		}
	}
	if a.mappingStore != nil {
		if err := a.mappingStore.Close(); err != nil {
			log.Printf("[SensitiveData-Shutdown] 关闭持久映射库失败: %v", err)
		}
	}
	a.cfgManager.Close()
	a.requestLogStore.Close()
	if err := a.logStore.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "[Logger-Shutdown] 关闭日志存储失败: %v\n", err)
	}
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

package admin

import (
	"context"
	"github.com/insistanan/api-proxy/internal/handlers/clients"
	"github.com/insistanan/api-proxy/internal/handlers/common"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/infra/httpclient"
	"github.com/insistanan/api-proxy/internal/infra/utils"
)

// DisguiseSyncStatus 区分设置保存和远程查询，保留各客户端最后成功时间及错误。
type DisguiseSyncStatus struct {
	Syncing             bool   `json:"syncing"`
	LastAttemptAt       int64  `json:"lastAttemptAt"`
	ClaudeLastSuccessAt int64  `json:"claudeLastSuccessAt"`
	CodexLastSuccessAt  int64  `json:"codexLastSuccessAt"`
	ClaudeError         string `json:"claudeError"`
	CodexError          string `json:"codexError"`
}

// DisguiseSyncer 串行处理手动、配置变更和定时刷新；停止时取消网络请求并等待退出。
type DisguiseSyncer struct {
	cfgManager *config.ConfigManager
	ctx        context.Context
	cancel     context.CancelFunc
	refresh    chan struct{}
	done       chan struct{}
	mu         sync.RWMutex
	status     DisguiseSyncStatus
}

func StartDisguiseAutoSync(cfgManager *config.ConfigManager) *DisguiseSyncer {
	ctx, cancel := context.WithCancel(context.Background())
	syncer := &DisguiseSyncer{
		cfgManager: cfgManager,
		ctx:        ctx,
		cancel:     cancel,
		refresh:    make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
	go syncer.run()
	return syncer
}

func (s *DisguiseSyncer) Stop() {
	s.cancel()
	<-s.done
}

func (s *DisguiseSyncer) Status() DisguiseSyncStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// RequestSync 手动查询只更新 latest 观测缓存，不改变自动跟随开关或自定义版本。
func (s *DisguiseSyncer) RequestSync() bool {
	if s.ctx.Err() != nil {
		return false
	}
	select {
	case s.refresh <- struct{}{}:
	default:
	}
	return true
}

func (s *DisguiseSyncer) run() {
	defer close(s.done)
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	// 配置管理器初次发布时已经应用本地版本，并排入首个变更事件。
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.refresh:
			s.syncOnce(true)
		case <-s.cfgManager.SettingsChanged():
			s.syncOnce(false)
		case <-ticker.C:
			s.syncOnce(false)
		}
	}
}

func (s *DisguiseSyncer) syncOnce(manual bool) {
	if s.ctx.Err() != nil || (!manual && !s.cfgManager.GetSettings().Disguise.AutoUpdateFromRegistry) {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	s.mu.Lock()
	s.status.Syncing = true
	s.status.LastAttemptAt = time.Now().UnixMilli()
	s.mu.Unlock()

	type fetchResult struct {
		claude bool
		ver    string
		err    string
	}
	results := make(chan fetchResult, 2)
	go func() {
		ver, err := clients.FetchNpmLatestVersionUsing(ctx, "@anthropic-ai/claude-code", s.registryGet)
		results <- fetchResult{claude: true, ver: ver, err: err}
	}()
	go func() {
		ver, err := clients.FetchNpmLatestVersionUsing(ctx, "@openai/codex", s.registryGet)
		results <- fetchResult{ver: ver, err: err}
	}()

	var claude, codex fetchResult
	// HTTP 请求继承应用 context；必须收完两个结果，Stop 才能完整等待后台任务。
	for i := 0; i < 2; i++ {
		result := <-results
		if result.claude {
			claude = result
		} else {
			codex = result
		}
	}
	utils.SetDisguiseLatestVersions(claude.ver, codex.ver)
	now := time.Now().UnixMilli()
	s.mu.Lock()
	s.status.Syncing = false
	s.status.ClaudeError = claude.err
	s.status.CodexError = codex.err
	if claude.err == "" {
		s.status.ClaudeLastSuccessAt = now
	}
	if codex.err == "" {
		s.status.CodexLastSuccessAt = now
	}
	s.mu.Unlock()
	if claude.err != "" || codex.err != "" {
		log.Printf("[Disguise-Sync] 版本查询失败: Claude Code=%s, Codex=%s", claude.err, codex.err)
	} else {
		log.Printf("[Disguise-Sync] 官方最新版本已查询: Claude Code=%s, Codex=%s", claude.ver, codex.ver)
	}
}

// registryGet 与推理请求共用网络出口，遵守设置页的全局上游代理。
func (s *DisguiseSyncer) registryGet(ctx context.Context, url string) ([]byte, int, error) {
	proxyURL, err := s.cfgManager.ResolveUpstreamProxyURL(&config.UpstreamConfig{ProxyMode: config.ProxyModeInherit})
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "api-proxy-disguise-sync")
	req.Header.Set("Accept", "application/json")
	resp, err := httpclient.Send(req, httpclient.SendOptions{Timeout: 10 * time.Second, ProxyURL: proxyURL})
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := common.ReadLimitedBody(resp.Body, 1<<20)
	return body, resp.StatusCode, err
}

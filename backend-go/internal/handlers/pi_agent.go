package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/piagent"
)

// PiAgentAPI 聚合 pi-agent 配置管理的互斥锁与配置存储，
// 由 main.go 构造并注入路由，替代包级全局状态。
type PiAgentAPI struct {
	mu    sync.Mutex
	store *piagent.Store
}

// NewPiAgentAPI 创建 pi-agent 配置管理 API。
// 备份目录在构造时显式解析，失败直接报错，不做静默兜底。
func NewPiAgentAPI() (*PiAgentAPI, error) {
	backupDir, err := piagent.DefaultBackupDir()
	if err != nil {
		return nil, fmt.Errorf("解析 pi-agent 备份目录失败: %w", err)
	}
	return &PiAgentAPI{store: piagent.NewStore(backupDir)}, nil
}

// piAgentError 将底层错误映射为 HTTP 状态码。
func piAgentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, piagent.ErrLocked):
		c.JSON(http.StatusLocked, gin.H{"error": err.Error()})
	case errors.Is(err, piagent.ErrRevisionRequired):
		c.JSON(http.StatusPreconditionRequired, gin.H{"error": err.Error(), "code": "revision_required"})
	case errors.Is(err, piagent.ErrRevisionMismatch):
		c.JSON(http.StatusConflict, gin.H{"error": "配置文件已被外部修改，请刷新后重试", "code": "revision_conflict"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// ============== 状态 ==============

// GetPiAgentStatus 返回 pi-agent 配置目录及三个文件的存在、读写状态。
func (p *PiAgentAPI) Status() gin.HandlerFunc {
	return func(c *gin.Context) {
		p.mu.Lock()
		defer p.mu.Unlock()
		status, err := piagent.InspectStatus()
		if err != nil {
			piAgentError(c, err)
			return
		}
		c.JSON(http.StatusOK, status)
	}
}

// ============== Provider CRUD ==============

type piAgentModelResponse struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name,omitempty"`
	API              string                 `json:"api,omitempty"`
	BaseURL          string                 `json:"baseUrl,omitempty"`
	Reasoning        *bool                  `json:"reasoning,omitempty"`
	ThinkingLevelMap map[string]*string     `json:"thinkingLevelMap,omitempty"`
	Input            []string               `json:"input,omitempty"`
	ContextWindow    *int64                 `json:"contextWindow,omitempty"`
	MaxTokens        *int64                 `json:"maxTokens,omitempty"`
	Cost             *piagent.ModelCost     `json:"cost,omitempty"`
	Headers          map[string]string      `json:"headers,omitempty"`
	Compat           map[string]interface{} `json:"compat,omitempty"`
}

type piAgentProviderResponse struct {
	ID             string                     `json:"id"`
	Name           string                     `json:"name,omitempty"`
	API            string                     `json:"api,omitempty"`
	BaseURL        string                     `json:"baseUrl,omitempty"`
	AuthHeader     *bool                      `json:"authHeader,omitempty"`
	Headers        map[string]string          `json:"headers,omitempty"`
	Compat         map[string]interface{}     `json:"compat,omitempty"`
	Models         []piAgentModelResponse     `json:"models,omitempty"`
	ModelOverrides map[string]json.RawMessage `json:"modelOverrides,omitempty"`
	HasOAuth       bool                       `json:"hasOAuth"`
	APIKeyMasked   string                     `json:"apiKeyMasked"`
	APIKeyPresent  bool                       `json:"apiKeyPresent"`
}

// ListPiAgentProviders 返回 provider 列表（含脱敏的凭据状态）。
func (p *PiAgentAPI) ListProviders() gin.HandlerFunc {
	return func(c *gin.Context) {
		p.mu.Lock()
		defer p.mu.Unlock()
		store := p.store
		providers, root, raw, _, err := store.ReadProviders()
		if err != nil {
			piAgentError(c, err)
			return
		}
		credentialInfo, err := p.credentialInfo()
		if err != nil {
			piAgentError(c, err)
			return
		}
		ids := make([]string, 0, len(providers))
		for id := range providers {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		response := make([]piAgentProviderResponse, 0, len(ids))
		for _, id := range ids {
			response = append(response, piAgentProviderResponseView(id, providers[id], credentialInfo[id]))
		}
		c.JSON(http.StatusOK, gin.H{
			"revision":  sha256HexOrEmpty(raw),
			"providers": response,
			"rawExists": len(root) > 0,
		})
	}
}

// GetPiAgentProvider 返回单个 provider 详情。
func (p *PiAgentAPI) GetProvider() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		p.mu.Lock()
		defer p.mu.Unlock()
		store := p.store
		providers, _, raw, _, err := store.ReadProviders()
		if err != nil {
			piAgentError(c, err)
			return
		}
		provider, exists := providers[id]
		if !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider 不存在"})
			return
		}
		credentialInfo, err := p.credentialInfo()
		if err != nil {
			piAgentError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"revision": sha256HexOrEmpty(raw),
			"provider": piAgentProviderResponseView(id, provider, credentialInfo[id]),
		})
	}
}

type createPiAgentProviderRequest struct {
	ID       string                 `json:"id"`
	Revision string                 `json:"revision"`
	Provider piagent.ProviderConfig `json:"provider"`
}

type updatePiAgentProviderRequest struct {
	Revision string                 `json:"revision"`
	Provider piagent.ProviderConfig `json:"provider"`
}

type deletePiAgentProviderRequest struct {
	Revision string `json:"revision"`
}

// CreatePiAgentProvider 新增 provider。provider 对象中的 apiKey 和 apiKey 字段都会写入 models.json。
func (p *PiAgentAPI) CreateProvider() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createPiAgentProviderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体无效"})
			return
		}
		id := piagent.NormalizeID(req.ID)
		if err := piagent.ValidateProvider(id, req.Provider); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		store := p.store
		release, err := store.LockAndCheck()
		if err != nil {
			piAgentError(c, err)
			return
		}
		defer release()

		providers, _, raw, _, err := store.ReadProviders()
		if err != nil {
			piAgentError(c, err)
			return
		}
		if req.Revision != "" && piagent.SHA256Hex(raw) != req.Revision {
			piAgentError(c, piagent.ErrRevisionMismatch)
			return
		}
		if _, exists := providers[id]; exists {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("provider 已存在: %s", id), "code": "provider_exists"})
			return
		}
		providers[id] = req.Provider
		result, err := store.SaveProviders(providers, req.Revision)
		if err != nil {
			piAgentError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "revision": result.Revision})
	}
}

// UpdatePiAgentProvider 更新单个 provider。provider.APIKey 写入 models.json。
func (p *PiAgentAPI) UpdateProvider() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		var req updatePiAgentProviderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体无效"})
			return
		}
		if err := piagent.ValidateProvider(id, req.Provider); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		store := p.store
		release, err := store.LockAndCheck()
		if err != nil {
			piAgentError(c, err)
			return
		}
		defer release()

		providers, _, _, _, err := store.ReadProviders()
		if err != nil {
			piAgentError(c, err)
			return
		}
		if _, exists := providers[id]; !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider 不存在"})
			return
		}
		providers[id] = piagent.MergeProviderUpdate(providers[id], req.Provider)
		result, err := store.SaveProviders(providers, req.Revision)
		if err != nil {
			piAgentError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "revision": result.Revision})
	}
}

// DeletePiAgentProvider 删除 provider。被 settings.json 默认模型引用的 provider 返回明确冲突。
func (p *PiAgentAPI) DeleteProvider() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		var req deletePiAgentProviderRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体无效"})
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		store := p.store
		release, err := store.LockAndCheck()
		if err != nil {
			piAgentError(c, err)
			return
		}
		defer release()

		providers, _, _, _, err := store.ReadProviders()
		if err != nil {
			piAgentError(c, err)
			return
		}
		if _, exists := providers[id]; !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider 不存在"})
			return
		}
		// 删除被默认模型引用的 provider 时返回明确冲突
		reason, conflictErr := p.defaultProviderConflict(id)
		if conflictErr != nil {
			piAgentError(c, conflictErr)
			return
		}
		if reason != "" {
			c.JSON(http.StatusConflict, gin.H{"error": reason, "code": "provider_in_use"})
			return
		}
		result, err := store.DeleteProvider(id, req.Revision)
		if err != nil {
			piAgentError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "revision": result.Revision})
	}
}

// piAgentDefaultProviderConflict 检查 provider 是否被 settings.json 默认模型引用。
func (p *PiAgentAPI) defaultProviderConflict(providerID string) (string, error) {
	store := p.store
	settings, _, _, _, err := store.ReadModelSettings()
	if err != nil {
		return "", err
	}
	defaultModel := strings.TrimSpace(settings.DefaultModel)
	if settings.DefaultProvider == providerID || defaultModel == providerID || strings.HasPrefix(defaultModel, providerID+"/") {
		return fmt.Sprintf("provider %s 是 settings.json 中的默认供应商，请先修改默认模型设置", providerID), nil
	}
	return "", nil
}

// ValidateProvider 只校验 provider 配置，不落盘。
func (p *PiAgentAPI) ValidateProvider() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			ID       string                 `json:"id"`
			Provider piagent.ProviderConfig `json:"provider"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"valid": false, "errors": []string{"请求体无效"}})
			return
		}
		id := piagent.NormalizeID(req.ID)
		if err := piagent.ValidateProvider(id, req.Provider); err != nil {
			c.JSON(http.StatusOK, gin.H{"valid": false, "errors": []string{err.Error()}})
			return
		}
		c.JSON(http.StatusOK, gin.H{"valid": true, "errors": []string{}})
	}
}

// ============== 模型发现与连通性测试 ==============

type piAgentProbeRequest struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	API     string `json:"api"`
}

// DiscoverPiAgentModels 从 provider 的 baseUrl 探测可用模型候选，不自动保存。
func (p *PiAgentAPI) DiscoverModels() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		var req piAgentProbeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体无效"})
			return
		}
		store := p.store
		providers, _, _, _, err := store.ReadProviders()
		if err != nil {
			piAgentError(c, err)
			return
		}
		provider, exists := providers[id]
		if !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider 不存在"})
			return
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		if baseURL == "" {
			baseURL = strings.TrimSpace(provider.BaseURL)
		}
		if baseURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 Base URL"})
			return
		}
		protocol := strings.TrimSpace(req.API)
		if protocol == "" {
			protocol = provider.API
		}
		if protocol != "" && protocol != "openai-completions" && protocol != "openai-responses" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "当前仅支持 OpenAI 兼容协议的模型发现"})
			return
		}
		apiKey := req.APIKey
		if apiKey == "" {
			apiKey = p.apiKeyFor(id)
		}
		models, method, err := probeOpenAIModels(baseURL, apiKey)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("模型探测失败: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "models": models, "method": method})
	}
}

// TestPiAgentProvider 对 provider 执行连通性测试（HTTP(S)，限制超时/重定向/响应大小）。
func (p *PiAgentAPI) TestProvider() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		var req piAgentProbeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体无效"})
			return
		}
		store := p.store
		providers, _, _, _, err := store.ReadProviders()
		if err != nil {
			piAgentError(c, err)
			return
		}
		provider, exists := providers[id]
		if !exists {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider 不存在"})
			return
		}
		baseURL := strings.TrimSpace(req.BaseURL)
		if baseURL == "" {
			baseURL = strings.TrimSpace(provider.BaseURL)
		}
		if baseURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 Base URL"})
			return
		}
		apiKey := req.APIKey
		if apiKey == "" {
			apiKey = p.apiKeyFor(id)
		}

		start := time.Now()
		statusCode, err := probeConnectivity(baseURL, apiKey)
		latencyMs := time.Since(start).Milliseconds()
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "latencyMs": latencyMs, "error": err.Error()})
			return
		}
		response := gin.H{"success": true, "latencyMs": latencyMs, "statusCode": statusCode}
		if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
			response["success"] = false
			response["error"] = fmt.Sprintf("目标可达但鉴权失败（HTTP %d）", statusCode)
		}
		c.JSON(http.StatusOK, response)
	}
}

// probeOpenAIModels 请求 OpenAI 兼容的 /models 端点并返回模型 ID 列表。
func probeOpenAIModels(baseURL, apiKey string) ([]string, string, error) {
	modelURL, err := appendURLPath(baseURL, "/models")
	if err != nil {
		return nil, "", err
	}
	endpoint, err := validateOutboundURL(modelURL)
	if err != nil {
		return nil, "", err
	}
	client := piAgentHTTPClient()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("上游返回 HTTP %d", response.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", fmt.Errorf("响应不是 OpenAI /models 格式: %w", err)
	}
	models := make([]string, 0, len(payload.Data))
	seen := make(map[string]struct{}, len(payload.Data))
	for _, entry := range payload.Data {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	sort.Strings(models)
	if len(models) == 0 {
		return nil, "", errors.New("上游未返回任何模型")
	}
	return models, "openai-completions", nil
}

// probeConnectivity 对 baseUrl 发起受限的 HTTP GET 连通性探测。
func probeConnectivity(baseURL, apiKey string) (int, error) {
	endpoint, err := validateOutboundURL(baseURL)
	if err != nil {
		return 0, err
	}
	client := piAgentHTTPClient()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	return response.StatusCode, nil
}

// validateOutboundURL 校验出站地址仅允许 HTTP(S)，并拒绝云元数据地址（SSRF 防护）。
func validateOutboundURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("URL 无效: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("仅支持 http/https 地址")
	}
	if parsed.Hostname() == "" {
		return "", errors.New("地址缺少主机名")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("地址不能包含用户信息或片段")
	}
	host := parsed.Hostname()
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("无法解析主机 %s: %w", host, err)
	}
	for _, ip := range ips {
		if isBlockedMetadataAddress(ip) {
			return "", fmt.Errorf("禁止访问云元数据地址: %s", ip)
		}
	}
	return parsed.String(), nil
}

func appendURLPath(rawURL, suffix string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("URL 无效: %w", err)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + suffix
	return parsed.String(), nil
}

// isBlockedMetadataAddress 识别云元数据与链路本地地址。
func isBlockedMetadataAddress(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		// 169.254.0.0/16 链路本地（AWS/GCP/Azure 元数据 169.254.169.254）
		if ip4[0] == 169 && ip4[1] == 254 {
			return true
		}
		// 阿里云元数据 100.100.100.200/201
		if ip4[0] == 100 && ip4[1] == 100 && (ip4[2] == 100 || ip4[2] == 101) {
			return true
		}
		// 本地模型需要访问 localhost/局域网，不做拦截。
		return false
	}
	// fe80::/10 链路本地
	if ip.IsLinkLocalUnicast() {
		return true
	}
	// AWS IPv6 元数据 fd00:ec2::254
	if strings.EqualFold(ip.String(), "fd00:ec2::254") {
		return true
	}
	// 本地模型需要访问回环/局域网，不做拦截。
	return false
}

// piAgentHTTPClient 返回受限的 HTTP 客户端：短超时、限制重定向、不跟随代理。
func piAgentHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("重定向次数过多")
			}
			if _, err := validateOutboundURL(request.URL.String()); err != nil {
				return fmt.Errorf("重定向目标无效: %w", err)
			}
			return nil
		},
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout: 5 * time.Second,
			}).DialContext,
		},
	}
}

// piAgentAPIKeyFor 获取 provider 的 API key（仅用于探测请求，不落盘）。
// 优先 auth.json 的 credential，其次 models.json 的 provider.apiKey。
func (p *PiAgentAPI) apiKeyFor(providerID string) string {
	store := p.store
	root, _, _, err := store.ReadRawAuth()
	if err == nil {
		if raw, exists := root[providerID]; exists {
			cred := make(map[string]interface{})
			if err := json.Unmarshal(raw, &cred); err == nil {
				if key, ok := cred["key"].(string); ok && key != "" {
					return key
				}
			}
		}
	}
	if providers, _, _, _, err := store.ReadProviders(); err == nil {
		if provider, exists := providers[providerID]; exists && provider.APIKey != "" {
			return provider.APIKey
		}
	}
	return ""
}

// piAgentCredentialInfo 读取 auth.json 的脱敏凭据状态，key 为 provider ID。
func (p *PiAgentAPI) credentialInfo() (map[string]piagent.CredentialView, error) {
	store := p.store
	views, _, _, _, err := store.ReadCredentials()
	if err != nil {
		return nil, err
	}
	result := make(map[string]piagent.CredentialView, len(views))
	for _, view := range views {
		result[view.ID] = view
	}
	return result, nil
}

func sha256HexOrEmpty(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	return piagent.SHA256Hex(raw)
}

func piAgentProviderResponseView(id string, provider piagent.ProviderConfig, credential piagent.CredentialView) piAgentProviderResponse {
	models := make([]piAgentModelResponse, 0, len(provider.Models))
	for _, model := range provider.Models {
		models = append(models, piAgentModelResponse{
			ID:               model.ID,
			Name:             model.Name,
			API:              model.API,
			BaseURL:          model.BaseURL,
			Reasoning:        model.Reasoning,
			ThinkingLevelMap: model.ThinkingLevelMap,
			Input:            model.Input,
			ContextWindow:    model.ContextWindow,
			MaxTokens:        model.MaxTokens,
			Cost:             model.Cost,
			Headers:          model.Headers,
			Compat:           model.Compat,
		})
	}
	// 凭据状态：auth.json 优先，fallback 到 models.json 的 provider.apiKey
	keyMasked := credential.KeyMasked
	keyPresent := credential.KeyPresent
	if !keyPresent && provider.APIKey != "" {
		keyMasked = piagent.MaskSecret(provider.APIKey)
		keyPresent = true
	}
	return piAgentProviderResponse{
		ID:             id,
		Name:           provider.Name,
		API:            provider.API,
		BaseURL:        provider.BaseURL,
		AuthHeader:     provider.AuthHeader,
		Headers:        provider.Headers,
		Compat:         provider.Compat,
		Models:         models,
		ModelOverrides: provider.ModelOverrides,
		HasOAuth:       credential.HasOAuth,
		APIKeyMasked:   keyMasked,
		APIKeyPresent:  keyPresent,
	}
}

// ============== 默认模型设置 ==============

// GetPiAgentModelSettings 返回 settings.json 中的默认模型字段。
func (p *PiAgentAPI) GetModelSettings() gin.HandlerFunc {
	return func(c *gin.Context) {
		p.mu.Lock()
		defer p.mu.Unlock()
		store := p.store
		settings, _, raw, _, err := store.ReadModelSettings()
		if err != nil {
			piAgentError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"revision": sha256HexOrEmpty(raw), "settings": settings})
	}
}

type updatePiAgentModelSettingsRequest struct {
	Revision string                    `json:"revision"`
	Settings piagent.ModelSettingsView `json:"settings"`
}

// UpdatePiAgentModelSettings 字段级更新默认模型设置。
func (p *PiAgentAPI) UpdateModelSettings() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updatePiAgentModelSettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体无效"})
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		store := p.store
		release, err := store.LockAndCheck()
		if err != nil {
			piAgentError(c, err)
			return
		}
		defer release()
		result, err := store.UpdateModelSettings(req.Settings, req.Revision)
		if err != nil {
			if strings.Contains(err.Error(), "思考等级") {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			piAgentError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "revision": result.Revision})
	}
}

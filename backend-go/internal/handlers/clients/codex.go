package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pelletier/go-toml/v2"
	"github.com/tidwall/sjson"
)

const codexAPIKeyField = "OPENAI_API_KEY"

var (
	codexSettingsMu  sync.Mutex
	codexProviderID  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	codexBaseURLLine = regexp.MustCompile(`^(\s*)base_url\s*=.*$`)
)

type codexSettingsResponse struct {
	ConfigPath           string              `json:"configPath"`
	ConfigExists         bool                `json:"configExists"`
	AuthPath             string              `json:"authPath"`
	AuthExists           bool                `json:"authExists"`
	ModelsPath           string              `json:"modelsPath"`
	ModelsExists         bool                `json:"modelsExists"`
	ActiveProvider       string              `json:"activeProvider"`
	SelectedProvider     string              `json:"selectedProvider"`
	Providers            []codexProviderView `json:"providers"`
	APIKeyMasked         string              `json:"apiKeyMasked"`
	APIKeyPresent        bool                `json:"apiKeyPresent"`
	Model                string              `json:"model"`
	ModelCatalogJSON     string              `json:"modelCatalogJson"`
	ModelReasoningEffort string              `json:"modelReasoningEffort"`
	CatalogModels        []codexModelView    `json:"catalogModels"`
	BuiltinModels        []codexModelView    `json:"builtinModels"`
	ModelsError          string              `json:"modelsError"`
	BuiltinError         string              `json:"builtinError"`
}

type codexProviderView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
}

type saveCodexSettingsRequest struct {
	Provider     string `json:"provider"`
	BaseURL      string `json:"baseUrl"`
	APIKeyAction string `json:"apiKeyAction"`
	APIKey       string `json:"apiKey"`
}

type codexConfigDocument struct {
	Model                string                         `toml:"model"`
	ModelProvider        string                         `toml:"model_provider"`
	ModelCatalogJSON     string                         `toml:"model_catalog_json"`
	ModelReasoningEffort string                         `toml:"model_reasoning_effort"`
	ModelProviders       map[string]codexProviderConfig `toml:"model_providers"`
}

type codexProviderConfig struct {
	Name    string `toml:"name"`
	BaseURL string `toml:"base_url"`
}

// GetCodexSettings 返回当前服务运行用户的 Codex CLI API Key 和 provider 配置。
func GetCodexSettings() gin.HandlerFunc {
	return func(c *gin.Context) {
		codexSettingsMu.Lock()
		defer codexSettingsMu.Unlock()

		configPath, authPath, modelsPath, err := resolveCodexPaths()
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("解析 Codex 配置目录失败: %v", err)})
			return
		}
		config, _, configExists, err := readCodexConfig(configPath)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取 Codex config.toml 失败: %v", err)})
			return
		}
		auth, _, authExists, err := readCodexAuth(authPath)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取 Codex auth.json 失败: %v", err)})
			return
		}
		// 内置清单读取失败不阻断设置页，通过 builtinError 透出；catalog 条目的 builtin
		// 标记依赖该清单，失败时全部为 false。
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		builtin, builtinErr := loadCodexBuiltinCatalog(ctx, false)
		catalogModels, modelsExists, modelsError := codexModelCatalogStatus(modelsPath, builtin)
		var builtinModels []codexModelView
		var builtinError string
		if builtinErr != nil {
			builtinModels = []codexModelView{}
			builtinError = builtinErr.Error()
		} else {
			builtinModels = builtin.builtinViews()
		}

		providers := codexProviderViews(config)
		selectedProvider := selectCodexProvider(config.ModelProvider, providers)
		var apiKey string
		if rawAPIKey, exists := auth[codexAPIKeyField]; exists {
			_ = json.Unmarshal(rawAPIKey, &apiKey)
		}
		c.JSON(200, codexSettingsResponse{
			ConfigPath:           configPath,
			ConfigExists:         configExists,
			AuthPath:             authPath,
			AuthExists:           authExists,
			ModelsPath:           modelsPath,
			ModelsExists:         modelsExists,
			ActiveProvider:       strings.TrimSpace(config.ModelProvider),
			SelectedProvider:     selectedProvider,
			Providers:            providers,
			APIKeyMasked:         maskOpenCodeSecret(apiKey),
			APIKeyPresent:        strings.TrimSpace(apiKey) != "",
			Model:                strings.TrimSpace(config.Model),
			ModelCatalogJSON:     strings.TrimSpace(config.ModelCatalogJSON),
			ModelReasoningEffort: strings.TrimSpace(config.ModelReasoningEffort),
			CatalogModels:        catalogModels,
			BuiltinModels:        builtinModels,
			ModelsError:          modelsError,
			BuiltinError:         builtinError,
		})
	}
}

// SaveCodexSettings 只更新 auth.json 的 OPENAI_API_KEY 和所选 provider 的 base_url。
func SaveCodexSettings() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req saveCodexSettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "GPT 配置请求无效"})
			return
		}
		if err := validateCodexRequest(req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		codexSettingsMu.Lock()
		defer codexSettingsMu.Unlock()

		configPath, authPath, _, err := resolveCodexPaths()
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("解析 Codex 配置目录失败: %v", err)})
			return
		}
		_, configRaw, _, err := readCodexConfig(configPath)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取 Codex config.toml 失败: %v", err)})
			return
		}
		_, authRaw, _, err := readCodexAuth(authPath)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取 Codex auth.json 失败: %v", err)})
			return
		}

		updatedConfig, err := mergeCodexProviderBaseURL(configRaw, strings.TrimSpace(req.Provider), strings.TrimSpace(req.BaseURL))
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		var updatedAuth []byte
		if req.APIKeyAction != "keep" {
			updatedAuth, err = mergeCodexAPIKey(authRaw, req)
			if err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("更新 Codex auth.json 失败: %v", err)})
				return
			}
		}

		if !bytes.Equal(configRaw, updatedConfig) {
			if err := writeCodexFile(configPath, configRaw, updatedConfig, ".codex-config-*.tmp"); err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("保存 Codex config.toml 失败: %v", err)})
				return
			}
		}
		if req.APIKeyAction != "keep" && !bytes.Equal(authRaw, updatedAuth) {
			if err := writeCodexFile(authPath, authRaw, updatedAuth, ".codex-auth-*.tmp"); err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("保存 Codex auth.json 失败: %v", err)})
				return
			}
		}
		c.JSON(200, gin.H{"success": true, "configPath": configPath, "authPath": authPath})
	}
}

func resolveCodexPaths() (string, string, string, error) {
	configDir := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", "", err
		}
		configDir = filepath.Join(home, ".codex")
	}
	configDir, err := filepath.Abs(configDir)
	if err != nil {
		return "", "", "", err
	}
	return filepath.Join(configDir, "config.toml"), filepath.Join(configDir, "auth.json"), filepath.Join(configDir, codexModelCatalogFileName), nil
}

// codexModelCatalogStatus 读取模型目录并返回条目视图（builtin 参数用于标记内置复制品）；
// 解析失败时返回空列表与错误信息，不阻断整个设置页（错误通过 modelsError 字段透出给前端）。
func codexModelCatalogStatus(modelsPath string, builtin *codexBuiltinCatalog) ([]codexModelView, bool, string) {
	catalog, err := readCodexModelCatalog(modelsPath)
	if err != nil {
		return []codexModelView{}, false, err.Error()
	}
	return catalog.modelViews(builtin), catalog.exists, ""
}

func readCodexConfig(path string) (codexConfigDocument, []byte, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return codexConfigDocument{ModelProviders: map[string]codexProviderConfig{}}, nil, false, nil
	}
	if err != nil {
		return codexConfigDocument{}, nil, false, err
	}
	var config codexConfigDocument
	if err := toml.Unmarshal(raw, &config); err != nil {
		return codexConfigDocument{}, raw, true, fmt.Errorf("TOML 格式无效: %w", err)
	}
	if config.ModelProviders == nil {
		config.ModelProviders = map[string]codexProviderConfig{}
	}
	return config, raw, true, nil
}

func readCodexAuth(path string) (map[string]json.RawMessage, []byte, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]json.RawMessage{}, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	var auth map[string]json.RawMessage
	if err := json.Unmarshal(raw, &auth); err != nil {
		return nil, raw, true, fmt.Errorf("JSON 格式无效: %w", err)
	}
	if auth == nil {
		auth = map[string]json.RawMessage{}
	}
	return auth, raw, true, nil
}

func codexProviderViews(config codexConfigDocument) []codexProviderView {
	providers := make([]codexProviderView, 0, len(config.ModelProviders))
	for id, provider := range config.ModelProviders {
		providers = append(providers, codexProviderView{ID: id, Name: provider.Name, BaseURL: provider.BaseURL})
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	return providers
}

func selectCodexProvider(active string, providers []codexProviderView) string {
	active = strings.TrimSpace(active)
	for _, provider := range providers {
		if provider.ID == active {
			return active
		}
	}
	if len(providers) == 1 {
		return providers[0].ID
	}
	return ""
}

func validateCodexRequest(req saveCodexSettingsRequest) error {
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		return errors.New("请选择或输入 provider")
	}
	if !codexProviderID.MatchString(provider) {
		return errors.New("provider 仅支持字母、数字、下划线和连字符")
	}
	if req.APIKeyAction != "" && req.APIKeyAction != "keep" && req.APIKeyAction != "replace" && req.APIKeyAction != "remove" {
		return errors.New("API Key 操作无效")
	}
	if req.APIKeyAction == "replace" && strings.TrimSpace(req.APIKey) == "" {
		return errors.New("替换 API Key 时密钥不能为空")
	}
	return nil
}

func mergeCodexAPIKey(raw []byte, req saveCodexSettingsRequest) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}\n")
	}
	var (
		updated []byte
		err     error
	)
	switch req.APIKeyAction {
	case "replace":
		updated, err = sjson.SetBytes(raw, codexAPIKeyField, strings.TrimSpace(req.APIKey))
	case "remove":
		updated, err = sjson.DeleteBytes(raw, codexAPIKeyField)
	default:
		return raw, nil
	}
	if err != nil {
		return nil, err
	}
	if len(updated) > 0 && updated[len(updated)-1] != '\n' {
		updated = append(updated, '\n')
	}
	return updated, nil
}

func mergeCodexProviderBaseURL(raw []byte, provider, baseURL string) ([]byte, error) {
	lineEnding := "\n"
	if bytes.Contains(raw, []byte("\r\n")) {
		lineEnding = "\r\n"
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	sectionStart := -1
	sectionEnd := len(lines)
	for index, line := range lines {
		if id, ok := parseCodexProviderHeader(line); ok && id == provider {
			sectionStart = index
			continue
		}
		if sectionStart >= 0 && isTOMLTableHeader(line) {
			sectionEnd = index
			break
		}
	}

	if sectionStart < 0 {
		if baseURL == "" {
			return nil, fmt.Errorf("provider %q 不存在，Base URL 不能为空", provider)
		}
		for len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		providerKey := strconv.Quote(provider)
		if codexProviderID.MatchString(provider) {
			providerKey = provider
		}
		lines = append(lines, "[model_providers."+providerKey+"]", "base_url = "+strconv.Quote(baseURL), "")
	} else {
		baseURLIndex := -1
		indent := ""
		for index := sectionStart + 1; index < sectionEnd; index++ {
			if match := codexBaseURLLine.FindStringSubmatch(lines[index]); match != nil {
				baseURLIndex = index
				indent = match[1]
				break
			}
		}
		if baseURLIndex >= 0 && baseURL == "" {
			lines = append(lines[:baseURLIndex], lines[baseURLIndex+1:]...)
		} else if baseURLIndex >= 0 {
			lines[baseURLIndex] = indent + "base_url = " + strconv.Quote(baseURL)
		} else if baseURL != "" {
			insertAt := sectionStart + 1
			lines = append(lines[:insertAt], append([]string{"base_url = " + strconv.Quote(baseURL)}, lines[insertAt:]...)...)
		}
	}

	updated := strings.Join(lines, "\n")
	if lineEnding == "\r\n" {
		updated = strings.ReplaceAll(updated, "\n", "\r\n")
	}
	result := []byte(updated)
	var validation map[string]interface{}
	if err := toml.Unmarshal(result, &validation); err != nil {
		return nil, fmt.Errorf("更新后的 config.toml 无效: %w", err)
	}
	return result, nil
}

func parseCodexProviderHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "[[") {
		return "", false
	}
	closing := strings.Index(trimmed, "]")
	if closing < 0 {
		return "", false
	}
	path := strings.TrimSpace(trimmed[1:closing])
	const prefix = "model_providers."
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	if codexProviderID.MatchString(id) {
		return id, true
	}
	if len(id) >= 2 && id[0] == '\'' && id[len(id)-1] == '\'' {
		return id[1 : len(id)-1], true
	}
	if unquoted, err := strconv.Unquote(id); err == nil {
		return unquoted, true
	}
	return "", false
}

func isTOMLTableHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "[") && strings.Contains(trimmed, "]")
}

func writeCodexFile(path string, previous, data []byte, pattern string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if len(previous) > 0 {
		backup := path + "." + time.Now().Format("20060102-150405.000000000") + ".bak"
		if err := os.WriteFile(backup, previous, 0600); err != nil {
			return fmt.Errorf("创建备份失败: %w", err)
		}
	}
	tempFile, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if err := tempFile.Chmod(0600); err != nil {
		tempFile.Close()
		return err
	}
	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	return replaceCodexFile(tempPath, path)
}

package clients

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var claudeCodeSettingsMu sync.Mutex

var claudeCodeModelFamilies = []string{"FABLE", "OPUS", "SONNET", "HAIKU"}

type claudeCodeSettingsResponse struct {
	Path              string                       `json:"path"`
	Exists            bool                         `json:"exists"`
	JSONC             bool                         `json:"jsonc"`
	BaseURL           string                       `json:"baseUrl"`
	CredentialKind    string                       `json:"credentialKind"`
	CredentialMasked  string                       `json:"credentialMasked"`
	CredentialPresent bool                         `json:"credentialPresent"`
	ModelDefaults     []claudeCodeModelDefaultView `json:"modelDefaults"`
}

type claudeCodeModelDefaultView struct {
	Family     string `json:"family"`
	Model      string `json:"model"`
	Name       string `json:"name"`
	Supports1M bool   `json:"supports1M"`
}

type saveClaudeCodeSettingsRequest struct {
	BaseURL          string                       `json:"baseUrl"`
	CredentialKind   string                       `json:"credentialKind"`
	CredentialAction string                       `json:"credentialAction"`
	Credential       string                       `json:"credential"`
	ModelDefaults    []saveClaudeCodeModelDefault `json:"modelDefaults"`
}

type saveClaudeCodeModelDefault struct {
	Family     string `json:"family"`
	Model      string `json:"model"`
	Name       string `json:"name"`
	Supports1M bool   `json:"supports1M"`
}

// claudeCode1MSuffix 是 Claude Code 客户端识别 1M 上下文的模型名后缀。
// 写入 ANTHROPIC_DEFAULT_{FAMILY}_MODEL 时追加；读取时反向剥离以还原 supports1M 标记。
const claudeCode1MSuffix = "[1m]"

// parseClaudeCode1MSuffix 从模型名中识别 [1m] 后缀，返回去后缀的模型名与是否支持 1M。
func parseClaudeCode1MSuffix(model string) (string, bool) {
	trimmed := strings.TrimSpace(model)
	if strings.HasSuffix(trimmed, claudeCode1MSuffix) {
		return strings.TrimSuffix(trimmed, claudeCode1MSuffix), true
	}
	return trimmed, false
}

// GetClaudeCodeSettings 返回 Claude Code 全局 settings.json 中的代理与模型配置。
func GetClaudeCodeSettings() gin.HandlerFunc {
	return func(c *gin.Context) {
		claudeCodeSettingsMu.Lock()
		defer claudeCodeSettingsMu.Unlock()

		path := resolveClaudeCodeSettingsPath()
		root, raw, exists, err := readClaudeCodeSettings(path)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取 Claude Code 配置失败: %v", err)})
			return
		}
		env := objectValue(root["env"])
		credentialKind, credential := claudeCodeCredential(env)
		c.JSON(200, claudeCodeSettingsResponse{
			Path:              path,
			Exists:            exists,
			JSONC:             isJSONC(raw),
			BaseURL:           stringValue(env["ANTHROPIC_BASE_URL"]),
			CredentialKind:    credentialKind,
			CredentialMasked:  maskOpenCodeSecret(credential),
			CredentialPresent: credential != "",
			ModelDefaults:     claudeCodeModelDefaults(env),
		})
	}
}

// SaveClaudeCodeSettings 只更新 Claude Code settings.json 中的代理和模型 env 键。
func SaveClaudeCodeSettings() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req saveClaudeCodeSettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Claude Code 配置请求无效"})
			return
		}
		if err := validateClaudeCodeRequest(req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		claudeCodeSettingsMu.Lock()
		defer claudeCodeSettingsMu.Unlock()

		path := resolveClaudeCodeSettingsPath()
		root, raw, _, err := readClaudeCodeSettings(path)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取 Claude Code 配置失败: %v", err)})
			return
		}
		if root == nil {
			root = make(map[string]interface{})
		}
		env := objectValue(root["env"])
		setOptionalString(env, "ANTHROPIC_BASE_URL", req.BaseURL)
		mergeClaudeCodeCredential(env, req)
		mergeClaudeCodeModelDefaults(env, req.ModelDefaults)
		if len(env) == 0 {
			delete(root, "env")
		} else {
			root["env"] = env
		}

		data, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			c.JSON(500, gin.H{"error": "序列化 Claude Code 配置失败"})
			return
		}
		data = append(data, '\n')
		if err := writeClaudeCodeSettings(path, raw, data); err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("保存 Claude Code 配置失败: %v", err)})
			return
		}
		c.JSON(200, gin.H{"success": true, "path": path})
	}
}

func resolveClaudeCodeSettingsPath() string {
	if configDir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configDir != "" {
		return absolutePath(filepath.Join(configDir, "settings.json"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return absolutePath(filepath.Join(".claude", "settings.json"))
	}
	return filepath.Join(home, ".claude", "settings.json")
}

func readClaudeCodeSettings(path string) (map[string]interface{}, []byte, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]interface{}{}, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	cleaned, err := normalizeJSONC(raw)
	if err != nil {
		return nil, raw, true, err
	}
	var root map[string]interface{}
	if err := json.Unmarshal(cleaned, &root); err != nil {
		return nil, raw, true, fmt.Errorf("JSON/JSONC 格式无效: %w", err)
	}
	return root, raw, true, nil
}

func validateClaudeCodeRequest(req saveClaudeCodeSettingsRequest) error {
	if req.CredentialKind != "authToken" && req.CredentialKind != "apiKey" {
		return errors.New("密钥类型仅支持认证令牌或 API Key")
	}
	if req.CredentialAction != "" && req.CredentialAction != "keep" && req.CredentialAction != "replace" && req.CredentialAction != "remove" {
		return errors.New("密钥操作无效")
	}
	seen := make(map[string]struct{}, len(req.ModelDefaults))
	for _, model := range req.ModelDefaults {
		family := strings.ToUpper(strings.TrimSpace(model.Family))
		if !isClaudeCodeModelFamily(family) {
			return fmt.Errorf("不支持的模型系列: %s", model.Family)
		}
		if _, exists := seen[family]; exists {
			return fmt.Errorf("模型系列重复: %s", family)
		}
		seen[family] = struct{}{}
	}
	return nil
}

func isClaudeCodeModelFamily(family string) bool {
	for _, allowed := range claudeCodeModelFamilies {
		if family == allowed {
			return true
		}
	}
	return false
}

func claudeCodeCredential(env map[string]interface{}) (string, string) {
	if token := stringValue(env["ANTHROPIC_AUTH_TOKEN"]); token != "" {
		return "authToken", token
	}
	return "apiKey", stringValue(env["ANTHROPIC_API_KEY"])
}

func mergeClaudeCodeCredential(env map[string]interface{}, req saveClaudeCodeSettingsRequest) {
	if req.CredentialAction == "remove" {
		delete(env, "ANTHROPIC_AUTH_TOKEN")
		delete(env, "ANTHROPIC_API_KEY")
		return
	}
	if req.CredentialAction != "replace" {
		return
	}
	if req.CredentialKind == "apiKey" {
		setOptionalString(env, "ANTHROPIC_API_KEY", req.Credential)
		delete(env, "ANTHROPIC_AUTH_TOKEN")
		return
	}
	setOptionalString(env, "ANTHROPIC_AUTH_TOKEN", req.Credential)
	delete(env, "ANTHROPIC_API_KEY")
}

func claudeCodeModelDefaults(env map[string]interface{}) []claudeCodeModelDefaultView {
	defaults := make([]claudeCodeModelDefaultView, 0, len(claudeCodeModelFamilies))
	for _, family := range claudeCodeModelFamilies {
		rawModel := stringValue(env["ANTHROPIC_DEFAULT_"+family+"_MODEL"])
		model, supports1M := parseClaudeCode1MSuffix(rawModel)
		defaults = append(defaults, claudeCodeModelDefaultView{
			Family:     strings.ToLower(family),
			Model:      model,
			Name:       stringValue(env["ANTHROPIC_DEFAULT_"+family+"_MODEL_NAME"]),
			Supports1M: supports1M,
		})
	}
	return defaults
}

func mergeClaudeCodeModelDefaults(env map[string]interface{}, defaults []saveClaudeCodeModelDefault) {
	byFamily := make(map[string]saveClaudeCodeModelDefault, len(defaults))
	for _, model := range defaults {
		byFamily[strings.ToUpper(strings.TrimSpace(model.Family))] = model
	}
	for _, family := range claudeCodeModelFamilies {
		model, exists := byFamily[family]
		if !exists {
			continue
		}
		// 支持任一 family 的 1M 上下文时，向模型标识追加 [1m] 后缀，
		// Claude Code 客户端据此启用 1M 模式；保存时按 family 独立判断。
		modelID := strings.TrimSpace(model.Model)
		if model.Supports1M && modelID != "" && !strings.HasSuffix(modelID, claudeCode1MSuffix) {
			modelID = modelID + claudeCode1MSuffix
		}
		setOptionalString(env, "ANTHROPIC_DEFAULT_"+family+"_MODEL", modelID)
		setOptionalString(env, "ANTHROPIC_DEFAULT_"+family+"_MODEL_NAME", model.Name)
	}
}

func writeClaudeCodeSettings(path string, previous, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if len(previous) > 0 {
		backup := path + "." + time.Now().Format("20060102-150405.000000000") + ".bak"
		if err := os.WriteFile(backup, previous, 0600); err != nil {
			return fmt.Errorf("创建备份失败: %w", err)
		}
	}
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".claude-*.tmp")
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
	if err := tempFile.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

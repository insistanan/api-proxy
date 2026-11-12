package clients

import (
	"bytes"
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

const openCodeSchemaURL = "https://opencode.ai/config.json"

var openCodeConfigMu sync.Mutex
var errOpenCodeRevisionConflict = errors.New("配置文件已被外部修改，请刷新后重试")

type openCodeConfigResponse struct {
	Path       string                 `json:"path"`
	Exists     bool                   `json:"exists"`
	JSONC      bool                   `json:"jsonc"`
	Model      string                 `json:"model"`
	SmallModel string                 `json:"smallModel"`
	Providers  []openCodeProviderView `json:"providers"`
	Revision   string                 `json:"revision"`
}

type openCodeProviderView struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Protocol      string                 `json:"protocol"`
	NPM           string                 `json:"npm"`
	BaseURL       string                 `json:"baseUrl"`
	APIKeyMasked  string                 `json:"apiKeyMasked"`
	APIKeyPresent bool                   `json:"apiKeyPresent"`
	Headers       map[string]string      `json:"headers"`
	Options       map[string]interface{} `json:"options"`
	Models        []openCodeModelView    `json:"models"`
}

type openCodeModelView struct {
	Key          string                         `json:"key"`
	APIModelID   string                         `json:"apiModelId"`
	Name         string                         `json:"name"`
	ContextLimit int                            `json:"contextLimit"`
	InputLimit   int                            `json:"inputLimit"`
	OutputLimit  int                            `json:"outputLimit"`
	Options      map[string]interface{}         `json:"options"`
	Variants     map[string]openCodeVariantView `json:"variants"`
}

type openCodeVariantView struct {
	ReasoningEffort string `json:"reasoningEffort"`
}

type saveOpenCodeConfigRequest struct {
	Providers []saveOpenCodeProvider `json:"providers" binding:"required"`
	Revision  *string                `json:"revision"`
}

type saveOpenCodeProvider struct {
	ID           string                 `json:"id" binding:"required"`
	Name         string                 `json:"name"`
	Protocol     string                 `json:"protocol" binding:"required"`
	NPM          string                 `json:"npm"`
	BaseURL      string                 `json:"baseUrl"`
	APIKeyAction string                 `json:"apiKeyAction"`
	APIKey       string                 `json:"apiKey"`
	Headers      map[string]string      `json:"headers"`
	Options      map[string]interface{} `json:"options"`
	Models       []saveOpenCodeModel    `json:"models"`
}

type saveOpenCodeModel struct {
	Key          string                         `json:"key" binding:"required"`
	APIModelID   string                         `json:"apiModelId"`
	Name         string                         `json:"name"`
	ContextLimit int                            `json:"contextLimit"`
	InputLimit   int                            `json:"inputLimit"`
	OutputLimit  int                            `json:"outputLimit"`
	Options      map[string]interface{}         `json:"options"`
	Variants     map[string]saveOpenCodeVariant `json:"variants"`
}

type saveOpenCodeVariant struct {
	ReasoningEffort string `json:"reasoningEffort"`
}

// GetOpenCodeConfig 读取 OpenCode 全局配置。API 响应只包含脱敏后的密钥信息。
func GetOpenCodeConfig() gin.HandlerFunc {
	return func(c *gin.Context) {
		openCodeConfigMu.Lock()
		defer openCodeConfigMu.Unlock()

		path := resolveOpenCodeConfigPath()
		root, raw, exists, err := readOpenCodeConfig(path)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取 OpenCode 配置失败: %v", err)})
			return
		}

		response := openCodeConfigResponse{
			Path:       path,
			Exists:     exists,
			JSONC:      isJSONC(raw),
			Model:      stringValue(root["model"]),
			SmallModel: stringValue(root["small_model"]),
			Providers:  openCodeProviderViews(objectValue(root["provider"])),
			Revision:   sha256HexOrEmpty(raw),
		}
		c.JSON(200, response)
	}
}

// SaveOpenCodeConfig 仅更新 OpenCode 的 provider 配置，保留现有全局默认模型设置。
func SaveOpenCodeConfig() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req saveOpenCodeConfigRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "OpenCode 配置请求无效"})
			return
		}
		if err := validateOpenCodeRequest(req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if req.Revision == nil {
			c.JSON(428, gin.H{"error": "保存需要配置版本，请先刷新配置", "code": "revision_required"})
			return
		}

		openCodeConfigMu.Lock()
		defer openCodeConfigMu.Unlock()

		path := resolveOpenCodeConfigPath()
		root, raw, _, err := readOpenCodeConfig(path)
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("读取 OpenCode 配置失败: %v", err)})
			return
		}
		if *req.Revision != sha256HexOrEmpty(raw) {
			c.JSON(409, gin.H{"error": errOpenCodeRevisionConflict.Error(), "code": "revision_conflict"})
			return
		}
		if root == nil {
			root = make(map[string]interface{})
		}
		if _, ok := root["$schema"]; !ok {
			root["$schema"] = openCodeSchemaURL
		}

		existingProviders := objectValue(root["provider"])
		root["provider"] = mergeOpenCodeProviders(existingProviders, req.Providers)

		data, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			c.JSON(500, gin.H{"error": "序列化 OpenCode 配置失败"})
			return
		}
		data = append(data, '\n')
		if err := writeOpenCodeConfig(path, raw, data); err != nil {
			if errors.Is(err, errOpenCodeRevisionConflict) {
				c.JSON(409, gin.H{"error": err.Error(), "code": "revision_conflict"})
				return
			}
			c.JSON(500, gin.H{"error": fmt.Sprintf("保存 OpenCode 配置失败: %v", err)})
			return
		}

		c.JSON(200, gin.H{"success": true, "path": path, "revision": sha256HexOrEmpty(data)})
	}
}

func validateOpenCodeRequest(req saveOpenCodeConfigRequest) error {
	providerIDs := make(map[string]struct{}, len(req.Providers))
	for _, provider := range req.Providers {
		provider.ID = strings.TrimSpace(provider.ID)
		if provider.ID == "" {
			return errors.New("提供商标识不能为空")
		}
		if _, exists := providerIDs[provider.ID]; exists {
			return fmt.Errorf("提供商标识重复: %s", provider.ID)
		}
		providerIDs[provider.ID] = struct{}{}
		if !isOpenCodeProtocol(provider.Protocol) {
			return fmt.Errorf("提供商 %s 的协议无效", provider.ID)
		}
		if provider.Protocol == "custom" && strings.TrimSpace(provider.NPM) == "" {
			return fmt.Errorf("提供商 %s 的自定义 SDK 包不能为空", provider.ID)
		}
		if provider.APIKeyAction != "" && provider.APIKeyAction != "keep" && provider.APIKeyAction != "replace" && provider.APIKeyAction != "remove" {
			return fmt.Errorf("提供商 %s 的密钥操作无效", provider.ID)
		}
		modelKeys := make(map[string]struct{}, len(provider.Models))
		for _, model := range provider.Models {
			model.Key = strings.TrimSpace(model.Key)
			if model.Key == "" {
				return fmt.Errorf("提供商 %s 存在空模型标识", provider.ID)
			}
			if _, exists := modelKeys[model.Key]; exists {
				return fmt.Errorf("提供商 %s 的模型标识重复: %s", provider.ID, model.Key)
			}
			modelKeys[model.Key] = struct{}{}
			if model.ContextLimit < 0 || model.InputLimit < 0 || model.OutputLimit < 0 {
				return fmt.Errorf("提供商 %s 的模型限制不能小于 0", provider.ID)
			}
		}
	}
	return nil
}

func isOpenCodeProtocol(protocol string) bool {
	return protocol == "chat" || protocol == "responses" || protocol == "messages" || protocol == "gemini" || protocol == "custom"
}

func resolveOpenCodeConfigPath() string {
	if configuredPath := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG")); configuredPath != "" {
		return absolutePath(configuredPath)
	}
	if configDir := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG_DIR")); configDir != "" {
		return absolutePath(filepath.Join(configDir, "opencode.json"))
	}
	if configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); configHome != "" {
		return absolutePath(filepath.Join(configHome, "opencode", "opencode.json"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return absolutePath(filepath.Join(".config", "opencode", "opencode.json"))
	}
	return filepath.Join(home, ".config", "opencode", "opencode.json")
}

func absolutePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func readOpenCodeConfig(path string) (map[string]interface{}, []byte, bool, error) {
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

func mergeOpenCodeProviders(existing map[string]interface{}, providers []saveOpenCodeProvider) map[string]interface{} {
	result := make(map[string]interface{}, len(providers))
	for _, provider := range providers {
		oldProvider := objectValue(existing[provider.ID])
		if oldProvider == nil {
			oldProvider = make(map[string]interface{})
		}
		oldProvider["npm"] = npmForOpenCodeProtocol(provider.Protocol, provider.NPM)
		setOptionalString(oldProvider, "name", provider.Name)

		options := objectValue(oldProvider["options"])
		for key := range options {
			if key != "baseURL" && key != "apiKey" && key != "headers" {
				delete(options, key)
			}
		}
		for key, value := range provider.Options {
			options[key] = value
		}
		setOptionalString(options, "baseURL", provider.BaseURL)
		if len(provider.Headers) == 0 {
			delete(options, "headers")
		} else {
			options["headers"] = provider.Headers
		}
		switch provider.APIKeyAction {
		case "replace":
			setOptionalString(options, "apiKey", provider.APIKey)
		case "remove":
			delete(options, "apiKey")
		}
		if len(options) == 0 {
			delete(oldProvider, "options")
		} else {
			oldProvider["options"] = options
		}

		oldModels := objectValue(oldProvider["models"])
		models := make(map[string]interface{}, len(provider.Models))
		for _, model := range provider.Models {
			oldModel := objectValue(oldModels[model.Key])
			if oldModel == nil {
				oldModel = make(map[string]interface{})
			}
			applyOpenCodeImageDefaults(oldModel)
			setOptionalString(oldModel, "id", model.APIModelID)
			setOptionalString(oldModel, "name", model.Name)
			limits := make(map[string]interface{})
			if model.ContextLimit > 0 {
				limits["context"] = model.ContextLimit
			}
			if model.InputLimit > 0 {
				limits["input"] = model.InputLimit
			}
			if model.OutputLimit > 0 {
				limits["output"] = model.OutputLimit
			}
			if len(limits) == 0 {
				delete(oldModel, "limit")
			} else {
				oldModel["limit"] = limits
			}
			if len(model.Options) == 0 {
				delete(oldModel, "options")
			} else {
				oldModel["options"] = model.Options
			}
			// variants
			if len(model.Variants) == 0 {
				delete(oldModel, "variants")
			} else {
				variants := make(map[string]interface{}, len(model.Variants))
				for variantName, variant := range model.Variants {
					v := make(map[string]interface{})
					setOptionalString(v, "reasoningEffort", variant.ReasoningEffort)
					if len(v) > 0 {
						variants[variantName] = v
					}
				}
				if len(variants) == 0 {
					delete(oldModel, "variants")
				} else {
					oldModel["variants"] = variants
				}
			}
			models[model.Key] = oldModel
		}
		if len(models) == 0 {
			delete(oldProvider, "models")
		} else {
			oldProvider["models"] = models
		}
		result[provider.ID] = oldProvider
	}
	return result
}

// applyOpenCodeImageDefaults declares image attachment support without replacing configured capabilities.
func applyOpenCodeImageDefaults(model map[string]interface{}) {
	if _, configured := model["attachment"]; !configured {
		model["attachment"] = true
	}
	if _, configured := model["modalities"]; !configured {
		model["modalities"] = map[string]interface{}{
			"input":  []string{"text", "image"},
			"output": []string{"text"},
		}
	}
}

func npmForOpenCodeProtocol(protocol, configuredNPM string) string {
	switch protocol {
	case "messages":
		return "@ai-sdk/anthropic"
	case "responses":
		return "@ai-sdk/openai"
	case "gemini":
		return "@ai-sdk/google"
	case "custom":
		return strings.TrimSpace(configuredNPM)
	default:
		return "@ai-sdk/openai-compatible"
	}
}

func protocolForNPM(npm string) string {
	switch npm {
	case "@ai-sdk/anthropic":
		return "messages"
	case "@ai-sdk/openai":
		return "responses"
	case "@ai-sdk/google":
		return "gemini"
	default:
		if npm == "" || npm == "@ai-sdk/openai-compatible" {
			return "chat"
		}
		return "custom"
	}
}

func openCodeProviderViews(providers map[string]interface{}) []openCodeProviderView {
	result := make([]openCodeProviderView, 0, len(providers))
	for id, rawProvider := range providers {
		provider := objectValue(rawProvider)
		options := objectValue(provider["options"])
		apiKey := stringValue(options["apiKey"])
		viewOptions := copyObject(options)
		delete(viewOptions, "apiKey")
		delete(viewOptions, "baseURL")
		delete(viewOptions, "headers")
		result = append(result, openCodeProviderView{
			ID:            id,
			Name:          stringValue(provider["name"]),
			Protocol:      protocolForNPM(stringValue(provider["npm"])),
			NPM:           stringValue(provider["npm"]),
			BaseURL:       stringValue(options["baseURL"]),
			APIKeyMasked:  maskOpenCodeSecret(apiKey),
			APIKeyPresent: apiKey != "",
			Headers:       stringMapValue(options["headers"]),
			Options:       viewOptions,
			Models:        openCodeModelViews(objectValue(provider["models"])),
		})
	}
	return result
}

func openCodeModelViews(models map[string]interface{}) []openCodeModelView {
	result := make([]openCodeModelView, 0, len(models))
	for key, rawModel := range models {
		model := objectValue(rawModel)
		limits := objectValue(model["limit"])
		result = append(result, openCodeModelView{
			Key:          key,
			APIModelID:   stringValue(model["id"]),
			Name:         stringValue(model["name"]),
			ContextLimit: intValue(limits["context"]),
			InputLimit:   intValue(limits["input"]),
			OutputLimit:  intValue(limits["output"]),
			Options:      objectValue(model["options"]),
			Variants:     openCodeVariantViews(objectValue(model["variants"])),
		})
	}
	return result
}

func openCodeVariantViews(variants map[string]interface{}) map[string]openCodeVariantView {
	result := make(map[string]openCodeVariantView, len(variants))
	for name, rawVariant := range variants {
		variant := objectValue(rawVariant)
		result[name] = openCodeVariantView{
			ReasoningEffort: stringValue(variant["reasoningEffort"]),
		}
	}
	return result
}

func writeOpenCodeConfig(path string, previous, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if len(previous) > 0 {
		backup := path + "." + time.Now().Format("20060102-150405") + ".bak"
		if err := os.WriteFile(backup, previous, 0600); err != nil {
			return fmt.Errorf("创建备份失败: %w", err)
		}
	}
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".opencode-*.tmp")
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
	// 外部编辑器不受 API 互斥锁保护，替换前再比较原始字节，避免覆盖期间的修改。
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("检查 OpenCode 配置版本失败: %w", err)
	}
	if !bytes.Equal(current, previous) {
		return errOpenCodeRevisionConflict
	}
	return os.Rename(tempPath, path)
}

func normalizeJSONC(raw []byte) ([]byte, error) {
	withoutComments := make([]byte, 0, len(raw))
	inString := false
	escaped := false
	for index := 0; index < len(raw); index++ {
		current := raw[index]
		if inString {
			withoutComments = append(withoutComments, current)
			if escaped {
				escaped = false
			} else if current == '\\' {
				escaped = true
			} else if current == '"' {
				inString = false
			}
			continue
		}
		if current == '"' {
			inString = true
			withoutComments = append(withoutComments, current)
			continue
		}
		if current == '/' && index+1 < len(raw) && raw[index+1] == '/' {
			index += 2
			for index < len(raw) && raw[index] != '\n' && raw[index] != '\r' {
				index++
			}
			if index < len(raw) {
				withoutComments = append(withoutComments, raw[index])
			}
			continue
		}
		if current == '/' && index+1 < len(raw) && raw[index+1] == '*' {
			index += 2
			for index+1 < len(raw) && !(raw[index] == '*' && raw[index+1] == '/') {
				index++
			}
			if index+1 >= len(raw) {
				return nil, errors.New("JSONC 块注释没有结束")
			}
			index++
			continue
		}
		withoutComments = append(withoutComments, current)
	}

	var output bytes.Buffer
	inString = false
	escaped = false
	for index := 0; index < len(withoutComments); index++ {
		current := withoutComments[index]
		if inString {
			output.WriteByte(current)
			if escaped {
				escaped = false
			} else if current == '\\' {
				escaped = true
			} else if current == '"' {
				inString = false
			}
			continue
		}
		if current == '"' {
			inString = true
			output.WriteByte(current)
			continue
		}
		if current == ',' {
			next := index + 1
			for next < len(withoutComments) && (withoutComments[next] == ' ' || withoutComments[next] == '\t' || withoutComments[next] == '\n' || withoutComments[next] == '\r') {
				next++
			}
			if next < len(withoutComments) && (withoutComments[next] == '}' || withoutComments[next] == ']') {
				continue
			}
		}
		output.WriteByte(current)
	}
	return output.Bytes(), nil
}

func isJSONC(raw []byte) bool {
	inString := false
	escaped := false
	for index := 0; index+1 < len(raw); index++ {
		current := raw[index]
		if inString {
			if escaped {
				escaped = false
			} else if current == '\\' {
				escaped = true
			} else if current == '"' {
				inString = false
			}
			continue
		}
		if current == '"' {
			inString = true
			continue
		}
		if current == '/' && (raw[index+1] == '/' || raw[index+1] == '*') {
			return true
		}
	}
	return false
}

func objectValue(value interface{}) map[string]interface{} {
	if object, ok := value.(map[string]interface{}); ok {
		return object
	}
	return make(map[string]interface{})
}

func stringMapValue(value interface{}) map[string]string {
	object := objectValue(value)
	result := make(map[string]string, len(object))
	for key, raw := range object {
		if stringValue(raw) != "" {
			result[key] = stringValue(raw)
		}
	}
	return result
}

func copyObject(source map[string]interface{}) map[string]interface{} {
	copy := make(map[string]interface{}, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}

func stringValue(value interface{}) string {
	stringValue, _ := value.(string)
	return stringValue
}

func boolValue(value interface{}) bool {
	boolValue, _ := value.(bool)
	return boolValue
}

func intValue(value interface{}) int {
	number, ok := value.(float64)
	if !ok {
		return 0
	}
	return int(number)
}

func setOptionalString(object map[string]interface{}, key, value string) {
	if value = strings.TrimSpace(value); value == "" {
		delete(object, key)
		return
	}
	object[key] = value
}

func maskOpenCodeSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if strings.HasPrefix(secret, "{env:") && strings.HasSuffix(secret, "}") {
		return secret
	}
	if len(secret) <= 8 {
		return "已配置"
	}
	return secret[:4] + "..." + secret[len(secret)-4:]
}

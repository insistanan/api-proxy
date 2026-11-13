package clients

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Codex 模型目录（models.json）管理：
// - 预设模板 + 少量可调字段生成模型元数据条目（与 DeepSeek 官方 Codex 接入文档的字段保持一致）
// - 按 slug upsert / delete，保留用户手工写入的未知字段与条目
// - setDefault 同步 config.toml 根级 model / model_catalog_json / model_reasoning_effort
//
// Codex（0.144+）对条目的两条硬性约束，违反将导致 Codex 启动失败：
// - 每个条目必须携带 base_instructions 或 model_messages.instructions_template 之一；
//   自定义条目的指令从动态获取的内置清单复制，不在代码里硬编码大段提示词。
// - model_catalog_json 是整体替换语义：设置后内置模型全部失效，因此每次写入都保证
//   内置清单条目完整（已存在的同 slug 条目保持不动）；目录删空时必须回退该配置键。

const (
	codexModelCatalogFileName = "models.json"

	codexRootKeyModel          = "model"
	codexRootKeyModelCatalog   = "model_catalog_json"
	codexRootKeyReasoningLevel = "model_reasoning_effort"

	codexMaxContextWindowLimit = 100000000
)

var (
	codexSlugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	codexRootKeys    = []string{codexRootKeyModel, codexRootKeyModelCatalog, codexRootKeyReasoningLevel}
)

type saveCodexModelCatalogRequest struct {
	Action                string `json:"action"`
	Slug                  string `json:"slug"`
	DisplayName           string `json:"displayName"`
	Description           string `json:"description"`
	SupportsImage         bool   `json:"supportsImage"`
	ContextWindow         int64  `json:"contextWindow"`
	DefaultReasoningLevel string `json:"defaultReasoningLevel"`
	ReasoningEffort       string `json:"reasoningEffort"`
	ClearReasoningEffort  bool   `json:"clearReasoningEffort"`
}

type codexModelUpsertInput struct {
	Slug                  string
	DisplayName           string
	Description           string
	SupportsImage         bool
	ContextWindow         int64
	DefaultReasoningLevel string
}

type codexModelCatalogResponse struct {
	Success    bool   `json:"success"`
	ModelsPath string `json:"modelsPath"`
	ConfigPath string `json:"configPath"`
}

// SaveCodexModelCatalog 管理 Codex 模型目录（models.json）与 config.toml 根级模型字段。
func SaveCodexModelCatalog() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req saveCodexModelCatalogRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Codex 模型目录请求无效"})
			return
		}
		if err := validateCodexModelCatalogRequest(req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		codexSettingsMu.Lock()
		defer codexSettingsMu.Unlock()

		configPath, _, modelsPath, err := resolveCodexPaths()
		if err != nil {
			c.JSON(500, gin.H{"error": fmt.Sprintf("解析 Codex 配置目录失败: %v", err)})
			return
		}
		response := codexModelCatalogResponse{ModelsPath: modelsPath, ConfigPath: configPath}
		// 内置清单需要执行外部命令，限制总时长防止请求悬挂。
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()

		switch req.Action {
		case "upsert":
			builtin, err := loadCodexBuiltinCatalog(ctx, true)
			if err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("获取 Codex 内置模型清单失败: %v", err)})
				return
			}
			updated, err := upsertCodexModelCatalog(modelsPath, codexModelUpsertInput{
				Slug:                  strings.TrimSpace(req.Slug),
				DisplayName:           strings.TrimSpace(req.DisplayName),
				Description:           strings.TrimSpace(req.Description),
				SupportsImage:         req.SupportsImage,
				ContextWindow:         req.ContextWindow,
				DefaultReasoningLevel: strings.TrimSpace(req.DefaultReasoningLevel),
			}, builtin)
			if err != nil {
				respondCodexModelError(c, err)
				return
			}
			if err := writeCodexFile(modelsPath, updated.previous, updated.data, ".codex-models-*.tmp"); err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("保存 models.json 失败: %v", err)})
				return
			}
		case "delete":
			updated, empty, err := deleteCodexModelCatalog(modelsPath, strings.TrimSpace(req.Slug))
			if err != nil {
				respondCodexModelError(c, err)
				return
			}
			if err := writeCodexFile(modelsPath, updated.previous, updated.data, ".codex-models-*.tmp"); err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("保存 models.json 失败: %v", err)})
				return
			}
			if err := cleanupCodexConfigAfterModelDelete(configPath, strings.TrimSpace(req.Slug), empty); err != nil {
				c.JSON(500, gin.H{"error": fmt.Sprintf("更新 Codex config.toml 失败: %v", err)})
				return
			}
		case "setDefault":
			if err := setCodexDefaultModel(ctx, configPath, modelsPath, strings.TrimSpace(req.Slug), req.ReasoningEffort, req.ClearReasoningEffort); err != nil {
				respondCodexModelError(c, err)
				return
			}
		default:
			c.JSON(400, gin.H{"error": "action 仅支持 upsert / delete / setDefault"})
			return
		}

		response.Success = true
		c.JSON(200, response)
	}
}

// respondCodexModelError 按错误类型区分状态码：用户输入问题 400，其余 500。
func respondCodexModelError(c *gin.Context, err error) {
	var inputErr codexUserInputError
	if errors.As(err, &inputErr) {
		c.JSON(400, gin.H{"error": inputErr.Error()})
		return
	}
	c.JSON(500, gin.H{"error": err.Error()})
}

// codexUserInputError 标识由请求参数导致的错误（应返回 400）。
type codexUserInputError struct{ message string }

func (e codexUserInputError) Error() string { return e.message }

func newCodexInputError(format string, args ...any) error {
	return codexUserInputError{message: fmt.Sprintf(format, args...)}
}

func validateCodexModelCatalogRequest(req saveCodexModelCatalogRequest) error {
	switch req.Action {
	case "upsert", "delete", "setDefault":
	default:
		return newCodexInputError("action 仅支持 upsert / delete / setDefault")
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		return newCodexInputError("模型 slug 不能为空")
	}
	if !codexSlugPattern.MatchString(slug) {
		return newCodexInputError("slug 仅支持字母、数字、点、下划线和连字符，且以字母或数字开头")
	}
	if req.Action == "upsert" {
		if req.ContextWindow <= 0 || req.ContextWindow > codexMaxContextWindowLimit {
			return newCodexInputError("上下文窗口必须在 1 ~ %d 之间", codexMaxContextWindowLimit)
		}
	}
	if req.ReasoningEffort != "" && req.ClearReasoningEffort {
		return newCodexInputError("reasoningEffort 与 clearReasoningEffort 不能同时设置")
	}
	return nil
}

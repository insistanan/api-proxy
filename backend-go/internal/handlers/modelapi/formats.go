package modelapi

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/modelcatalog"
)

type anthropicModel struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

type anthropicList struct {
	Data    []anthropicModel `json:"data"`
	HasMore bool             `json:"has_more"`
	FirstID *string          `json:"first_id"`
	LastID  *string          `json:"last_id"`
}

type geminiModel struct {
	Name                       string   `json:"name"`
	DisplayName                string   `json:"displayName"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
}

type geminiList struct {
	Models        []geminiModel `json:"models"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
}

func asAnthropic(model modelcatalog.ModelEntry) anthropicModel {
	// 分组别名没有真实模型的创建日期，沿用目录 created=0 的未知时间哨兵。
	return anthropicModel{ID: model.ID, Type: "model", DisplayName: model.ID, CreatedAt: time.Unix(model.Created, 0).UTC().Format(time.RFC3339)}
}

func asGemini(model modelcatalog.ModelEntry) geminiModel {
	// 只声明网关提供的生成入口，不编造分组内模型的上下文长度、价格或输入模态。
	return geminiModel{Name: "models/" + model.ID, DisplayName: model.ID, SupportedGenerationMethods: []string{"generateContent"}}
}

func formatModel(model modelcatalog.ModelEntry, format string) any {
	switch format {
	case formatAnthropic:
		return asAnthropic(model)
	case formatGemini:
		return asGemini(model)
	default:
		return model
	}
}

func listResponse(c *gin.Context, models modelcatalog.ModelsResponse, format string) (any, error) {
	switch format {
	case formatAnthropic:
		return anthropicPage(c, models.Data)
	case formatGemini:
		return geminiPage(c, models.Data)
	default:
		return models, nil
	}
}

func modelIndex(models []modelcatalog.ModelEntry, id string) (int, error) {
	for index, model := range models {
		if model.ID == id {
			return index, nil
		}
	}
	return 0, fmt.Errorf("分页游标不在当前入口目录中，请重新获取模型列表")
}

func anthropicPage(c *gin.Context, models []modelcatalog.ModelEntry) (anthropicList, error) {
	result := anthropicList{Data: []anthropicModel{}}
	limit, err := pageSize(c, "limit", 20)
	if err != nil {
		return result, err
	}
	after, hasAfter, err := queryValue(c, "after_id")
	if err != nil {
		return result, err
	}
	before, hasBefore, err := queryValue(c, "before_id")
	if err != nil {
		return result, err
	}
	start, end := 0, len(models)
	if hasAfter {
		index, err := modelIndex(models, after)
		if err != nil {
			return result, err
		}
		start = index + 1
	}
	if hasBefore {
		index, err := modelIndex(models, before)
		if err != nil {
			return result, err
		}
		end = index
	}
	if start > end {
		return result, fmt.Errorf("after_id 必须位于 before_id 之前")
	}
	result.HasMore = end-start > limit
	if hasBefore && !hasAfter {
		start = max(start, end-limit)
	} else {
		end = min(end, start+limit)
	}
	for _, model := range models[start:end] {
		result.Data = append(result.Data, asAnthropic(model))
	}
	if len(result.Data) > 0 {
		result.FirstID = &result.Data[0].ID
		result.LastID = &result.Data[len(result.Data)-1].ID
	}
	return result, nil
}

func geminiPage(c *gin.Context, models []modelcatalog.ModelEntry) (geminiList, error) {
	result := geminiList{Models: []geminiModel{}}
	limit, err := pageSize(c, "pageSize", 50)
	if err != nil {
		return result, err
	}
	token, exists, err := queryValue(c, "pageToken")
	if err != nil {
		return result, err
	}
	start := 0
	if exists {
		id, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || len(id) == 0 {
			return result, fmt.Errorf("pageToken 无效，请使用上次响应的 nextPageToken")
		}
		index, err := modelIndex(models, string(id))
		if err != nil {
			return result, err
		}
		start = index + 1
	}
	end := min(len(models), start+limit)
	for _, model := range models[start:end] {
		result.Models = append(result.Models, asGemini(model))
	}
	if end < len(models) {
		result.NextPageToken = base64.RawURLEncoding.EncodeToString([]byte(models[end-1].ID))
	}
	return result, nil
}

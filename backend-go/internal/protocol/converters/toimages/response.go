package toimages

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ImageItem struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Status        string          `json:"status"`
	Result        string          `json:"result"`
	ResultJSON    string          `json:"-"` // 流式路径保留原始 JSON 字符串切片，不解码或复制整张图片
	RevisedPrompt string          `json:"revised_prompt"`
	OutputFormat  string          `json:"output_format"`
	Background    string          `json:"background"`
	Size          string          `json:"size"`
	Quality       string          `json:"quality"`
	Usage         json.RawMessage `json:"usage"`
}

type ResponsesResponse struct {
	CreatedAt int64           `json:"created_at"`
	Status    string          `json:"status"`
	Output    []ImageItem     `json:"output"`
	Error     json.RawMessage `json:"error"`
	Usage     json.RawMessage `json:"usage"`
}

type ImageData struct {
	Base64        string `json:"b64_json"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type Response struct {
	Created      int64           `json:"created"`
	Data         []ImageData     `json:"data"`
	OutputFormat string          `json:"output_format,omitempty"`
	Background   string          `json:"background,omitempty"`
	Size         string          `json:"size,omitempty"`
	Quality      string          `json:"quality,omitempty"`
	Usage        json.RawMessage `json:"usage,omitempty"`
}

func ConvertResponse(response ResponsesResponse) (Response, error) {
	if len(response.Error) > 0 && string(response.Error) != "null" {
		return Response{}, ResponseError(response.Error)
	}
	if response.Status != "" && response.Status != "completed" {
		return Response{}, fmt.Errorf("Responses 图片请求未完成，状态为 %s", response.Status)
	}
	created := response.CreatedAt
	if created == 0 {
		created = time.Now().Unix()
	}
	result := Response{Created: created, Data: make([]ImageData, 0)}
	for _, item := range response.Output {
		if item.Type != "image_generation_call" {
			continue
		}
		if item.Status != "" && item.Status != "completed" {
			return Response{}, fmt.Errorf("Responses 图片生成未完成，状态为 %s", item.Status)
		}
		if strings.TrimSpace(item.Result) == "" {
			return Response{}, fmt.Errorf("Responses 图片生成结果缺少图片数据")
		}
		result.Data = append(result.Data, ImageData{Base64: item.Result, RevisedPrompt: item.RevisedPrompt})
		if len(result.Data) == 1 {
			result.OutputFormat, result.Background = item.OutputFormat, item.Background
			result.Size, result.Quality, result.Usage = item.Size, item.Quality, item.Usage
		}
	}
	if len(result.Data) == 0 {
		return Response{}, fmt.Errorf("Responses 接口未返回任何已完成的图片生成结果")
	}
	return result, nil
}

func ResponseError(raw json.RawMessage) error {
	var details struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(raw, &details); err != nil {
		return fmt.Errorf("Responses 图片接口返回无效的错误信息: %w", err)
	}
	if details.Message == "" {
		return fmt.Errorf("Responses 图片接口返回错误，错误码为 %s", details.Code)
	}
	return fmt.Errorf("Responses 图片接口失败: %s", details.Message)
}

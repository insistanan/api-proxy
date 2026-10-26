package images

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/handlers/streams"
	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/gin-gonic/gin"
)

type responsesImageItem struct {
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

type responsesImageResponse struct {
	CreatedAt int64                `json:"created_at"`
	Status    string               `json:"status"`
	Output    []responsesImageItem `json:"output"`
	Error     json.RawMessage      `json:"error"`
	Usage     json.RawMessage      `json:"usage"`
}

type imagesResponseData struct {
	Base64        string `json:"b64_json"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type imagesResponse struct {
	Created      int64                `json:"created"`
	Data         []imagesResponseData `json:"data"`
	OutputFormat string               `json:"output_format,omitempty"`
	Background   string               `json:"background,omitempty"`
	Size         string               `json:"size,omitempty"`
	Quality      string               `json:"quality,omitempty"`
	Usage        json.RawMessage      `json:"usage,omitempty"`
}

// handleImagesResponsesSuccess 只输出 Images 协议，绝不把 Responses 的
// output/text/reasoning 当作成功图片返回。关闭与解压都覆盖客户端断连出口。
func handleImagesResponsesSuccess(c *gin.Context, resp *http.Response, envCfg *config.EnvConfig, endpoint string, requestedStream bool) (*types.Usage, error) {
	reader, _, err := utils.WrapStreamReaderIfNeeded(resp)
	if err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("解压 Responses 图片响应失败: %w", err)
	}
	defer reader.Close()
	if envCfg.MaxRequestBodySize <= 0 {
		return nil, fmt.Errorf("Responses 图片响应大小限制必须为正数")
	}
	buffered := bufio.NewReaderSize(reader, 32*1024)
	if streams.IsEventStreamResponse(resp) || requestedStream {
		// 部分兼容上游遗漏 SSE Content-Type，或在流式请求下返回 JSON。
		// 只查看正文首个非空白字节，避免为判断传输格式预读整张图片。
		for {
			prefix, err := buffered.Peek(1)
			if err != nil {
				return nil, fmt.Errorf("读取 Responses 图片响应开头失败: %w", err)
			}
			if prefix[0] != ' ' && prefix[0] != '\n' && prefix[0] != '\r' && prefix[0] != '\t' {
				if prefix[0] != '{' && prefix[0] != '[' {
					return convertResponsesImageStream(c, buffered, resp.Header, endpoint, requestedStream, envCfg.MaxRequestBodySize)
				}
				break
			}
			if _, err := buffered.ReadByte(); err != nil {
				return nil, fmt.Errorf("读取 Responses 图片响应失败: %w", err)
			}
		}
	}
	body, err := utils.ReadWithOutputLimit(buffered, envCfg.MaxRequestBodySize)
	if err != nil {
		return nil, fmt.Errorf("读取 Responses 图片响应失败: %w", err)
	}
	var response responsesImageResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("解析 Responses 图片响应失败: %w", err)
	}
	converted, err := convertResponsesImageResponse(response)
	if err != nil {
		return nil, err
	}
	if requestedStream {
		writer := newImagesStreamWriter(c, resp.Header, endpoint)
		for _, item := range response.Output {
			if item.Type == "image_generation_call" {
				if err := writer.completed(item, converted.Created); err != nil {
					return nil, err
				}
			}
		}
	} else {
		utils.ForwardResponseHeadersEx(resp.Header, c.Writer, true)
		utils.StripEntityHeadersForRebuiltBody(c.Writer.Header())
		c.JSON(http.StatusOK, converted)
	}
	usage, err := responsesImageUsage(response.Usage)
	if err != nil {
		return nil, fmt.Errorf("解析 Responses 图片用量失败: %w", err)
	}
	return usage, nil
}

type nativeImagesJSONData struct {
	Base64        json.RawMessage `json:"b64_json"`
	RevisedPrompt string          `json:"revised_prompt"`
}

type nativeImagesJSONResponse struct {
	Created      int64                  `json:"created"`
	Data         []nativeImagesJSONData `json:"data"`
	OutputFormat string                 `json:"output_format"`
	Background   string                 `json:"background"`
	Size         string                 `json:"size"`
	Quality      string                 `json:"quality"`
	Usage        json.RawMessage        `json:"usage"`
	Error        json.RawMessage        `json:"error"`
}

// writeImagesJSONAsCompletedStream 把上游完整 Images JSON 包成一条 completed SSE。
// 不解码或再编码整段 base64，只把原始 JSON 字符串切片写进事件。
func writeImagesJSONAsCompletedStream(c *gin.Context, resp *http.Response, envCfg *config.EnvConfig, endpoint string) error {
	reader, _, err := utils.WrapStreamReaderIfNeeded(resp)
	if err != nil {
		resp.Body.Close()
		return fmt.Errorf("解压 Images 响应失败: %w", err)
	}
	defer reader.Close()
	if envCfg == nil || envCfg.MaxRequestBodySize <= 0 {
		return fmt.Errorf("Images 响应大小限制必须为正数")
	}
	body, err := utils.ReadWithOutputLimit(reader, envCfg.MaxRequestBodySize)
	if err != nil {
		return fmt.Errorf("读取 Images 响应失败: %w", err)
	}
	var response nativeImagesJSONResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("解析 Images 响应失败: %w", err)
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		return nativeImagesJSONError(response.Error)
	}
	if len(response.Data) == 0 {
		return fmt.Errorf("Images 接口未返回任何图片数据")
	}
	created := response.Created
	if created == 0 {
		created = time.Now().Unix()
	}
	writer := newImagesStreamWriter(c, resp.Header, endpoint)
	for _, item := range response.Data {
		quoted, err := quotedImagesBase64JSON(item.Base64)
		if err != nil {
			return writer.failure(err)
		}
		if err := writer.completed(responsesImageItem{
			ResultJSON:    quoted,
			RevisedPrompt: item.RevisedPrompt,
			OutputFormat:  response.OutputFormat,
			Background:    response.Background,
			Size:          response.Size,
			Quality:       response.Quality,
			Usage:         response.Usage,
		}, created); err != nil {
			return err
		}
	}
	return nil
}

func quotedImagesBase64JSON(raw json.RawMessage) (string, error) {
	quoted := strings.TrimSpace(string(raw))
	if len(quoted) < 2 || quoted[0] != '"' || quoted[len(quoted)-1] != '"' {
		return "", fmt.Errorf("Images 响应 data.b64_json 必须是 JSON 字符串")
	}
	if len(quoted) == 2 {
		return "", fmt.Errorf("Images 响应缺少图片数据")
	}
	return quoted, nil
}

func nativeImagesJSONError(raw json.RawMessage) error {
	var details struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(raw, &details); err != nil {
		return fmt.Errorf("Images 接口返回无效的错误信息: %w", err)
	}
	if details.Message == "" {
		return fmt.Errorf("Images 接口返回错误，错误码为 %s", details.Code)
	}
	return fmt.Errorf("Images 接口失败: %s", details.Message)
}

func convertResponsesImageResponse(response responsesImageResponse) (imagesResponse, error) {
	if len(response.Error) > 0 && string(response.Error) != "null" {
		return imagesResponse{}, responsesImageError(response.Error)
	}
	if response.Status != "" && response.Status != "completed" {
		return imagesResponse{}, fmt.Errorf("Responses 图片请求未完成，状态为 %s", response.Status)
	}
	created := response.CreatedAt
	if created == 0 {
		created = time.Now().Unix()
	}
	result := imagesResponse{Created: created, Data: make([]imagesResponseData, 0)}
	for _, item := range response.Output {
		if item.Type != "image_generation_call" {
			continue
		}
		if item.Status != "" && item.Status != "completed" {
			return imagesResponse{}, fmt.Errorf("Responses 图片生成未完成，状态为 %s", item.Status)
		}
		if strings.TrimSpace(item.Result) == "" {
			return imagesResponse{}, fmt.Errorf("Responses 图片生成结果缺少图片数据")
		}
		result.Data = append(result.Data, imagesResponseData{Base64: item.Result, RevisedPrompt: item.RevisedPrompt})
		if len(result.Data) == 1 {
			result.OutputFormat, result.Background = item.OutputFormat, item.Background
			result.Size, result.Quality, result.Usage = item.Size, item.Quality, item.Usage
		}
	}
	if len(result.Data) == 0 {
		return imagesResponse{}, fmt.Errorf("Responses 接口未返回任何已完成的图片生成结果")
	}
	return result, nil
}

func responsesImageError(raw json.RawMessage) error {
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

package images

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"insistanan/api-proxy/internal/api/common"
	"insistanan/api-proxy/internal/api/streams"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/upstreamreq"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/converters/toimages"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/protocol/types"
)

// handleImagesResponsesSuccess 只输出 Images 协议，绝不把 Responses 的
// output/text/reasoning 当作成功图片返回。关闭与解压都覆盖客户端断连出口。
func handleImagesResponsesSuccess(c *gin.Context, resp *http.Response, envCfg *config.EnvConfig, endpoint string, requestedStream bool) (*types.Usage, error) {
	lifecycle := responsesLifecycle(c)
	defer lifecycle.close()
	defer codec.BindStreamBody(lifecycle.ctx, resp.Body)()
	reader, _, err := codec.WrapStreamReaderIfNeeded(resp)
	if err != nil {
		if cause := context.Cause(lifecycle.ctx); cause != nil {
			return nil, cause
		}
		return nil, fmt.Errorf("解压 Responses 图片响应失败: %w", err)
	}
	defer reader.Close()
	if envCfg.Current().MaxRequestBodySize <= 0 {
		return nil, fmt.Errorf("Responses 图片响应大小限制必须为正数")
	}
	buffered := bufio.NewReaderSize(reader, 32*1024)
	if streams.IsEventStreamResponse(resp) || requestedStream {
		// 部分兼容上游遗漏 SSE Content-Type，或在流式请求下返回 JSON。
		// 只查看正文首个非空白字节，避免为判断传输格式预读整张图片。
		for {
			prefix, err := buffered.Peek(1)
			if cause := context.Cause(lifecycle.ctx); cause != nil {
				return nil, fmt.Errorf("读取 Responses 图片响应中止: %w", cause)
			}
			if err != nil {
				return nil, fmt.Errorf("读取 Responses 图片响应开头失败: %w", err)
			}
			if prefix[0] != ' ' && prefix[0] != '\n' && prefix[0] != '\r' && prefix[0] != '\t' {
				if prefix[0] != '{' && prefix[0] != '[' {
					return convertResponsesImageStream(c, buffered, resp.Header, endpoint, requestedStream, envCfg.Current().MaxRequestBodySize)
				}
				break
			}
			if _, err := buffered.ReadByte(); err != nil {
				return nil, fmt.Errorf("读取 Responses 图片响应失败: %w", err)
			}
		}
	}
	body, err := codec.ReadWithOutputLimit(buffered, envCfg.Current().MaxRequestBodySize)
	if cause := context.Cause(lifecycle.ctx); cause != nil {
		return nil, fmt.Errorf("读取 Responses 图片响应中止: %w", cause)
	}
	if err != nil {
		return nil, fmt.Errorf("读取 Responses 图片响应失败: %w", err)
	}
	var response toimages.ResponsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("解析 Responses 图片响应失败: %w", err)
	}
	usage, usageErr := responsesImageUsage(response.Usage)
	if usageErr != nil {
		return nil, fmt.Errorf("解析 Responses 图片用量失败: %w", usageErr)
	}
	converted, err := toimages.ConvertResponse(response)
	if err != nil {
		return usage, err
	}
	if requestedStream {
		writer := newImagesStreamWriter(c, resp.Header, endpoint)
		for _, item := range response.Output {
			if item.Type == "image_generation_call" {
				if err := writer.completed(item, converted.Created); err != nil {
					return usage, err
				}
			}
		}
	} else {
		if err := writeImagesJSON(c, resp.Header, converted); err != nil {
			return usage, err
		}
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
func writeImagesJSONAsCompletedStream(c *gin.Context, resp *http.Response, envCfg *config.EnvConfig, endpoint string) (*types.Usage, error) {
	defer codec.BindStreamBody(c.Request.Context(), resp.Body)()
	reader, _, err := codec.WrapStreamReaderIfNeeded(resp)
	if err != nil {
		if cancelErr := c.Request.Context().Err(); cancelErr != nil {
			return nil, cancelErr
		}
		return nil, fmt.Errorf("解压 Images 响应失败: %w", err)
	}
	defer reader.Close()
	if envCfg == nil || envCfg.Current().MaxRequestBodySize <= 0 {
		return nil, fmt.Errorf("Images 响应大小限制必须为正数")
	}
	body, err := codec.ReadWithOutputLimit(reader, envCfg.Current().MaxRequestBodySize)
	if cancelErr := c.Request.Context().Err(); cancelErr != nil {
		return nil, cancelErr
	}
	if err != nil {
		return nil, fmt.Errorf("读取 Images 响应失败: %w", err)
	}
	var response nativeImagesJSONResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("解析 Images 响应失败: %w", err)
	}
	usage, err := tokenusage.ParseJSONUsage(response.Usage)
	if err != nil {
		return nil, fmt.Errorf("解析 Images 图片用量失败: %w", err)
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		return usage, nativeImagesJSONError(response.Error)
	}
	if len(response.Data) == 0 {
		return usage, fmt.Errorf("Images 接口未返回任何图片数据")
	}
	created := response.Created
	if created == 0 {
		created = time.Now().Unix()
	}
	writer := newImagesStreamWriter(c, resp.Header, endpoint)
	for _, item := range response.Data {
		quoted, err := quotedImagesBase64JSON(item.Base64)
		if err != nil {
			return usage, writer.failure(err)
		}
		if err := writer.completed(toimages.ImageItem{
			ResultJSON:    quoted,
			RevisedPrompt: item.RevisedPrompt,
			OutputFormat:  response.OutputFormat,
			Background:    response.Background,
			Size:          response.Size,
			Quality:       response.Quality,
			Usage:         response.Usage,
		}, created); err != nil {
			return usage, err
		}
	}
	return usage, nil
}

// writeImagesJSON 显式返回交付失败，避免 gin.JSON 将写错误留在 Context 后仍返回成功。
func writeImagesJSON(c *gin.Context, headers http.Header, payload toimages.Response) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("编码 Images 响应失败: %w", err)
	}
	if err := c.Request.Context().Err(); err != nil {
		return err
	}
	upstreamreq.ForwardResponseHeadersEx(headers, c.Writer, true)
	upstreamreq.StripEntityHeadersForRebuiltBody(c.Writer.Header())
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Status(http.StatusOK)
	return common.WriteStream(c.Writer, nil, body)
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

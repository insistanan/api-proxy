package modelapi

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	KindHeader      = "X-API-Proxy-Type"
	formatOpenAI    = "openai"
	formatAnthropic = "anthropic"
	formatGemini    = "gemini"
)

type options struct {
	kind   string
	format string
}

func queryValue(c *gin.Context, name string) (string, bool, error) {
	values, exists := c.Request.URL.Query()[name]
	if !exists {
		return "", false, nil
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", true, fmt.Errorf("%s 必须提供一个非空值", name)
	}
	return strings.TrimSpace(values[0]), true, nil
}

func requestKind(c *gin.Context) (string, error) {
	kind, _, err := queryValue(c, "type")
	if err != nil {
		return "", err
	}
	headers := c.Request.Header.Values(KindHeader)
	if len(headers) > 1 || (len(headers) == 1 && strings.TrimSpace(headers[0]) == "") {
		return "", fmt.Errorf("%s 必须提供一个非空值", KindHeader)
	}
	headerKind := strings.ToLower(strings.TrimSpace(c.GetHeader(KindHeader)))
	kind = strings.ToLower(kind)
	if kind != "" && headerKind != "" && kind != headerKind {
		return "", fmt.Errorf("type 与 %s 指定的入口协议不一致", KindHeader)
	}
	if kind == "" {
		kind = headerKind
	}
	return kind, nil
}

func requestOptions(c *gin.Context, nativeGemini bool) (options, error) {
	result := options{format: formatOpenAI}
	if nativeGemini {
		result.format = formatGemini
	} else if c.GetHeader("anthropic-version") != "" {
		result.format = formatAnthropic
	}
	format, hasFormat, err := queryValue(c, "format")
	if err != nil {
		return result, err
	}
	if hasFormat {
		format = strings.ToLower(format)
		if nativeGemini && format != formatGemini {
			return result, fmt.Errorf("/v1beta/models 仅支持 Gemini 格式")
		}
		if !nativeGemini && format != formatOpenAI && format != formatAnthropic {
			return result, fmt.Errorf("format 仅支持 openai 或 anthropic；Gemini 请使用 /v1beta/models")
		}
		result.format = format
	}
	result.kind, err = requestKind(c)
	if err != nil {
		return result, err
	}
	if nativeGemini {
		if result.kind != "" && result.kind != "gemini" {
			return result, fmt.Errorf("/v1beta/models 仅查询 Gemini 入口分组")
		}
		result.kind = "gemini"
	} else if result.kind == "" && (c.GetHeader("anthropic-version") != "" || result.format == formatAnthropic) {
		// 入口筛选与输出格式独立，切换格式不应扩大 Anthropic 客户端的目录范围。
		result.kind = "messages"
	}
	return result, nil
}

func pageSize(c *gin.Context, name string, defaultSize int) (int, error) {
	value, exists, err := queryValue(c, name)
	if err != nil {
		return 0, err
	}
	if !exists {
		return defaultSize, nil
	}
	size, err := strconv.Atoi(value)
	if err != nil || size < 1 || size > 1000 {
		return 0, fmt.Errorf("%s 必须为 1 到 1000 的整数", name)
	}
	return size, nil
}

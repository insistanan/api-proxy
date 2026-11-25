package responses

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/api/streams"
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/protocol/providers"
	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/insistanan/api-proxy/internal/proxycore"
	"github.com/tidwall/gjson"
)

// 生图原样事件只读取完成状态和用量，不把大图片转换为通用会话对象。
func forwardResponsesImageStream(c *gin.Context, resp *http.Response) (*types.Usage, error) {
	var usage *types.Usage
	err := streams.ForwardEventStream(c, resp, "responses", func(event codec.SSEEvent) (bool, error) {
		if event.Data == "" && event.Name != "error" {
			return false, nil
		}
		if !gjson.Valid(event.Data) || !gjson.Parse(event.Data).IsObject() {
			return false, fmt.Errorf("Responses SSE 数据必须是有效 JSON 对象")
		}
		root := gjson.Parse(event.Data)
		name := root.Get("type").String()
		if name == "" {
			name = event.Name
		} else if event.Name != "" && name != event.Name {
			return false, fmt.Errorf("Responses SSE 事件名 %q 与 type %q 不一致", event.Name, name)
		}
		response := root.Get("response")
		if field := response.Get("usage"); field.Exists() {
			parsed, err := tokenusage.ParseJSONUsage([]byte(field.Raw))
			if err != nil {
				return false, fmt.Errorf("解析 Responses 图片流用量失败: %w", err)
			}
			if parsed != nil {
				usage = parsed
			}
		}
		failure := root.Get("error")
		responseFailure := response.Get("error")
		status := response.Get("status").String()
		if name == "error" || name == "response.failed" || name == "response.incomplete" ||
			status == "failed" || status == "incomplete" ||
			failure.Exists() && failure.Type != gjson.Null || responseFailure.Exists() && responseFailure.Type != gjson.Null {
			return false, providers.NewUpstreamStreamFailedError(proxycore.IsUpstreamModelCapacityError([]byte(event.Data)), event.Data)
		}
		if name == "response.completed" {
			if !response.IsObject() || status != "" && status != "completed" {
				return false, fmt.Errorf("Responses 完成事件缺少有效的完成响应")
			}
			return true, nil
		}
		return false, nil
	})
	return usage, err
}

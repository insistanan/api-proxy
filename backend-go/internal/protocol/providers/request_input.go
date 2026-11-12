// request_input.go 定义 Provider 构造上游请求时使用的输入和返回结果（R4-a）。
//
// llmgate 和 visionlayer 直接通过 RequestInput 传入参数，无需构造 Gin 上下文。
// 协议 handler 则通过 ConvertToProviderRequest 解析入站请求，再交给 BuildRequest 构造。
package providers

import (
	"context"
	"net/http"

	"github.com/insistanan/api-proxy/internal/infra/config"
)

// RequestInput 保存一次协议转换所需的请求参数。
// 参数由 HTTP handler 或内部调用方解析后传入，不携带 Gin context；
// 请求取消和截止时间通过 Ctx 传播。
type RequestInput struct {
	// Ctx 取消与截止时间（对应入站请求的 context）。
	Ctx context.Context
	// Method / Path / RawQuery 入站请求的方法与目标
	// （透传类 provider 用 Path 推导上游 endpoint，如 "/v1/messages"）。
	Method   string
	Path     string
	RawQuery string
	// Header 入站请求头（透明代理与客户端伪装提取用）。
	Header http.Header
	// Body 入站请求体（Claude Messages 格式的 JSON）。
	Body []byte
	// Query 入站 URL 查询参数（如 conversation_id）。
	Query func(key string) string

	// ConversationID 是编排流程通过 registry 解析得到的内部会话 ID。
	// llmgate 和 visionlayer 的内部调用不记录会话，传入空串。
	ConversationID string

	// 客户端伪装开关（入口按配置解析）。
	ClaudeCodeDisguise bool
	CodexDisguise      bool
}

// RequestOutcome 返回构造后的上游请求及协议转换元数据。
// 调用方从返回值读取结果，无需从 Gin context 获取转换状态。
type RequestOutcome struct {
	// Request 构造后的上游 http.Request（Header / Body / URL 已就绪）。
	Request *http.Request
	// OriginalBody 入站原始请求体（日志用，与 RequestInput.Body 相同）。
	OriginalBody []byte

	// 以下为 Responses 链的转换产物（其余协议为零值）：

	// PreviousIDDropped 表示转换器为避免服务端链与完整历史叠加而主动
	// 移除了 previous_response_id；响应出口不得再伪造旧 PreviousID 元数据。
	PreviousIDDropped bool
	// HistoryBoundary 表示当前 Responses input 已经成为新的本地历史根；
	// 非原生 Responses 转换器不能把旧 session 追加到它前面，响应持久化
	// 必须替换旧历史而不是按普通轮次合并。
	HistoryBoundary bool
}

// BuildUpstreamRequest 以显式输入构造上游请求（R4-a 主入口）。
// 调用方负责关闭失败时返回的 Request.Body（如有）。
func BuildUpstreamRequest(provider Provider, input RequestInput, upstream *config.UpstreamConfig, apiKey string) (*RequestOutcome, error) {
	if provider == nil {
		return nil, errNilProvider
	}
	return provider.BuildRequest(input, upstream, apiKey)
}

var errNilProvider = configError("provider 未初始化")

func configError(message string) error {
	return &buildRequestError{message: message}
}

type buildRequestError struct {
	message string
}

func (e *buildRequestError) Error() string { return e.message }

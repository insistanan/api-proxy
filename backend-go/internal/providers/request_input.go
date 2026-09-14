// request_input.go 定义 Provider 的显式请求输入与构造产物契约（R4-a）。
//
// 目标：上游调用与核心策略能独立于 HTTP 框架复用——内部调用方
// （llmgate / visionlayer）以 RequestInput 显式传入请求参数，
// 不再伪造 gin 测试上下文；协议 handler 保留的 gin 方法（ConvertToProviderRequest）
// 只是解析 gin 后委托显式实现的薄壳，待调用方迁移完成后退役。
package providers

import (
	"context"
	"net/http"

	"github.com/BenedictKing/api-proxy/internal/config"
)

// RequestInput 一次协议转换的显式请求输入。
// 由入口（HTTP handler）或内部调用方（llmgate / vision）解析后传入；
// 不携带 gin context，取消与截止时间经 Ctx 传播。
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

	// ConversationID 是编排持有的会话决定（registry 解析的内部会话 ID）。
	// 内部调用（llmgate / vision）没有会话观测，传空串。
	ConversationID string

	// 客户端伪装开关（入口按配置解析）。
	ClaudeCodeDisguise bool
	CodexDisguise      bool
}

// RequestOutcome 一次协议转换的显式产物。
// 转换结果由返回值表达，不再写回 gin context。
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

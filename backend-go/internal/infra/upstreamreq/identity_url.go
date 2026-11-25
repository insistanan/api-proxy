package upstreamreq

import (
	"net/url"
	"strings"
)

// BuildUpstreamIdentityURL 以协议的主端点表示共享上游服务。
// 自动补全与完整地址复用相同的拼接规则；同一服务内的流式、计数和压缩操作共用额度。
// 此函数只构造身份，不验证渠道是否可发送；无效地址仍由请求构建层显式拒绝。
func BuildUpstreamIdentityURL(baseURL, protocol string, useFullURL bool) string {
	version, endpoint := "/v1", ""
	switch protocol {
	case "claude":
		endpoint = "/messages"
	case "responses":
		endpoint = "/responses"
	case "openai":
		endpoint = "/chat/completions"
	case "images":
		endpoint = "/images/generations"
	case "embeddings":
		endpoint = "/embeddings"
	case "decisions":
		endpoint = "/decisions"
	case "gemini":
		version, endpoint = "/v1beta", "/models"
	default:
		// 未知协议没有可证明的等价端点，不能借用其他协议的地址规则。
		return baseURL
	}
	target := BuildUpstreamURL(baseURL, version, endpoint, useFullURL)
	if !useFullURL {
		return target
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.RawPath != "" {
		// 不把无法解析或使用特殊路径编码的地址猜成另一个服务。
		return target
	}
	switch protocol {
	case "claude":
		parsed.Path = replaceEndpointSuffix(parsed.Path, "/messages/count_tokens", endpoint)
	case "responses":
		parsed.Path = replaceEndpointSuffix(parsed.Path, "/responses/compact", endpoint)
	case "images":
		parsed.Path = replaceEndpointSuffix(parsed.Path, "/images/edits", endpoint)
	case "gemini":
		index := strings.LastIndex(parsed.Path, "/models/")
		if index >= 0 && (strings.HasSuffix(parsed.Path, ":generateContent") || strings.HasSuffix(parsed.Path, ":streamGenerateContent")) {
			parsed.Path = parsed.Path[:index] + endpoint
			// alt=sse 仅选择流式传输，不改变服务身份。保留其余查询参数的原始写法和顺序。
			parts := strings.Split(parsed.RawQuery, "&")
			kept := parts[:0]
			for _, part := range parts {
				if part != "alt=sse" {
					kept = append(kept, part)
				}
			}
			parsed.RawQuery = strings.Join(kept, "&")
		}
	}
	return parsed.String()
}

func replaceEndpointSuffix(path, suffix, endpoint string) string {
	if strings.HasSuffix(path, suffix) {
		return strings.TrimSuffix(path, suffix) + endpoint
	}
	return path
}

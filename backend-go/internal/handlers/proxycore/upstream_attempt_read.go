// 上游响应读取：大小限制、解压与错误信封前缀回放。
package proxycore

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/insistanan/api-proxy/internal/utils"
)

// maxEnvelopeSniffBytes 是 2xx 错误信封检测的最大读取字节数。错误信封必然
// 很小；正常非流式响应通常也在该上限内（HandleSuccess 本就全量读）；超过
// 上限的巨型响应按读失败处理，走 failover 让其他候选接手。
const maxEnvelopeSniffBytes = 10 * 1024 * 1024

// Images 成功响应可能包含很大的 base64 JSON；只缓冲足以识别常见错误信封的前缀。
const maxImagesEnvelopeSniffBytes = 64 * 1024

// prefixReadCloser 回放已读取的前缀，同时保留原响应体的关闭责任。
type prefixReadCloser struct {
	io.Reader
	io.Closer
}

// readBodyWithLimit 读取响应体，超过 limit 返回错误（与静默截断相比，
// 显式失败能触发 failover 而不是把半个 JSON 交给下游解析）。
func readBodyWithLimit(body io.Reader, limit int64) ([]byte, error) {
	limited, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("读取上游响应体失败: %w", err)
	}
	if int64(len(limited)) > limit {
		return nil, fmt.Errorf("上游响应体超过 %d MB 上限", limit/1024/1024)
	}
	return limited, nil
}

// readUpstreamErrorBody 对线上读取和解压后的错误正文应用同一上限。
// 超限或读取失败立即关闭响应，不排空异常正文，也不把截断内容用于错误分类。
func readUpstreamErrorBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	body, err := readBodyWithLimit(resp.Body, maxEnvelopeSniffBytes)
	if err != nil {
		return nil, err
	}
	body, _, err = utils.DecompressBodyWithLimit(utils.GetContentEncoding(resp.Header), body, maxEnvelopeSniffBytes)
	if err != nil {
		return nil, fmt.Errorf("解压上游错误响应体失败: %w", err)
	}
	return body, nil
}

// readImagesErrorEnvelopePrefix 只缓冲有限前缀。超过预算时立即恢复完整响应流，
// 让 Images 适配器继续处理大块 base64，而不在共享错误信封检查阶段整包缓存。
func readImagesErrorEnvelopePrefix(resp *http.Response) ([]byte, bool, error) {
	prefix, err := io.ReadAll(io.LimitReader(resp.Body, maxImagesEnvelopeSniffBytes+1))
	if int64(len(prefix)) > maxImagesEnvelopeSniffBytes {
		resp.Body = &prefixReadCloser{Reader: io.MultiReader(bytes.NewReader(prefix), resp.Body), Closer: resp.Body}
		return nil, false, nil
	}
	if err != nil {
		resp.Body = &prefixReadCloser{Reader: io.MultiReader(bytes.NewReader(prefix), resp.Body), Closer: resp.Body}
		return nil, false, fmt.Errorf("读取 Images 上游错误信封失败: %w", err)
	}
	return prefix, true, nil
}

// looksLikeJSONResponse 判断上游响应是否声称 JSON 载荷。错误信封只可能是
// JSON；SSE / 图片 / 其他二进制响应直接跳过检测。
func looksLikeJSONResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		return true // 无 Content-Type 时保守检测（body 谓词本身会做 JSON 校验）
	}
	mediaType := contentType
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		mediaType = contentType[:idx]
	}
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

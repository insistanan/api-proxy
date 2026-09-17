package hooks

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/BenedictKing/api-proxy/internal/utils"
)

// runMultipartFormSafety 对 multipart/form-data 上游请求体执行内容安全检查。
//
// 存在这条独立路径的原因：Images 的 /v1/images/edits 与 /v1/images/variations 惯用表单
// 提交，prompt 是普通表单字段而不是 JSON 字段。JSON 路径的 isJSONContentType 会把
// multipart 直接放行，等于这两个端点完全没有内容安全——本函数补上这个缺口。
//
// 检查范围是**全部非文件文本部件**，而不是只看 prompt：字段名由客户端决定，
// 凭据出现在哪个字段都会照样发到上游，因此按字段名白名单过滤等于留后门。
// 文件部件（Content-Disposition 带 filename）跳过——那是图片二进制，
// 逐字节做敏感词/凭据匹配既无意义又昂贵，图片内容由 vision 层负责。
func (h *contentSafetyPreRequestHook) runMultipartFormSafety(
	ctx context.Context,
	metadata HookContext,
	result HookResult,
	snapshot *contentSafetySnapshot,
	protocol string,
	boundary string,
) (HookResult, error) {
	parts, err := utils.ParseMultipartParts(result.RequestBody, boundary)
	if err != nil {
		return result, fmt.Errorf("解析内容安全 multipart 请求体失败: %w", err)
	}

	segments := multipartSafetySegments(parts, protocol)
	if len(segments) == 0 {
		return result, nil
	}

	prompts := make([]string, 0, len(segments))
	// multipart 表单字段不在完整请求体的文本字段里（解码值与原始字节可能不一致），
	// 没有全量前置登记，必须由 applyContentSafetySegments 逐段 ReserveText。
	changed, err := applyContentSafetySegments(ctx, metadata, segments, snapshot, h.recorder, &prompts, false)
	if err != nil {
		return result, err
	}
	if !changed {
		result.Prompts = prompts
		return result, nil
	}

	body, contentType, err := utils.EncodeMultipartParts(parts)
	if err != nil {
		return result, fmt.Errorf("重建内容安全 multipart 请求体失败: %w", err)
	}
	// boundary 由 writer 重新生成，必须同步回请求头：沿用旧 boundary 的上游会读到空表单。
	// 请求体与 Content-Length 由 runAttachedPreRequestHooks 统一回填。
	result.UpstreamRequest.Header.Set("Content-Type", contentType)
	result.RequestBody = body
	result.Prompts = prompts
	return result, nil
}

// multipartSafetySegments 把表单里可检查的文本部件转成 SafetySegment。
// replace 闭包直接写回 parts 元素，因此调用方拿到的 parts 就是改写后的表单内容。
func multipartSafetySegments(parts []utils.MultipartPart, protocol string) []SafetySegment {
	segments := make([]SafetySegment, 0, len(parts))
	for index := range parts {
		part := &parts[index]
		if part.FileName != "" || len(part.Content) == 0 {
			continue
		}
		// 无 filename 却是二进制的部件（例如手写表单直接塞图片字节）没有可读文本，
		// 掩码后写回只会破坏载荷，这里按"非文本"跳过而不是当散文处理。
		if !utf8.Valid(part.Content) {
			continue
		}
		segments = append(segments, SafetySegment{
			Protocol: protocol,
			Source:   safetySourceUser,
			Path:     multipartSegmentPath(part.FormName, index),
			Text:     string(part.Content),
			Mutable:  true,
			replace:  func(text string) { part.Content = []byte(text) },
		})
	}
	return segments
}

// multipartSegmentPath 生成稳定且唯一的片段路径：同名字段可以重复出现，
// 带上部件序号才能让审计去重键区分它们。
func multipartSegmentPath(formName string, index int) string {
	if formName == "" {
		return fmt.Sprintf("form[%d]", index)
	}
	return fmt.Sprintf("form.%s[%d]", formName, index)
}

package utils

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
)

// MultipartBoundary 从 Content-Type 头取出 multipart 表单的 boundary。
// 非 multipart 媒体类型、Content-Type 无法解析、或缺少 boundary 参数时返回 ok=false。
// multipart 请求体的读写（内容安全检查、模型映射改写）一律先走本函数判定，
// 不要再自行 strings.HasPrefix(mediaType, "multipart/") + params["boundary"]。
func MultipartBoundary(contentType string) (string, bool) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", false
	}
	if !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		return "", false
	}
	boundary := params["boundary"]
	if boundary == "" {
		return "", false
	}
	return boundary, true
}

// MultipartPart 是 multipart 表单里的一个部件：原始 MIME 头 + 表单字段名 + 文件名 + 内容。
// FileName 非空表示这是文件部件（Content-Disposition 带 filename），
// 文本检查类逻辑应据此跳过它——文件内容不是用户散文。
type MultipartPart struct {
	Header   textproto.MIMEHeader
	FormName string
	FileName string
	Content  []byte
}

// ParseMultipartParts 按顺序把整张 multipart 表单解析为部件列表，所有部件内容都读入内存。
// 需要重新编码（改写某个字段后转发上游）的调用点用它；
// 只读若干文本字段的调用点用 ReadMultipartTextFields，避免把文件内容也拷进内存。
func ParseMultipartParts(body []byte, boundary string) ([]MultipartPart, error) {
	if boundary == "" {
		return nil, fmt.Errorf("multipart 表单缺少 boundary")
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	parts := make([]MultipartPart, 0, 4)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return parts, nil
		}
		if err != nil {
			return nil, fmt.Errorf("解析 multipart 部件失败: %w", err)
		}
		content, readErr := io.ReadAll(part)
		if closeErr := part.Close(); closeErr != nil && readErr == nil {
			readErr = closeErr
		}
		if readErr != nil {
			return nil, fmt.Errorf("读取 multipart 部件 %q 失败: %w", part.FormName(), readErr)
		}
		parts = append(parts, MultipartPart{
			Header:   cloneMIMEHeader(part.Header),
			FormName: part.FormName(),
			FileName: part.FileName(),
			Content:  content,
		})
	}
}

// EncodeMultipartParts 把部件按给定顺序重新编码为 multipart 表单。
//
// 第二个返回值是**新的** Content-Type：boundary 由 writer 重新生成，与入参表单不同，
// 调用方必须把它同步到请求头，否则上游会按旧 boundary 解析而读到空表单。
func EncodeMultipartParts(parts []MultipartPart) ([]byte, string, error) {
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	for _, part := range parts {
		target, err := writer.CreatePart(part.Header)
		if err != nil {
			writer.Close()
			return nil, "", fmt.Errorf("重建 multipart 部件 %q 失败: %w", part.FormName, err)
		}
		if _, err := target.Write(part.Content); err != nil {
			writer.Close()
			return nil, "", fmt.Errorf("写入 multipart 部件 %q 失败: %w", part.FormName, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("完成 multipart 表单失败: %w", err)
	}
	return out.Bytes(), writer.FormDataContentType(), nil
}

// ReadMultipartTextFields 顺序扫描 multipart 表单，只把 names 登记的**非文件**字段读入内存，
// 其余部件（含文件部件）的内容直接丢弃、不驻留内存。适合"只看几个文本字段"的读路径。
//
// 同名字段重复出现时**首个取胜**（表单语义上后者通常是补充项，观测取第一个即可）；
// 需要覆盖全部同名字段的场景（例如内容安全逐段检查）请用 ParseMultipartParts。
// 返回值只包含实际出现过的字段，缺失字段不出现在 map 里，值不做空白清理。
func ReadMultipartTextFields(body []byte, boundary string, names []string) (map[string]string, error) {
	if boundary == "" {
		return nil, fmt.Errorf("multipart 表单缺少 boundary")
	}
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}

	values := make(map[string]string, len(names))
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return values, nil
		}
		if err != nil {
			return nil, fmt.Errorf("解析 multipart 部件失败: %w", err)
		}
		name := part.FormName()
		_, want := wanted[name]
		if !want || part.FileName() != "" {
			// 不需要的字段与文件部件都不读内容，交由 NextPart 跳过。
			if closeErr := part.Close(); closeErr != nil {
				return nil, fmt.Errorf("关闭 multipart 部件 %q 失败: %w", name, closeErr)
			}
			continue
		}
		if _, exists := values[name]; exists {
			if closeErr := part.Close(); closeErr != nil {
				return nil, fmt.Errorf("关闭 multipart 部件 %q 失败: %w", name, closeErr)
			}
			continue
		}
		content, readErr := io.ReadAll(part)
		if closeErr := part.Close(); closeErr != nil && readErr == nil {
			readErr = closeErr
		}
		if readErr != nil {
			return nil, fmt.Errorf("读取 multipart 字段 %q 失败: %w", name, readErr)
		}
		values[name] = string(content)
	}
}

func cloneMIMEHeader(src textproto.MIMEHeader) textproto.MIMEHeader {
	dst := make(textproto.MIMEHeader, len(src))
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
	return dst
}

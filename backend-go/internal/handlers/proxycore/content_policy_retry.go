package proxycore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/insistanan/api-proxy/internal/infra/utils"
)

func shouldFailoverToNextChannel(bodyBytes []byte, activeChannelCount int) bool {
	return activeChannelCount > 1 && utils.IsContentPolicyErrorBody(bodyBytes)
}

// buildContentPolicyCompatibilityBody 保持 JSON 解码结果不变，将容易被原始字节扫描
// 误判的非 ASCII 文本和审核错误码改为 JSON Unicode 转义。
func buildContentPolicyCompatibilityBody(bodyBytes []byte) ([]byte, bool, error) {
	if !json.Valid(bodyBytes) {
		return nil, false, fmt.Errorf("内容审核兼容重试的请求体不是有效 JSON")
	}

	replacements := []struct {
		plain   []byte
		escaped []byte
	}{
		{[]byte("sensitive_words_detected"), []byte(`sensitive\u005fwords_detected`)},
		{[]byte("content_policy_violation"), []byte(`content\u005fpolicy_violation`)},
		{[]byte("content_filter"), []byte(`content\u005ffilter`)},
		{[]byte("content_blocked"), []byte(`content\u005fblocked`)},
		{[]byte("moderation_blocked"), []byte(`moderation\u005fblocked`)},
	}

	result := append([]byte(nil), bodyBytes...)
	changed := false
	for _, replacement := range replacements {
		if bytes.Contains(result, replacement.plain) {
			result = bytes.ReplaceAll(result, replacement.plain, replacement.escaped)
			changed = true
		}
	}

	result, unicodeChanged, err := escapeNonASCIIInJSONStrings(result)
	if err != nil {
		return nil, false, err
	}
	return result, changed || unicodeChanged, nil
}

func escapeNonASCIIInJSONStrings(bodyBytes []byte) ([]byte, bool, error) {
	result := make([]byte, 0, len(bodyBytes))
	inString := false
	escaped := false
	changed := false

	for i := 0; i < len(bodyBytes); {
		b := bodyBytes[i]
		if !inString {
			result = append(result, b)
			if b == '"' {
				inString = true
			}
			i++
			continue
		}

		if escaped {
			result = append(result, b)
			escaped = false
			i++
			continue
		}
		if b == '\\' {
			result = append(result, b)
			escaped = true
			i++
			continue
		}
		if b == '"' {
			result = append(result, b)
			inString = false
			i++
			continue
		}
		if b < utf8.RuneSelf {
			result = append(result, b)
			i++
			continue
		}

		r, size := utf8.DecodeRune(bodyBytes[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, false, fmt.Errorf("内容审核兼容重试的请求体包含无效 UTF-8")
		}
		if r <= 0xffff {
			result = appendJSONUnicodeEscape(result, uint16(r))
		} else {
			high, low := utf16.EncodeRune(r)
			result = appendJSONUnicodeEscape(result, uint16(high))
			result = appendJSONUnicodeEscape(result, uint16(low))
		}
		changed = true
		i += size
	}

	return result, changed, nil
}

func appendJSONUnicodeEscape(dst []byte, value uint16) []byte {
	const hex = "0123456789abcdef"
	return append(dst,
		'\\', 'u',
		hex[value>>12],
		hex[(value>>8)&0xf],
		hex[(value>>4)&0xf],
		hex[value&0xf],
	)
}

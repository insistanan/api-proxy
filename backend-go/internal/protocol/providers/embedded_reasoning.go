package providers

import (
	"fmt"
	"strings"
)

type reasoningTextPart struct {
	text     string
	thinking bool
}

// embeddedReasoningParser 仅缓存可能属于标签的短尾部，支持任意 SSE 分块边界。
// 只有渠道显式启用标签格式时才使用，普通正文中的标签默认保持原样。
type embeddedReasoningParser struct {
	pending  string
	closeTag string
}

func (p *embeddedReasoningParser) feed(text string) []reasoningTextPart {
	text = p.pending + text
	p.pending = ""
	var parts []reasoningTextPart
	for text != "" {
		tags := []string{"<think>", "<thinking>"}
		thinking := p.closeTag != ""
		if thinking {
			tags = []string{p.closeTag}
		}
		// 只折叠 ASCII 标签字符，避免 Unicode 大小写转换改变字节索引。
		lowerBytes := []byte(text)
		for index, value := range lowerBytes {
			if value >= 'A' && value <= 'Z' {
				lowerBytes[index] = value + ('a' - 'A')
			}
		}
		lower := string(lowerBytes)
		index, tag := -1, ""
		for _, candidate := range tags {
			if found := strings.Index(lower, candidate); found >= 0 && (index < 0 || found < index) {
				index, tag = found, candidate
			}
		}
		if index >= 0 {
			if index > 0 {
				parts = append(parts, reasoningTextPart{text: text[:index], thinking: thinking})
			}
			text = text[index+len(tag):]
			if thinking {
				p.closeTag = ""
			} else {
				p.closeTag = "</" + tag[1:]
			}
			continue
		}
		keep := 0
		for _, candidate := range tags {
			for size := 1; size < len(candidate) && size <= len(lower); size++ {
				if size > keep && strings.HasSuffix(lower, candidate[:size]) {
					keep = size
				}
			}
		}
		if len(text) > keep {
			parts = append(parts, reasoningTextPart{text: text[:len(text)-keep], thinking: thinking})
		}
		p.pending = text[len(text)-keep:]
		break
	}
	return parts
}

func (p *embeddedReasoningParser) finish() ([]reasoningTextPart, error) {
	if p.closeTag != "" {
		return nil, fmt.Errorf("上游嵌入思考标签未闭合，缺少 %s", p.closeTag)
	}
	if p.pending == "" {
		return nil, nil
	}
	parts := []reasoningTextPart{{text: p.pending}}
	p.pending = ""
	return parts, nil
}

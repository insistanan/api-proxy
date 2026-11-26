package tokenusage

import (
	"unicode"
	"unicode/utf8"
)

// IncrementalEstimator 保留字符计数和最多一个未完整的 UTF-8 字符，不保留输出正文。
// 只在查询时累计取整，结果与对拼接全文调用 EstimateTokens 一致。
type IncrementalEstimator struct {
	cjk, other int
	pending    string
}

func (e *IncrementalEstimator) add(r rune) {
	if isCJK(r) {
		e.cjk++
	} else if !unicode.IsSpace(r) {
		e.other++
	}
}

func (e *IncrementalEstimator) WriteString(text string) (int, error) {
	n := len(text)
	for e.pending != "" {
		for len(text) > 0 && !utf8.FullRuneInString(e.pending) {
			e.pending += text[:1]
			text = text[1:]
		}
		if !utf8.FullRuneInString(e.pending) {
			return n, nil
		}
		r, size := utf8.DecodeRuneInString(e.pending)
		e.add(r)
		e.pending = e.pending[size:]
	}
	for len(text) > 0 {
		if !utf8.FullRuneInString(text) {
			e.pending = text
			break
		}
		r, size := utf8.DecodeRuneInString(text)
		e.add(r)
		text = text[size:]
	}
	return n, nil
}

func (e *IncrementalEstimator) Tokens() int {
	other := e.other
	// 未完成的尾字节按 Go range 的 RuneError 语义逐字节计数；不改变后续追加状态。
	other += len(e.pending)
	return int(float64(e.cjk)/1.5 + float64(other)/3.5 + 0.5)
}

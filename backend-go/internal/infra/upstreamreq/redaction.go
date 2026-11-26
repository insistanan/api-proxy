package upstreamreq

import (
	"net/url"
	"sort"
	"strings"
)

// RedactURL 保留定位上游所需的地址，移除认证信息和全部查询参数。
func RedactURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "<invalid-url>"
	}
	parsed.User, parsed.RawQuery, parsed.Fragment, parsed.RawFragment = nil, "", "", ""
	parsed.ForceQuery = false
	return parsed.String()
}

type redactedError struct {
	message string
	cause   error
}

func (e *redactedError) Error() string { return e.message }
func (e *redactedError) Unwrap() error { return e.cause }

// RedactError 处理嵌套网络错误及代理认证；只改变展示文案，保留原始 cause。
func RedactError(err error, rawURLs []string, secrets ...string) error {
	if err == nil {
		return nil
	}
	urls := append([]string(nil), rawURLs...)
	values := append([]string(nil), secrets...)
	var visit func(error)
	visit = func(cause error) {
		if value, ok := cause.(*url.Error); ok {
			urls = append(urls, value.URL)
		}
		switch value := cause.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range value.Unwrap() {
				visit(child)
			}
		case interface{ Unwrap() error }:
			visit(value.Unwrap())
		}
	}
	visit(err)
	// 先替换完整 URL，再按长度替换凭据，避免短密码破坏另一个 URL 的匹配。
	sort.Slice(urls, func(i, j int) bool { return len(urls[i]) > len(urls[j]) })
	message := err.Error()
	for _, raw := range urls {
		if raw == "" {
			continue
		}
		message = strings.ReplaceAll(message, raw, RedactURL(raw))
		if parsed, parseErr := url.Parse(raw); parseErr == nil {
			if parsed.User != nil {
				password, _ := parsed.User.Password()
				values = append(values, parsed.User.String(), password)
			}
			for _, query := range parsed.Query() {
				values = append(values, query...)
			}
		}
	}
	for _, secret := range append([]string(nil), values...) {
		values = append(values, url.QueryEscape(secret), url.PathEscape(secret))
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, secret := range values {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return &redactedError{message: message, cause: err}
}

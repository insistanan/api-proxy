package upstreamcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

// Identity 使用已按协议解析的服务地址，不保留明文 Key，也不推断不同 Key 是否属于同一账号。
type Identity struct {
	URL        string
	Protocol   string
	Credential [32]byte
}

func NewIdentity(targetURL, protocol, key string) Identity {
	targetURL = strings.TrimSpace(targetURL)
	if parsed, err := url.Parse(targetURL); err == nil {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		if parsed.Scheme == "https" && parsed.Port() == "443" || parsed.Scheme == "http" && parsed.Port() == "80" {
			parsed.Host = strings.TrimSuffix(parsed.Host, ":"+parsed.Port())
		}
		if parsed.Host != "" && parsed.Path == "" {
			parsed.Path = "/"
		}
		// HTTP 请求不发送 fragment；配置中的 # 跳过版本标记须在调用前由 URL 构造器处理。
		parsed.Fragment, parsed.RawFragment = "", ""
		targetURL = parsed.String()
	}
	return Identity{URL: targetURL, Protocol: strings.ToLower(strings.TrimSpace(protocol)), Credential: sha256.Sum256([]byte(key))}
}

// Fingerprint 用于持久化 response ID 的归属，不记录明文凭据。
// 新域标记使旧版按原始 BaseURL 计算的归属不能被误认为已经核实了实际服务地址。
func (id Identity) Fingerprint() string {
	payload := append([]byte("upstream-service-v2\x00"+id.URL+"\x00"+id.Protocol+"\x00"), id.Credential[:]...)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

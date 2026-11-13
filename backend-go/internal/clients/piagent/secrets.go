package piagent

import (
	"encoding/json"
	"strings"
)

// MaskSecret 脱敏密钥字符串。
// 长度 <= 8 时只显示"已配置"；长度 > 8 时保留前 4 后 4 字符，中间用 ... 代替。
// 环境引用 {env:VAR} 格式保持不变。
func MaskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if strings.HasPrefix(secret, "{env:") && strings.HasSuffix(secret, "}") {
		return secret
	}
	if len(secret) <= 8 {
		return "已配置"
	}
	return secret[:4] + "..." + secret[len(secret)-4:]
}

// MaskCredentials 递归脱敏 auth.json 中的密钥字段。
// 保持原始 map 结构不变，只替换字符串类型的 key/accessToken/refreshToken 等字段。
func MaskCredentials(raw map[string]json.RawMessage) map[string]json.RawMessage {
	// 对顶层每个 providerID 值对象脱敏
	for id, rawValue := range raw {
		var cred map[string]interface{}
		if err := json.Unmarshal([]byte(rawValue), &cred); err != nil {
			continue
		}
		for _, key := range []string{"key", "accessToken", "refreshToken", "clientSecret", "token"} {
			if val, ok := cred[key]; ok {
				if s, ok := val.(string); ok && s != "" {
					cred[key] = MaskSecret(s)
				}
			}
		}
		if envValues, ok := cred["env"].(map[string]interface{}); ok {
			for envKey, envVal := range envValues {
				if s, ok := envVal.(string); ok && s != "" {
					envValues[envKey] = MaskSecret(s)
				}
			}
		}
		maskedBytes, err := json.Marshal(cred)
		if err != nil {
			continue
		}
		raw[id] = json.RawMessage(maskedBytes)
	}
	return raw
}

// IsSensitiveFile 判断文件类型是否包含敏感数据。models.json 也可能直接保存 apiKey。
func IsSensitiveFile(kind FileKind) bool {
	return kind == FileAuth || kind == FileModels
}

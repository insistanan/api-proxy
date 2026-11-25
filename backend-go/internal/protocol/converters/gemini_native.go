package converters

import (
	"encoding/json"
	"fmt"

	"github.com/insistanan/api-proxy/internal/protocol/types"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// GeminiNativeRequest 只修改明确配置的签名字段，其他原生字段完整保留。
func GeminiNativeRequest(req *types.GeminiRequest, stripSignature, injectSignature bool) ([]byte, error) {
	body := req.RawJSON
	if len(body) == 0 {
		var err error
		body, err = json.Marshal(req)
		if err != nil {
			return nil, err
		}
	}
	if !stripSignature && !injectSignature {
		return body, nil
	}
	for i, content := range gjson.GetBytes(body, "contents").Array() {
		for j, part := range content.Get("parts").Array() {
			path := fmt.Sprintf("contents.%d.parts.%d", i, j)
			var err error
			if stripSignature {
				for _, field := range []string{"thoughtSignature", "thought_signature", "functionCall.thoughtSignature", "functionCall.thought_signature"} {
					body, err = sjson.DeleteBytes(body, path+"."+field)
					if err != nil {
						return nil, err
					}
				}
			} else if part.Get("functionCall").Exists() && !part.Get("thoughtSignature").Exists() && !part.Get("thought_signature").Exists() {
				body, err = sjson.SetBytes(body, path+".thoughtSignature", types.DummyThoughtSignature)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return body, nil
}

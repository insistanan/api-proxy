package types

import (
	"encoding/json"
	"fmt"

	"github.com/insistanan/api-proxy/internal/protocol/schema"
)

func (r *GeminiRequest) UnmarshalJSON(data []byte) error {
	type request GeminiRequest
	var decoded *request
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded == nil {
		return fmt.Errorf("Gemini 请求必须是 JSON 对象")
	}
	*r = GeminiRequest(*decoded)
	r.RawJSON = append(json.RawMessage(nil), data...)
	return nil
}

// JSONSchema 保留声明使用的方言；解码本身不删字段或放宽约束。
func (d GeminiFunctionDeclaration) JSONSchema() interface{} {
	if d.ParametersJSONSchema != nil {
		return d.ParametersJSONSchema
	}
	return schema.FromGemini(d.Parameters)
}

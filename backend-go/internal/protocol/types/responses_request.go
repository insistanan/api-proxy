package types

import (
	"bytes"
	"encoding/json"
)

// UnmarshalJSON 仅对原生 input 保留数字字面值，避免未知业务字段经历史保存后失真。
// 采样、预算等已知字段仍按各自声明的类型解码。
func (r *ResponsesRequest) UnmarshalJSON(data []byte) error {
	type wire ResponsesRequest
	var decoded wire
	var payload struct {
		*wire
		Input json.RawMessage `json:"input"`
	}
	payload.wire = &decoded
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if len(payload.Input) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(payload.Input))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded.Input); err != nil {
			return err
		}
	}
	*r = ResponsesRequest(decoded)
	return nil
}

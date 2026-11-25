package converters

import (
	"encoding/json"
	"insistanan/api-proxy/internal/protocol/types"
)

// 将原有行为用例接到生产分发入口，避免测试旧实现而遗漏真实请求链。
type chatDispatcherFixture struct{}

func (*chatDispatcherFixture) ToProviderRequest(sess *types.Session, req *types.ResponsesRequest) (interface{}, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	converted, err := ConvertResponsesRequestToUpstream("openai", req.Model, body, req.Stream, sess, req, nil)
	if err != nil {
		return nil, err
	}
	result, err := JSONToMap(converted)
	if err != nil {
		return nil, err
	}
	result["messages"], err = normalizeMapSlice(result["messages"])
	return result, err
}

func convertChatResponseFixture(response map[string]interface{}, sessionID string) (*types.ResponsesResponse, error) {
	body, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return ConvertUpstreamResponseToResponses("openai", nil, body, sessionID)
}

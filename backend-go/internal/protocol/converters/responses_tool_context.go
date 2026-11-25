package converters

import "github.com/insistanan/api-proxy/internal/protocol/types"

// ResponseToolContext 是一个候选请求实际使用的工具映射，只读且不跨请求共享。
// 回程必须使用它，才能还原从本地会话恢复的动态工具。
type ResponseToolContext struct{ tools map[string]responseToolSpec }

func NewResponseToolContext(req *types.ResponsesRequest, sess *types.Session) (*ResponseToolContext, error) {
	definitions, err := collectResponsesToolDefinitions(req.Tools, req.Input, sessionResponseItems(sess))
	if err != nil {
		return nil, err
	}
	return &ResponseToolContext{tools: buildResponseToolContext(definitions, nil)}, nil
}

func responseTools(original []byte, contexts ...*ResponseToolContext) map[string]responseToolSpec {
	if len(contexts) > 0 && contexts[0] != nil {
		return contexts[0].tools
	}
	return buildResponseToolContextFromRawRequest(original)
}

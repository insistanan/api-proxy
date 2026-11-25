package images

import (
	"encoding/json"
	"fmt"

	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
	"github.com/insistanan/api-proxy/internal/protocol/types"
)

func responsesImageUsage(raw json.RawMessage) (*types.Usage, error) {
	usage, err := tokenusage.ParseJSONUsage(raw)
	if err != nil {
		return nil, fmt.Errorf("Responses 图片用量不是有效对象: %w", err)
	}
	return usage, nil
}

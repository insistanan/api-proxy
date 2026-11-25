package responses

import (
	"context"
	"encoding/json"
	"strings"

	"insistanan/api-proxy/internal/protocol/codec"
	respitems "insistanan/api-proxy/internal/protocol/items"
	"insistanan/api-proxy/internal/protocol/types"
)

// 交付层仅解码完整 SSE；条目和分片归并由协议层公共收集器负责。
type responsesStreamSessionCollector struct {
	output *respitems.ResponsesOutputCollector
	err    error
}

func newResponsesStreamSessionCollector() *responsesStreamSessionCollector {
	return &responsesStreamSessionCollector{output: respitems.NewResponsesOutputCollector()}
}
func (c *responsesStreamSessionCollector) consumeEvent(event string) {
	if c.err != nil {
		return
	}
	reader := codec.NewSSEEventReader(context.Background(), strings.NewReader(event))
	for reader.Scan() {
		payload := reader.Event().Data
		if payload == "" || payload == codec.SSEDoneMarker {
			continue
		}
		var data map[string]interface{}
		decoder := json.NewDecoder(strings.NewReader(payload))
		decoder.UseNumber()
		if err := decoder.Decode(&data); err != nil {
			c.err = err
			return
		}
		if err := c.output.Consume(data); err != nil {
			c.err = err
			return
		}
	}
	c.err = reader.Err()
}
func (c *responsesStreamSessionCollector) Err() error      { return c.err }
func (c *responsesStreamSessionCollector) Completed() bool { return c.output.Completed() }
func (c *responsesStreamSessionCollector) Items() []types.ResponsesItem {
	items, err := c.output.Items()
	if err != nil {
		c.err = err
	}
	return items
}

package messages

import (
	"encoding/json"
	"testing"
)

func TestCompactionSSEUsageKeepsZerosAndReplacesIterationSnapshot(t *testing.T) {
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(`{"type":"message_delta","usage":{"input_tokens":0,"output_tokens":0,"iterations":[{"type":"compaction","input_tokens":1000,"output_tokens":200,"cache_read_input_tokens":5000}]}}`), &event); err != nil {
		t.Fatal(err)
	}
	has, inputPatch, outputPatch, usage := CheckEventUsageStatusFromData(event, false)
	if !has || inputPatch || outputPatch {
		t.Fatal("合法零值不能触发补估")
	}
	ctx := &Context{}
	updateCollectedUsage(&ctx.UpstreamUsage, usage)
	updateCollectedUsage(&ctx.UpstreamUsage, usage)
	billed := ctx.upstreamUsageSnapshot()
	if billed == nil || billed.InputTokens != 1000 || billed.OutputTokens != 200 || billed.CacheReadInputTokens != 5000 {
		t.Fatalf("迭代丢失或重复: %+v", billed)
	}
	PatchTokensInEventData(event, 20000, 2000, false)
	wire := event["usage"].(map[string]interface{})
	if wire["input_tokens"] != float64(0) || wire["output_tokens"] != float64(0) {
		t.Fatal("客户端零值被覆盖")
	}
}

package tokenusage

import (
	"encoding/json"
	"insistanan/api-proxy/internal/protocol/types"
	"testing"
)

func TestCompactionBillingDoesNotOverwriteClientUsage(t *testing.T) {
	raw := []byte(`{"input_tokens":0,"output_tokens":0,"iterations":[{"type":"compaction","input_tokens":1000,"output_tokens":100,"cache_read_input_tokens":9000},{"type":"message","input_tokens":200,"output_tokens":20,"cache_creation_input_tokens":50}]}`)
	var client types.Usage
	if err := json.Unmarshal(raw, &client); err != nil {
		t.Fatal(err)
	}
	billed := BillingUsage(&client)
	if billed.InputTokens != 1200 || billed.OutputTokens != 120 || billed.CacheReadInputTokens != 9000 || billed.CacheCreationInputTokens != 50 {
		t.Fatalf("计费错误: %+v", billed)
	}
	if client.InputTokens != 0 || client.OutputTokens != 0 || len(client.Iterations) != 2 {
		t.Fatal("改写了客户端窗口用量")
	}
	client.InputTokens, client.OutputTokens = 200, 20
	if repeated := BillingUsage(&client); repeated.InputTokens != 1200 {
		t.Fatal("重复计算顶层与迭代")
	}
	parsed, err := ParseJSONUsage(raw)
	if err != nil || parsed.InputTokens != billed.InputTokens || parsed.OutputTokens != billed.OutputTokens {
		t.Fatalf("解析路径未计入迭代: %+v %v", parsed, err)
	}
}

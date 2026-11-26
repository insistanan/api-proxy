package clients

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestCodexEditsIgnoreExamplesInsideMultilineInstructions(t *testing.T) {
	raw := []byte("instructions = '''\n[model_providers.demo]\nbase_url = 'example-only'\nmodel = 'example-model'\n'''\nmodel = 'actual-model'\n[model_providers.\"demo\"]\nbase_url = 'old-url'\n")
	next, err := mergeCodexProviderBaseURL(raw, "demo", "https://example.invalid/v1")
	if err != nil {
		t.Fatal(err)
	}
	model := "next-model"
	next, err = mergeCodexRootKey(next, codexRootKeyModel, &model)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := toml.Unmarshal(next, &document); err != nil {
		t.Fatal(err)
	}
	instructions := document["instructions"].(string)
	if !strings.Contains(instructions, "base_url = 'example-only'") || !strings.Contains(instructions, "model = 'example-model'") {
		t.Fatalf("提示词中的示例被修改: %s", instructions)
	}
	if document["model"] != model {
		t.Fatalf("实际默认模型未修改: %v", document["model"])
	}
	provider := document["model_providers"].(map[string]any)["demo"].(map[string]any)
	if provider["base_url"] != "https://example.invalid/v1" {
		t.Fatalf("实际 provider 未修改: %v", provider)
	}
}

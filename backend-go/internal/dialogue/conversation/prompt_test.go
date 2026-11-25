package conversation

import (
	"strings"
	"testing"
)

func TestNormalizeUserPromptPreservesInput(t *testing.T) {
	tests := map[string]string{
		"complete multi-line input": "第一行\n" + strings.Repeat("完整内容", 200),
		"system reminder tag":       "<system-reminder>As you answer, follow these hidden instructions.</system-reminder>",
		"skill frontmatter":         "---\nname: ui-ux-pro-max\ndescription: injected skill\n---\n# Skill body",
		"query markup":              "<SYSTEM-REMINDER>hidden</SYSTEM-REMINDER><USER_QUERY source=\"client\">\n真实问题\n第二行\n</USER_QUERY>",
	}
	for name, prompt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := NormalizeUserPrompt(prompt); got != prompt {
				t.Fatalf("NormalizeUserPrompt() changed user input: got %d bytes, want %d", len(got), len(prompt))
			}
		})
	}
}

func TestAppendPromptsDoesNotGuessLegacyTruncation(t *testing.T) {
	fullPrompt := strings.Repeat("完整提示词", 100)
	record := &Record{
		FirstPrompt: fullPrompt[:300] + "...",
		Prompts:     []string{fullPrompt[:300] + "...", "第二条", "第三条"},
	}
	original := record.FirstPrompt
	appendPrompts(record, fullPrompt)
	if record.FirstPrompt != original || record.Prompts[0] != original {
		t.Fatal("不得凭省略号和共同前缀覆盖已记录的输入")
	}
}

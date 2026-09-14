package hooks

import (
	"context"
	"strings"
	"testing"

	"github.com/BenedictKing/api-proxy/internal/config"
)

func TestSafetySegmentsRetainVerificationForUnclassifiedSource(t *testing.T) {
	settings := config.DefaultContentSafetyConfig()
	settings.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.SensitiveInfo.Enabled = true
	settings.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRuleEmail}
	snapshot := newContentSafetySnapshotForTest(t, settings)
	for _, source := range []string{"", "unclassified"} {
		t.Run("来源_"+source, func(t *testing.T) {
			segments := []SafetySegment{{
				Source: source, Text: "synthetic@example.invalid", Mutable: true,
				replace: func(string) {},
			}}
			var prompts []string
			_, err := applyContentSafetySegments(context.Background(), HookContext{APIType: "messages"},
				segments, snapshot, nil, &prompts)
			if err == nil || !strings.Contains(err.Error(), "脱敏后仍命中") {
				t.Fatalf("未经过个人信息检测的来源不能跳过末尾校验: %v", err)
			}
		})
	}
}

package sensitive

import (
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
)

func TestCredentialDetectorDetectsSupportedCredentialShapes(t *testing.T) {
	detector, err := NewCredentialDetector(config.CredentialConfig{
		Enabled: true,
		EnabledRules: []string{
			config.CredentialRuleAPIKey,
			config.CredentialRuleNamedSecret,
			config.CredentialRulePrivateKey,
			config.CredentialRuleConnectionString,
			config.CredentialRuleHighEntropy,
		},
	})
	if err != nil {
		t.Fatalf("创建凭据检测器失败: %v", err)
	}

	tests := []struct {
		name string
		rule string
		text string
	}{
		{name: "API Key", rule: config.CredentialRuleAPIKey, text: "sk-ant-api03-abcdefghijklmnopqrstuvwxyz012345"},
		{name: "Bearer Token", rule: config.CredentialRuleAPIKey, text: "Authorization: Bearer AbCdEf0123456789._-token"},
		{name: "命名密码", rule: config.CredentialRuleNamedSecret, text: "PASSWORD=correct-horse-battery-staple"},
		{name: "连接串", rule: config.CredentialRuleConnectionString, text: "postgresql://admin:s3cret-pass@db.internal/app"},
		{name: "私钥", rule: config.CredentialRulePrivateKey, text: "-----BEGIN PRIVATE KEY-----\nabc123\n-----END PRIVATE KEY-----"},
		{name: "高熵值", rule: config.CredentialRuleHighEntropy, text: "API_KEY=AbCdEf1234567890GhIjKlMnOpQrStUv"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matches := detector.FindAll(test.text)
			found := false
			for _, match := range matches {
				if match.Rule == test.rule {
					found = true
				}
			}
			if !found {
				t.Fatalf("未命中规则 %s: %+v", test.rule, matches)
			}
		})
	}
}

func TestCredentialDetectorIgnoresShortBearerValue(t *testing.T) {
	detector, err := NewCredentialDetector(config.CredentialConfig{
		Enabled:      true,
		EnabledRules: []string{config.CredentialRuleAPIKey},
	})
	if err != nil {
		t.Fatalf("创建凭据检测器失败: %v", err)
	}
	if matches := detector.FindAll("Authorization: Bearer example"); len(matches) != 0 {
		t.Fatalf("短示例值不应被识别为 Bearer Token: %+v", matches)
	}
}

func TestCredentialDetectorIgnoresPlaceholdersAndNormalConfiguration(t *testing.T) {
	detector, err := NewCredentialDetector(config.CredentialConfig{
		Enabled: true,
		EnabledRules: []string{
			config.CredentialRuleNamedSecret,
			config.CredentialRuleHighEntropy,
		},
	})
	if err != nil {
		t.Fatalf("创建凭据检测器失败: %v", err)
	}
	text := strings.Join([]string{
		"PASSWORD=${APP_PASSWORD}",
		"TOKEN=$APP_TOKEN",
		"SECRET=%APP_SECRET%",
		"API_KEY=your-api-key-here",
		"TOKEN=config.Token",
		"TOKEN=tokenValue",
		"PASSWORD=input_password",
		"CLIENT_SECRET=os.Getenv('CLIENT_SECRET')",
		"PASSWORD=/run/secrets/database_password",
		"DATABASE_URL=postgresql://app:${DB_PASSWORD}@db.internal/app",
		"DATABASE_URL=settings.DatabaseURL",
		"CONNECTION_STRING=Server=db.internal;Database=app;Integrated Security=true",
		"GIT_COMMIT=0123456789abcdef0123456789abcdef01234567",
		"IMAGE_DIGEST=AbCDef0123456789+/xyZ9876543210",
		"PATH=/usr/local/bin:/usr/bin",
	}, "\n")
	if matches := detector.FindAll(text); len(matches) != 0 {
		t.Fatalf("正常配置不应被命中: %+v", matches)
	}
}

func TestCredentialDetectorDetectsConnectionAssignmentsWithPasswords(t *testing.T) {
	detector, err := NewCredentialDetector(config.CredentialConfig{
		Enabled:      true,
		EnabledRules: []string{config.CredentialRuleConnectionString},
	})
	if err != nil {
		t.Fatalf("创建连接串检测器失败: %v", err)
	}
	for _, text := range []string{
		"DATABASE_URL=redis://:s3cret-pass@localhost:6379/0",
		"CONNECTION_STRING=Server=db.internal;Database=app;User ID=admin;Password=s3cret-pass",
	} {
		if matches := detector.FindAll(text); len(matches) == 0 || matches[0].Rule != config.CredentialRuleConnectionString {
			t.Fatalf("未识别含密码的连接配置 %q: %+v", text, matches)
		}
	}
}

func TestCredentialDetectorMaskUsesTypedMarker(t *testing.T) {
	detector, err := NewCredentialDetector(config.CredentialConfig{
		Enabled:      true,
		EnabledRules: []string{config.CredentialRuleAPIKey},
	})
	if err != nil {
		t.Fatalf("创建凭据检测器失败: %v", err)
	}
	masked, matches := detector.Mask("token=sk-1234567890abcdefghijklmnop")
	if masked != "token=[MASKED_CREDENTIAL:api_key]" || len(matches) != 1 {
		t.Fatalf("凭据掩码错误: %q %+v", masked, matches)
	}
}

func TestCredentialDetectorKeepsBusinessIDsOutOfSecretRules(t *testing.T) {
	detector, err := NewCredentialDetector(config.CredentialConfig{
		Enabled: true,
		EnabledRules: []string{
			config.CredentialRuleNamedSecret,
			config.CredentialRuleHighEntropy,
		},
	})
	if err != nil {
		t.Fatalf("创建凭据检测器失败: %v", err)
	}
	text := strings.Join([]string{
		"conversation_id=AbCdEf1234567890GhIjKlMnOpQrStUv",
		"sessionId=550e8400-e29b-41d4-a716-446655440000",
		"order_id=123456789012345678",
		"token=550e8400-e29b-41d4-a716-446655440000",
	}, "\n")
	if matches := detector.FindAll(text); len(matches) != 0 {
		t.Fatalf("业务 ID 不应被掩码: %+v", matches)
	}
}

func TestCredentialDetectorStillMasksExplicitSecretFields(t *testing.T) {
	detector, err := NewCredentialDetector(config.CredentialConfig{
		Enabled:      true,
		EnabledRules: []string{config.CredentialRuleHighEntropy},
	})
	if err != nil {
		t.Fatalf("创建凭据检测器失败: %v", err)
	}
	if matches := detector.FindAll("API_KEY=AbCdEf1234567890GhIjKlMnOpQrStUv"); len(matches) != 1 {
		t.Fatalf("明确密钥字段应被识别: %+v", matches)
	}
}

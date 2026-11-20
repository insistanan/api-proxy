package config

import (
	"encoding/json"
	"testing"
)

func clearManagedEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"ENABLE_REQUEST_LOGS", "ENABLE_RESPONSE_LOGS", "REQUEST_TIMEOUT", "RESPONSE_HEADER_TIMEOUT",
		"STREAM_IDLE_TIMEOUT", "MAX_REQUEST_BODY_SIZE_MB", "METRICS_RETENTION_DAYS",
		"METRICS_PERSISTENCE_ENABLED", "LOG_DB_PATH", "ENABLE_CORS", "CORS_ORIGIN",
		"CLAUDE_CODE_DISGUISE_VERSION", "CODEX_DISGUISE_VERSION",
	} {
		t.Setenv(key, "")
	}
}

func TestManagedSettingsMigration(t *testing.T) {
	clearManagedEnv(t)
	t.Setenv("ENABLE_REQUEST_LOGS", "false")
	t.Setenv("ENABLE_RESPONSE_LOGS", "false")
	t.Setenv("METRICS_RETENTION_DAYS", "30")
	t.Setenv("REQUEST_TIMEOUT", "600000")
	t.Setenv("STREAM_IDLE_TIMEOUT", "900")
	t.Setenv("LOG_DB_PATH", "custom/logs.db")
	t.Setenv("CLAUDE_CODE_DISGUISE_VERSION", "7.1.1")

	for _, raw := range []string{`{}`, `{"settings":{}}`} {
		var candidate Config
		changed, err := migrateManagedSettings(&candidate, []byte(raw))
		if err != nil || !changed {
			t.Fatalf("迁移失败: changed=%v err=%v", changed, err)
		}
		gateway := candidate.Settings.Gateway
		if gateway.EnableRequestLogs || gateway.EnableResponseLogs || gateway.MetricsRetentionDays != 30 || gateway.RequestTimeout != 600000 || gateway.StreamIdleTimeout != 900 || gateway.LogDBPath != "custom/logs.db" {
			t.Fatalf("旧部署策略未保留: %+v", gateway)
		}
		if candidate.Settings.Disguise.ClaudeCodeVersion != "7.1.1" || !candidate.Settings.Disguise.AutoUpdateFromRegistry {
			t.Fatal("旧版本迁移或缺失 settings 默认值错误")
		}
		// 迁移结果持久化后，旧 env 再变化也不能覆盖用户设置。
		encoded, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("CLAUDE_CODE_DISGUISE_VERSION", "8.1.1")
		t.Setenv("METRICS_RETENTION_DAYS", "3")
		var restored Config
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		changed, err = migrateManagedSettings(&restored, encoded)
		if err != nil || changed || restored.Settings.Disguise.ClaudeCodeVersion != "7.1.1" || restored.Settings.Gateway.MetricsRetentionDays != 30 {
			t.Fatalf("重复迁移改写了已保存设置: changed=%v err=%v", changed, err)
		}
		t.Setenv("CLAUDE_CODE_DISGUISE_VERSION", "7.1.1")
		t.Setenv("METRICS_RETENTION_DAYS", "30")
	}
}

func TestManagedSettingsPreserveExplicitValues(t *testing.T) {
	clearManagedEnv(t)
	t.Setenv("CLAUDE_CODE_DISGUISE_VERSION", "7.1.1")
	t.Setenv("ENABLE_REQUEST_LOGS", "true")
	data := []byte(`{"settings":{"gateway":{"enableRequestLogs":false},"disguise":{"claudeCodeVersion":"","codexVersion":"","autoUpdateFromRegistry":false}}}`)
	var candidate Config
	if err := json.Unmarshal(data, &candidate); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateManagedSettings(&candidate, data); err != nil {
		t.Fatal(err)
	}
	if candidate.Settings.Gateway.EnableRequestLogs || candidate.Settings.Disguise.AutoUpdateFromRegistry || candidate.Settings.Disguise.ClaudeCodeVersion != "" {
		t.Fatal("显式 false 或空版本不得被旧 env 或默认值覆盖")
	}
}

func TestManagedSettingsRejectInvalidEnv(t *testing.T) {
	clearManagedEnv(t)
	t.Setenv("REQUEST_TIMEOUT", "invalid")
	var candidate Config
	if _, err := migrateManagedSettings(&candidate, []byte(`{}`)); err == nil {
		t.Fatal("无效旧配置必须返回错误，不能悄悄回退到默认超时")
	}
}

func TestLogPolicyMigrationPreservesExistingCollection(t *testing.T) {
	clearManagedEnv(t)
	for _, enabled := range []bool{false, true} {
		raw, err := json.Marshal(map[string]any{"settings": map[string]any{"gateway": map[string]any{"enableRequestLogs": enabled, "enableResponseLogs": enabled}}})
		if err != nil {
			t.Fatal(err)
		}
		var candidate Config
		if _, err := migrateManagedSettings(&candidate, raw); err != nil {
			t.Fatal(err)
		}
		gateway := candidate.Settings.Gateway
		if gateway.EnableRequestLogs != enabled || gateway.EnableResponseLogs != enabled || gateway.LogCaptureMode != "bounded" || gateway.LogBodyLimitKB != 1024 || gateway.LogRuntimeRetentionDays != 2 || gateway.LogCallRetentionDays != 2 || gateway.LogBodyRetentionDays != 2 || gateway.LogBudgetMB != 0 {
			t.Fatalf("日志迁移改变原行为: %+v", gateway)
		}
	}
}

func TestRejectInvalidLogPolicy(t *testing.T) {
	for _, change := range []func(*GatewaySettings){
		func(s *GatewaySettings) { s.LogCaptureMode = "full" },
		func(s *GatewaySettings) { s.LogBodyLimitKB = 2048 },
		func(s *GatewaySettings) { s.LogBodyRetentionDays = 3 },
		func(s *GatewaySettings) { s.LogCallRetentionDays = 0 },
		func(s *GatewaySettings) { s.LogRuntimeRetentionDays = 366 },
		func(s *GatewaySettings) { s.LogBudgetMB = 1 },
	} {
		settings := DefaultGatewaySettings()
		change(&settings)
		if err := ValidateGatewaySettings(settings); err == nil {
			t.Fatalf("无效日志策略未被拒绝: %+v", settings)
		}
	}
}

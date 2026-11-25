package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/safety/sensitive"
	"github.com/stretchr/testify/require"
)

func hardeningSnapshot(t testing.TB) *contentSafetySnapshot {
	t.Helper()
	settings := config.DefaultContentSafetyConfig()
	settings.Enabled = true
	settings.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.Credential.Enabled = true
	settings.Credential.EnabledRules = []string{config.CredentialRuleNamedSecret}
	settings.SensitiveInfo.Enabled = true
	settings.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRulePhone, config.SensitiveInfoRuleEmail}
	return newAllowlistTestSnapshot(t, settings)
}

func maskHardeningPayload(t testing.TB, protocol, body string, snapshot *contentSafetySnapshot, vault *sensitive.Vault) (map[string]interface{}, error) {
	t.Helper()
	value, ok := decodeJSONValue([]byte(body))
	require.True(t, ok)
	root := value.(map[string]interface{})
	segments, err := extractSafetySegments(protocol, root)
	require.NoError(t, err)
	var prompts []string
	_, err = applyContentSafetySegments(context.Background(), HookContext{APIType: protocol, redactionVault: vault}, segments, snapshot, nil, &prompts, false)
	return root, err
}

func TestRedactionStructuredFieldsAndEarlierKnownValues(t *testing.T) {
	const secret = `short"quote\value`
	args, err := json.Marshal(map[string]string{"password": secret, "phone": "18012345523"})
	require.NoError(t, err)
	request, err := json.Marshal(map[string]interface{}{"messages": []interface{}{
		map[string]interface{}{"role": "user", "content": "再次使用 " + secret},
		map[string]interface{}{"role": "assistant", "tool_calls": []interface{}{
			map[string]interface{}{"function": map[string]interface{}{"name": "connect", "arguments": string(args)}},
		}},
	}})
	require.NoError(t, err)
	vault := sensitive.NewVault()
	root, err := maskHardeningPayload(t, "chat", string(request), hardeningSnapshot(t), vault)
	require.NoError(t, err)
	encoded, err := json.Marshal(root)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "18012345523")
	restored, err := vault.RestoreJSON(encoded)
	require.NoError(t, err)
	require.JSONEq(t, string(request), string(restored))
	messages := root["messages"].([]interface{})
	require.NotContains(t, messages[0].(map[string]interface{})["content"], secret)
}

func TestRedactionRejectsSensitiveNumbersAndSignedHistory(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"connect","input":{"phone":18012345523}}]}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"password=K8p!rQ7vZ2","signature":"opaque"}]}]}`,
	} {
		_, err := maskHardeningPayload(t, "messages", body, hardeningSnapshot(t), sensitive.NewVault())
		require.NotNil(t, ContentSafetyErrorFrom(err))
	}
}

func TestRedactionResponsesOutputAndToolDescriptions(t *testing.T) {
	const body = `{"tools":[{"type":"function","name":"connect","description":"password=K8p!rQ7vZ2"}],"input":[{"role":"assistant","content":[{"type":"output_text","text":"再次使用 K8p!rQ7vZ2"}]}]}`
	root, err := maskHardeningPayload(t, "responses", body, hardeningSnapshot(t), sensitive.NewVault())
	require.NoError(t, err)
	encoded, err := json.Marshal(root)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "K8p!rQ7vZ2")
}

func TestRedactionMemoDoesNotFreezeKnownValueCoverage(t *testing.T) {
	snapshot := hardeningSnapshot(t)
	vault := sensitive.NewVault()
	text := strings.Repeat("普通历史内容。", 64) + " K8p!rQ7vZ2"
	request, err := json.Marshal(map[string]interface{}{"input": text})
	require.NoError(t, err)
	_, err = maskHardeningPayload(t, "responses", string(request), snapshot, vault)
	require.NoError(t, err)
	_, err = maskHardeningPayload(t, "responses", `{"input":"password=K8p!rQ7vZ2"}`, snapshot, vault)
	require.NoError(t, err)
	root, err := maskHardeningPayload(t, "responses", string(request), snapshot, vault)
	require.NoError(t, err)
	require.NotContains(t, root["input"], "K8p!rQ7vZ2")
}

func TestRedactionRejectsForgedPlaceholderAndKeepsEscapedNestedJSON(t *testing.T) {
	_, err := maskHardeningPayload(t, "responses", `{"input":"MASKED_SECRET_AAAAAAAAAAAAA_END"}`, hardeningSnapshot(t), sensitive.NewVault())
	require.ErrorContains(t, err, "未获会话授权")
	inner, err := json.Marshal(map[string]string{"password": "short\"quote"})
	require.NoError(t, err)
	arguments, err := json.Marshal(map[string]string{"payload": string(inner)})
	require.NoError(t, err)
	request, err := json.Marshal(map[string]interface{}{"messages": []interface{}{
		map[string]interface{}{"role": "assistant", "tool_calls": []interface{}{
			map[string]interface{}{"function": map[string]interface{}{"name": "write", "arguments": string(arguments)}},
		}},
	}})
	require.NoError(t, err)
	body := string(request)
	vault := sensitive.NewVault()
	root, err := maskHardeningPayload(t, "chat", body, hardeningSnapshot(t), vault)
	require.NoError(t, err)
	encoded, err := json.Marshal(root)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "short")
	restored, err := vault.RestoreJSON(encoded)
	require.NoError(t, err)
	require.JSONEq(t, body, string(restored))
}

func TestRedactionGeminiBusinessNamesRestoreWithoutProtocolRewrite(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	vault := sensitive.NewVault()
	placeholder := maskTestValue(t, vault, "SECRET", "private-value")
	attached := &attachedHookPipeline{pipeline: NewPipeline(), vault: vault, stream: sensitive.NewStreamCmdScanner(nil)}
	c.Set(contentSafetyPipelineKey, attached)
	event := `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"tool","args":{"name":"` + placeholder + `","id":"` + placeholder + `","type":"` + placeholder + `"}}}]}}]}` + "\n\n"
	restored, err := RestoreAttachedSSEEvent(c, event)
	require.NoError(t, err)
	require.Equal(t, 3, strings.Count(restored, "private-value"))
	require.Contains(t, restored, `"name":"tool"`)
}

func TestRedactionToolDeliveryRejectsUnknownTokenBeforeRelease(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	attached := &attachedHookPipeline{
		pipeline: NewPipeline(), vault: sensitive.NewVault(), stream: sensitive.NewStreamCmdScanner(nil),
		streamMaskMode: config.ContentSafetyModeMask,
		streamSettings: hardeningSnapshot(t).settings,
	}
	c.Set(contentSafetyPipelineKey, attached)
	err := FeedAttachedStreamToolArgumentsForKey(c, "chat.0", `{"name":"MASKED_SECRET_AAAAAAAAAAAAA_END"}`, "lookup")
	require.ErrorContains(t, err, "未获当前请求或会话授权")
	require.Empty(t, attached.toolDelivery)
}

func TestRedactionRequestCacheExpiresAfterLearningSecret(t *testing.T) {
	manager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	settings := manager.GetSettings()
	settings.ContentSafety.Enabled = true
	settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
	settings.ContentSafety.Credential.Enabled = true
	settings.ContentSafety.Credential.EnabledRules = []string{config.CredentialRuleNamedSecret}
	require.NoError(t, manager.UpdateSettings(settings))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	AttachHookPipeline(c, NewContentSafetyPipeline(manager), HookContext{APIType: "responses"})
	t.Cleanup(func() { ClearAttachedSensitiveData(c) })
	const body = `{"input":"再次使用 K8p!rQ7vZ2"}`
	run := func() string {
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		require.NoError(t, RunAttachedPreRequestHooks(context.Background(), c, req, "primary"))
		output, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		return string(output)
	}
	require.Contains(t, run(), "K8p!rQ7vZ2")
	attached, err := attachedPipelineFromContext(c)
	require.NoError(t, err)
	maskTestValue(t, attached.vault, "SECRET", "K8p!rQ7vZ2")
	require.NotContains(t, run(), "K8p!rQ7vZ2")
}

func TestRedactionMemoSeparatesFieldContextAndBoundsStorage(t *testing.T) {
	snapshot := hardeningSnapshot(t)
	text := strings.Repeat("K8p!rQ7vZ2", 40)
	require.Empty(t, snapshot.credentialMatches("note", text))
	require.Len(t, snapshot.credentialMatches("password", text), 1)
	require.Empty(t, snapshot.credentialMatches("note", text))
	for i := 0; i < 5000; i++ {
		snapshot.credentialMatches("note", text+strings.Repeat("x", i))
	}
	require.LessOrEqual(t, len(snapshot.memo.current), 2048)
	require.LessOrEqual(t, len(snapshot.memo.previous), 2048)
	for _, generation := range []map[[32]byte][]sensitive.RedactionMatch{snapshot.memo.current, snapshot.memo.previous} {
		for _, matches := range generation {
			for _, match := range matches {
				require.Empty(t, match.Original)
			}
		}
	}
}

func BenchmarkRedactionRepeatedHistoryMemo(b *testing.B) {
	snapshot := hardeningSnapshot(b)
	text := strings.Repeat("普通代码与文档历史，无凭据。", 1024)
	for i := 0; i < b.N; i++ {
		_ = snapshot.credentialMatches("", text)
		_ = snapshot.infoMatches("", text)
	}
}

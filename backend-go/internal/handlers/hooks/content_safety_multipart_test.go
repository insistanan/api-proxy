package hooks

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/insistanan/api-proxy/internal/config"
	"github.com/insistanan/api-proxy/internal/utils"
)

// buildImagesEditForm 构造 /v1/images/edits 形态的表单：prompt 文本字段 + 图片文件部件。
func buildImagesEditForm(t *testing.T, prompt string) ([]byte, string, []byte) {
	t.Helper()
	fileBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0xFF, 0xFE}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("model", "gpt-image-1"); err != nil {
		t.Fatalf("写入 model 字段失败: %v", err)
	}
	if err := writer.WriteField("prompt", prompt); err != nil {
		t.Fatalf("写入 prompt 字段失败: %v", err)
	}
	filePart, err := writer.CreateFormFile("image", "cat.png")
	if err != nil {
		t.Fatalf("创建文件部件失败: %v", err)
	}
	if _, err := filePart.Write(fileBytes); err != nil {
		t.Fatalf("写入文件内容失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭表单失败: %v", err)
	}
	return buffer.Bytes(), writer.FormDataContentType(), fileBytes
}

func newImagesSafetyContext(t *testing.T, mutate func(*config.SettingsConfig)) *gin.Context {
	t.Helper()
	configManager, err := config.NewConfigManager(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { configManager.Close() })

	settings := configManager.GetSettings()
	mutate(&settings)
	if err := configManager.UpdateSettings(settings); err != nil {
		t.Fatalf("更新内容安全设置失败: %v", err)
	}

	pipeline := NewContentSafetyPipeline(configManager)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	AttachHookPipeline(c, pipeline, HookContext{APIType: "images", Model: "gpt-image-1"})
	return c
}

func TestMultipartContentSafetyMasksPromptAndSyncsBoundary(t *testing.T) {
	c := newImagesSafetyContext(t, func(settings *config.SettingsConfig) {
		settings.ContentSafety.SensitiveWord.Enabled = false
		settings.ContentSafety.SensitiveInfo.Enabled = true
		settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
		settings.ContentSafety.SensitiveInfo.Mode = config.ContentSafetyModeMask
		settings.ContentSafety.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRulePhone}
	})

	body, contentType, fileBytes := buildImagesEditForm(t, "把电话号码 13800138000 写在招牌上")
	originalBoundary, _ := utils.MultipartBoundary(contentType)
	req := httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)

	if err := runAttachedPreRequestHooks(context.Background(), c, req, "images-channel", "images"); err != nil {
		t.Fatalf("请求前 Hook 执行失败: %v", err)
	}

	newContentType := req.Header.Get("Content-Type")
	newBoundary, ok := utils.MultipartBoundary(newContentType)
	if !ok {
		t.Fatalf("改写后的 Content-Type 无 boundary: %s", newContentType)
	}
	if newBoundary == originalBoundary {
		t.Fatal("表单被改写后 Content-Type 的 boundary 必须同步更新，否则上游读到空表单")
	}

	rewritten, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("读取改写后的请求体失败: %v", err)
	}
	if req.ContentLength != int64(len(rewritten)) {
		t.Fatalf("ContentLength = %d，实际长度 = %d", req.ContentLength, len(rewritten))
	}

	parts, err := utils.ParseMultipartParts(rewritten, newBoundary)
	if err != nil {
		t.Fatalf("解析改写后的表单失败: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("改写后部件数量 = %d，期望 3", len(parts))
	}
	if !strings.Contains(string(parts[1].Content), "MASKED_PHONE_") || strings.Contains(string(parts[1].Content), "13800138000") {
		t.Errorf("prompt 字段未掩码: %q", parts[1].Content)
	}
	if parts[2].FileName != "cat.png" || !bytes.Equal(parts[2].Content, fileBytes) {
		t.Errorf("图片部件必须原样保留: %+v", parts[2])
	}
}

func TestMultipartContentSafetyBlocksSensitiveWordInFormField(t *testing.T) {
	c := newImagesSafetyContext(t, func(settings *config.SettingsConfig) {
		settings.ContentSafety.SensitiveWord.Enabled = true
		settings.ContentSafety.SensitiveWord.CustomWords = []string{"违禁提示"}
		settings.ContentSafety.SensitiveInfo.Enabled = false
		settings.ContentSafety.Credential.Enabled = false
	})

	body, contentType, _ := buildImagesEditForm(t, "画一张含违禁提示的海报")
	req := httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)

	err := runAttachedPreRequestHooks(context.Background(), c, req, "images-channel", "images")
	var safetyErr *ContentSafetyError
	if !errors.As(err, &safetyErr) {
		t.Fatalf("multipart 表单里的敏感词未被拦截: %v", err)
	}
	if safetyErr.Code() != "SENSITIVE_WORD_BLOCKED" {
		t.Fatalf("拦截类型 = %s，期望 SENSITIVE_WORD_BLOCKED", safetyErr.Code())
	}
}

// 表单字段名由客户端决定，凭据放在哪个字段都会照样发到上游，
// 因此检查范围必须覆盖全部非文件文本字段，而不是只看 prompt。
func TestMultipartContentSafetyScansNonPromptTextFields(t *testing.T) {
	c := newImagesSafetyContext(t, func(settings *config.SettingsConfig) {
		settings.ContentSafety.SensitiveWord.Enabled = false
		settings.ContentSafety.SensitiveInfo.Enabled = false
		settings.ContentSafety.Credential.Enabled = true
		settings.ContentSafety.Credential.EnabledRules = []string{config.CredentialRuleNamedSecret}
		settings.ContentSafety.Credential.UserInputMode = config.ContentSafetyModeBlock
	})

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("prompt", "普通提示"); err != nil {
		t.Fatalf("写入 prompt 字段失败: %v", err)
	}
	if err := writer.WriteField("metadata", "API_KEY=sk-1234567890abcdefghijklmnop"); err != nil {
		t.Fatalf("写入 metadata 字段失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭表单失败: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(buffer.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())

	err := runAttachedPreRequestHooks(context.Background(), c, req, "images-channel", "images")
	var safetyErr *ContentSafetyError
	if !errors.As(err, &safetyErr) || safetyErr.Code() != "CREDENTIAL_BLOCKED" {
		t.Fatalf("非 prompt 字段中的凭据未被拦截: %v", err)
	}
}

func TestMultipartContentSafetyKeepsBytesWhenNothingMatches(t *testing.T) {
	c := newImagesSafetyContext(t, func(settings *config.SettingsConfig) {
		settings.ContentSafety.SensitiveWord.Enabled = false
		settings.ContentSafety.SensitiveInfo.Enabled = true
		settings.ContentSafety.SensitiveData.Mode = config.ContentSafetyModeMask
		settings.ContentSafety.SensitiveInfo.Mode = config.ContentSafetyModeMask
		settings.ContentSafety.SensitiveInfo.EnabledRules = []string{config.SensitiveInfoRulePhone}
	})

	body, contentType, _ := buildImagesEditForm(t, "画一只戴帽子的猫")
	req := httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)

	if err := runAttachedPreRequestHooks(context.Background(), c, req, "images-channel", "images"); err != nil {
		t.Fatalf("请求前 Hook 执行失败: %v", err)
	}
	if got := req.Header.Get("Content-Type"); got != contentType {
		t.Errorf("无命中时不应更换 Content-Type: %s", got)
	}
	forwarded, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("读取转发请求体失败: %v", err)
	}
	if !bytes.Equal(forwarded, body) {
		t.Error("无命中时表单必须逐字节原样转发")
	}
}

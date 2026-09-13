package hooks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/BenedictKing/api-proxy/internal/config"
	"github.com/BenedictKing/api-proxy/internal/sensitive"
	"github.com/BenedictKing/api-proxy/internal/utils"
)

// ContentSafetyError 是请求前内容安全 Hook 的可识别错误。
type ContentSafetyError struct {
	BlockType string
	RuleName  string
	Snippet   string
	// Command 是危险命令命中时的完整命令（拦截记录展示与名单操作用；
	// 非命令类拦截为空）。与 Snippet 中的嵌入展示互补，结构化字段。
	Command     string
	CommandHash string
	AIReview    *sensitive.CommandAIReview
	audited     bool
}

// ContentSafetyHookError 表示内容安全基础设施自身失败，而不是策略命中。
// 上游故障统计必须把它视为本地中性错误，不能降级渠道或 API Key。
type ContentSafetyHookError struct {
	Err error
}

func (e *ContentSafetyHookError) Error() string {
	if e == nil || e.Err == nil {
		return "内容安全检查失败"
	}
	return fmt.Sprintf("内容安全检查失败: %v", e.Err)
}

func (e *ContentSafetyHookError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func wrapContentSafetyHookError(err error) error {
	if err == nil {
		return nil
	}
	var safetyErr *ContentSafetyError
	if errors.As(err, &safetyErr) {
		return err
	}
	var hookErr *ContentSafetyHookError
	if errors.As(err, &hookErr) {
		return err
	}
	return &ContentSafetyHookError{Err: err}
}

// ContentSafetyErrorFrom 从错误链中提取内容安全拦截错误；不存在时返回 nil。
func ContentSafetyErrorFrom(err error) *ContentSafetyError {
	var target *ContentSafetyError
	if errors.As(err, &target) {
		return target
	}
	return nil
}

func ContentSafetyHookErrorFrom(err error) *ContentSafetyHookError {
	var target *ContentSafetyHookError
	if errors.As(err, &target) {
		return target
	}
	return nil
}

func (e *ContentSafetyError) Error() string {
	if e == nil {
		return "内容安全检查失败"
	}
	if e.RuleName == "" {
		return fmt.Sprintf("请求被内容安全策略拦截 (%s)", e.BlockType)
	}
	return fmt.Sprintf("请求被内容安全策略拦截 (%s: %s)", e.BlockType, e.RuleName)
}

// Code 返回稳定的客户端错误码，协议适配层可据此生成各自格式的响应。
func (e *ContentSafetyError) Code() string {
	if e != nil {
		switch e.BlockType {
		case sensitive.BlockTypeSensitiveWord:
			return "SENSITIVE_WORD_BLOCKED"
		case sensitive.BlockTypeSensitiveInfo:
			return "SENSITIVE_INFORMATION_BLOCKED"
		case sensitive.BlockTypeDangerousCmd, sensitive.BlockTypeDangerousCmdAI:
			return "DANGEROUS_COMMAND_BLOCKED"
		case sensitive.BlockTypeCredential:
			return "CREDENTIAL_BLOCKED"
		}
	}
	return "CONTENT_SAFETY_BLOCKED"
}

const contentSafetyPipelineKey = "__content_safety_hook_pipeline"

type attachedHookPipeline struct {
	pipeline            *Pipeline
	vault               *sensitive.Vault
	metadataMu          sync.RWMutex
	metadata            HookContext
	streamMu            sync.Mutex
	stream              *sensitive.StreamCmdScanner
	textStreams         map[string]*streamTextState
	textStreamBytes     int
	toolArgs            map[string]*streamToolArgumentState
	toolArgBytes        int
	restorers           map[string]*sensitive.StreamRestorer
	restorePending      map[string]streamRestoreTemplate
	restorePendingBytes int
	restoreEventBytes   int
	restoreErr          error
	vetoGuard           *StreamVetoGuard
	lifecycleCtx        context.Context
	lifecycleCancel     context.CancelFunc
	// allowedCommands 记录本请求中已放行的完整命令身份（streamMu 保护），
	// 避免同一完整脚本的多条规则重复送审和注册否决窗口。
	allowedCommands map[string]struct{}
	// streamCredential / streamMaskMode 在流式扫描器初始化时从配置快照固化
	//（streamMu 保护）。流式生命周期内配置热重载不再生效——与 stream 扫描器
	// 同一约定；换来的是凭据扫描不再逐 chunk 取配置快照（深拷贝 + DeepEqual）。
	streamCredential *sensitive.CredentialDetector
	streamMaskMode   string
	streamSettings   config.ContentSafetyConfig
	streamDetector   *sensitive.CmdDetector
	// preRequestMu 保护 preRequestCache：同一请求的请求前钩子在一次代理里
	// 会执行多次（入口 + vision 层前后 + 故障转移重试），按输入体指纹缓存
	// 处理结果，重复执行直接复用，避免多次 JSON 解析与全量正则扫描。
	// 配置热重载在请求中途不生效——同一请求内保持一致的处置是更可预期的
	// 语义，变更自下一请求起生效。
	preRequestMu       sync.Mutex
	preRequestCache    *attachedPreRequestCache
	preRequestSnapshot *contentSafetySnapshot
	preRequestPrepared bool
	preRequestEnabled  bool
}

// attachedPreRequestCache 是请求级请求前钩子的结果缓存。
type attachedPreRequestCache struct {
	inputSHA          [sha256.Size]byte
	protocol          string // payloadProtocols[0]，空表示默认
	output            []byte // 处理后的请求体
	changed           bool   // 处理是否改动了请求体
	contentType       string
	outputContentType string
}

type checkedPreRequestBody struct {
	*bytes.Reader
	owner    *attachedHookPipeline
	protocol string
	contentType string
	size int
}

func (b *checkedPreRequestBody) Close() error { return nil }

func (a *attachedHookPipeline) preparePreRequest() (bool, error) {
	a.preRequestMu.Lock()
	defer a.preRequestMu.Unlock()
	if a.preRequestPrepared {
		return a.preRequestEnabled, nil
	}
	enabled := false
	for _, entry := range a.pipeline.snapshot(HookStagePreRequest) {
		if hook, ok := entry.hook.(*contentSafetyPreRequestHook); ok {
			snapshot, err := hook.currentSnapshot()
			if err != nil {
				return false, err
			}
			a.preRequestSnapshot = snapshot
			enabled = enabled || snapshot.requestEnabled()
		} else {
			enabled = true
		}
	}
	a.preRequestPrepared, a.preRequestEnabled = true, enabled
	return enabled, nil
}

func (s *contentSafetySnapshot) requestEnabled() bool {
	return s.settings.SensitiveWord.Enabled || s.settings.SensitiveInfo.Enabled || s.settings.Credential.Enabled ||
		s.settings.SensitiveData.Mode == config.ContentSafetyModeMask && s.custom != nil && s.custom.Enabled()
}

// AttachHookPipeline 将内容安全 Hook 管道绑定到当前请求。入口解析请求后调用，
// 请求前检查会在最终上游载荷生成后、Vision 处理前后各执行一次。
func AttachHookPipeline(c requestContextSetter, pipeline *Pipeline, metadata HookContext) {
	if c == nil || pipeline == nil {
		return
	}
	if metadata.eventDeduper == nil {
		metadata.eventDeduper = newSafetyEventDeduper()
	}
	requestCtx := context.Background()
	if getter, ok := c.(requestContextGetter); ok {
		requestCtx = attachedRequestContext(getter)
	}
	lifecycleCtx, lifecycleCancel := context.WithCancel(requestCtx)
	vault := sensitive.NewVaultWithContext(lifecycleCtx)
	metadata.redactionVault = vault
	c.Set(contentSafetyPipelineKey, &attachedHookPipeline{
		pipeline:        pipeline,
		metadata:        metadata,
		vault:           vault,
		vetoGuard:       newStreamVetoGuard(),
		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
	})
}

type requestContextSetter interface {
	Set(string, any)
}

type requestContextGetter interface {
	Get(string) (any, bool)
}

func runAttachedPreRequestHooks(ctx context.Context, c requestContextGetter, req *http.Request, channelName string, payloadProtocols ...string) error {
	if c == nil || req == nil {
		return nil
	}
	value, exists := c.Get(contentSafetyPipelineKey)
	if !exists || value == nil {
		return nil
	}
	attached, ok := value.(*attachedHookPipeline)
	if !ok || attached == nil || attached.pipeline == nil {
		return fmt.Errorf("内容安全管道绑定数据无效")
	}
	if len(payloadProtocols) > 1 {
		return fmt.Errorf("内容安全只允许一个上游载荷协议")
	}
	enabled, err := attached.preparePreRequest()
	if err != nil {
		return err
	}
	metadata := attached.currentMetadata()
	metadata.redactionVault = attached.vault
	metadata.preRequestSnapshot = attached.preRequestSnapshot
	metadata.ChannelName = channelName
	if metadata.RequestID == "" {
		metadata.RequestID = requestIDFromHeaders(req.Header)
	}
	protocolKey := ""
	if len(payloadProtocols) == 1 {
		protocolKey = payloadProtocols[0]
	}
	metadata.PayloadProtocol = protocolKey
	attached.metadataMu.Lock()
	attached.metadata = metadata
	attached.metadataMu.Unlock()
	if !enabled {
		return nil
	}
	contentType := req.Header.Get("Content-Type")
	if inspected, ok := req.Body.(*checkedPreRequestBody); ok && inspected.owner == attached && inspected.protocol == protocolKey && inspected.contentType == contentType && inspected.Len() == inspected.size {
		return nil
	}
	var (
		body                  []byte
		originalContentLength = req.ContentLength
		originalGetBody       = req.GetBody
		originalBodyWasNil    = req.Body == nil
		originalHeaderLength  []string
		hadHeaderLength       bool
	)
	if req.Header != nil {
		originalHeaderLength, hadHeaderLength = req.Header["Content-Length"]
		originalHeaderLength = append([]string(nil), originalHeaderLength...)
	}
	if req.Body != nil {
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return fmt.Errorf("读取上游请求体失败: %w", err)
		}
	}
	// 请求级结果缓存：同一请求体的钩子重复执行（vision 层前后 / 故障转移
	// 重试）直接复用上次的处理结果。命中时只重放请求体改写，跳过整套检测。
	inputSHA := sha256.Sum256(body)
	attached.preRequestMu.Lock()
	cached := attached.preRequestCache
	attached.preRequestMu.Unlock()
	if cached != nil && cached.protocol == protocolKey && cached.contentType == contentType && cached.inputSHA == inputSHA {
		if req.Header != nil && cached.outputContentType != contentType {
			req.Header.Set("Content-Type", cached.outputContentType)
		}
		applyPreRequestBody(req, cached.output, cached.changed, body, originalContentLength, originalGetBody, originalBodyWasNil, originalHeaderLength, hadHeaderLength)
		markCheckedPreRequestBody(req, attached, protocolKey, cached.output)
		return nil
	}

	result, err := attached.pipeline.RunPreRequest(ctx, metadata, HookResult{
		RequestBody:     body,
		UpstreamRequest: req,
	})
	if err != nil {
		return attached.recordContentSafetyError(ctx, metadata, err)
	}
	if result.UpstreamRequest == nil {
		result.UpstreamRequest = req
	}
	if result.UpstreamRequest != req {
		return fmt.Errorf("请求前 Hook 不允许替换上游请求对象")
	}

	changed := !bytes.Equal(result.RequestBody, body)
	attached.preRequestMu.Lock()
	attached.preRequestCache = &attachedPreRequestCache{
		inputSHA:          inputSHA,
		protocol:          protocolKey,
		output:            result.RequestBody,
		changed:           changed,
		contentType:       contentType,
		outputContentType: req.Header.Get("Content-Type"),
	}
	attached.preRequestMu.Unlock()
	applyPreRequestBody(req, result.RequestBody, changed, body, originalContentLength, originalGetBody, originalBodyWasNil, originalHeaderLength, hadHeaderLength)
	markCheckedPreRequestBody(req, attached, protocolKey, result.RequestBody)
	return nil
}

func markCheckedPreRequestBody(req *http.Request, owner *attachedHookPipeline, protocol string, body []byte) {
	if req.Body != nil {
		req.Body = &checkedPreRequestBody{Reader: bytes.NewReader(body), owner: owner, protocol: protocol, contentType: req.Header.Get("Content-Type"), size: len(body)}
	}
}

// applyPreRequestBody 把钩子处理结果写回上游请求。changed=false 时恢复请求
// 的原始长度属性（仅在真正改动时才复制请求体，避免无谓的全量拷贝）。
func applyPreRequestBody(req *http.Request, output []byte, changed bool, original []byte, originalContentLength int64, originalGetBody func() (io.ReadCloser, error), originalBodyWasNil bool, originalHeaderLength []string, hadHeaderLength bool) {
	if !changed {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		if originalBodyWasNil {
			req.Body = nil
		} else {
			req.Body = io.NopCloser(bytes.NewReader(original))
		}
		req.ContentLength = originalContentLength
		req.GetBody = originalGetBody
		if req.Header != nil {
			if hadHeaderLength {
				req.Header["Content-Length"] = originalHeaderLength
			} else {
				req.Header.Del("Content-Length")
			}
		}
		return
	}
	requestBody := output
	if req.Body != nil {
		_ = req.Body.Close()
	}
	req.Body = io.NopCloser(bytes.NewReader(requestBody))
	req.ContentLength = int64(len(requestBody))
	req.TransferEncoding = nil
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(requestBody)), nil
	}
	if req.Header != nil {
		req.Header.Set("Content-Length", fmt.Sprintf("%d", len(requestBody)))
	}
}

// RunAttachedPreRequestHooks 对已经构造完成的上游请求执行绑定的请求前 Hook。
// 独立代理入口（例如 Responses Compact）应在每次实际上游发送前调用它。
func RunAttachedPreRequestHooks(ctx context.Context, c requestContextGetter, req *http.Request, channelName string, payloadProtocols ...string) error {
	return runAttachedPreRequestHooks(ctx, c, req, channelName, payloadProtocols...)
}

// NewContentSafetyPipeline 创建带敏感词和敏感信息请求前 Hook 的管道。
// 设置在每次请求时从 ConfigManager 读取，文件热重载无需重建路由。
func NewContentSafetyPipeline(cfgManager *config.ConfigManager) *Pipeline {
	return NewContentSafetyPipelineWithRecorder(cfgManager, nil)
}

// ResolveContentSafetyPipeline 解析入口注入的共享管道。
// 不传参数时保留旧 Handler 调用合同，仍创建完整的检测管道；主进程必须传入
// 带 BlockedLogRecorder 的共享实例，确保拦截事件可以持久化。
func ResolveContentSafetyPipeline(cfgManager *config.ConfigManager, pipelines ...*Pipeline) *Pipeline {
	if len(pipelines) > 1 {
		panic("每个协议入口只能注入一个内容安全管道")
	}
	if len(pipelines) == 1 {
		if pipelines[0] == nil {
			panic("注入的内容安全管道不能为空")
		}
		return pipelines[0]
	}
	return NewContentSafetyPipeline(cfgManager)
}

// NewContentSafetyPipelineWithRecorder 创建内容安全管道并注入统一拦截记录器。
// 可选注入危险命令 AI 闸门（CmdGate）：nil 或未启用时保持"命中即拦截"的既有行为。
func NewContentSafetyPipelineWithRecorder(cfgManager *config.ConfigManager, recorder BlockedLogRecorder, cmdGate ...*CmdGate) *Pipeline {
	pipeline := NewPipeline()
	pipeline.blockedLogRecorder = recorder
	if len(cmdGate) > 0 {
		pipeline.cmdGate = cmdGate[0]
	}
	if err := pipeline.Register(&contentSafetyPreRequestHook{cfgManager: cfgManager, recorder: recorder}); err != nil {
		panic(fmt.Sprintf("注册内置内容安全 Hook 失败: %v", err))
	}
	if err := pipeline.Register(&contentSafetyPostResponseHook{cfgManager: cfgManager, recorder: recorder, gate: pipeline.cmdGate}); err != nil {
		panic(fmt.Sprintf("注册内置响应安全 Hook 失败: %v", err))
	}
	return pipeline
}

// RunAttachedPostResponseHooks 在协议将非流式响应写回客户端前执行响应 Hook。
func RunAttachedPostResponseHooks(ctx context.Context, c requestContextGetter, body []byte, response *http.Response) ([]byte, error) {
	if c == nil {
		return body, nil
	}
	value, exists := c.Get(contentSafetyPipelineKey)
	if !exists || value == nil {
		return body, nil
	}
	attached, ok := value.(*attachedHookPipeline)
	if !ok || attached == nil || attached.pipeline == nil {
		return body, wrapContentSafetyHookError(fmt.Errorf("内容安全管道绑定数据无效"))
	}
	metadata := attached.currentMetadata()
	result, err := attached.pipeline.RunPostResponse(ctx, metadata, HookResult{
		ResponseBody:     body,
		UpstreamResponse: response,
	})
	if err != nil {
		return body, wrapContentSafetyHookError(attached.recordContentSafetyError(ctx, metadata, err))
	}
	restored, err := RestoreAttachedResponseBody(c, result.ResponseBody)
	if err != nil {
		return body, wrapContentSafetyHookError(err)
	}
	return restored, nil
}

// RestoreAttachedResponseBody 仅执行请求级占位符还原，供非 2xx 错误等不会进入
// PostResponse 管道的响应出口复用。
func RestoreAttachedResponseBody(c requestContextGetter, body []byte) ([]byte, error) {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil || attached.vault == nil {
		return body, err
	}
	return attached.vault.RestoreJSON(body)
}

// MaskAttachedText 使用当前请求已经建立的映射脱敏协议解析阶段提前提取的文本。
func MaskAttachedText(c requestContextGetter, text string) (string, error) {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil || attached.vault == nil {
		return text, err
	}
	masked := attached.vault.MaskKnown(text)
	return masked, attached.vault.Err()
}

// AssignAttachedConversation 将当前请求的内容安全事件关联到内部会话，并更新
// 后续上游尝试和响应流使用的 Hook 元数据。
func AssignAttachedConversation(ctx context.Context, c requestContextGetter, conversationID string) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil {
		return err
	}
	attached.metadataMu.Lock()
	attached.metadata.ConversationID = conversationID
	requestID := attached.metadata.RequestID
	deduper := attached.metadata.eventDeduper
	attached.metadataMu.Unlock()
	assigner, ok := attached.pipeline.blockedLogRecorder.(blockedLogConversationAssigner)
	if !ok || requestID == "" || !deduper.hasUnassigned() {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, gateStoreTimeout)
	defer cancel()
	if err := assigner.AssignConversation(ctx, requestID, conversationID); err != nil {
		return err
	}
	deduper.markAssigned()
	return nil
}

// ClearAttachedSensitiveData 在请求统一退出路径释放明文映射和流式还原状态。
func ClearAttachedSensitiveData(c requestContextGetter) {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil {
		return
	}
	if attached.lifecycleCancel != nil {
		attached.lifecycleCancel()
	}
	attached.streamMu.Lock()
	attached.restorers = nil
	attached.restorePending = nil
	attached.restorePendingBytes = 0
	attached.restoreEventBytes = 0
	attached.restoreErr = nil
	attached.toolArgs = nil
	attached.toolArgBytes = 0
	attached.stream = nil
	attached.textStreams = nil
	attached.textStreamBytes = 0
	attached.streamCredential = nil
	attached.streamMaskMode = ""
	attached.streamSettings = config.ContentSafetyConfig{}
	attached.streamDetector = nil
	attached.allowedCommands = nil
	attached.streamMu.Unlock()
	attached.preRequestMu.Lock()
	attached.preRequestCache = nil
	attached.preRequestMu.Unlock()
	if attached.vault != nil {
		attached.vault.Clear()
	}
}

// ResetAttachedStreamRestoration 在新的上游尝试开始前丢弃上一尝试的分片状态，
// 但保留请求级 Vault，使重试和渠道故障转移继续使用同一批占位符。
func ResetAttachedStreamRestoration(c requestContextGetter) error {
	attached, err := attachedPipelineFromContext(c)
	if err != nil || attached == nil {
		return err
	}
	attached.streamMu.Lock()
	attached.restorers = nil
	attached.restorePending = nil
	attached.restorePendingBytes = 0
	attached.restoreEventBytes = 0
	attached.restoreErr = nil
	attached.toolArgs = nil
	attached.toolArgBytes = 0
	attached.stream = nil
	attached.textStreams = nil
	attached.textStreamBytes = 0
	attached.streamDetector = nil
	attached.streamCredential = nil
	attached.allowedCommands = nil
	attached.streamMu.Unlock()
	return nil
}

func (a *attachedHookPipeline) currentMetadata() HookContext {
	if a == nil {
		return HookContext{}
	}
	a.metadataMu.RLock()
	defer a.metadataMu.RUnlock()
	return a.metadata
}

func (a *attachedHookPipeline) recordContentSafetyError(ctx context.Context, metadata HookContext, err error) error {
	if a == nil || a.pipeline == nil || a.pipeline.blockedLogRecorder == nil {
		return err
	}
	safetyErr := ContentSafetyErrorFrom(err)
	if safetyErr == nil || safetyErr.audited {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, gateStoreTimeout)
	defer cancel()
	_, recordErr := a.pipeline.blockedLogRecorder.Record(ctx, sensitive.BlockedLog{
		APIType:        metadata.APIType,
		BlockType:      safetyErr.BlockType,
		RuleName:       safetyErr.RuleName,
		PromptSnippet:  safetyErr.Snippet,
		Command:        safetyErr.Command,
		CommandHash:    safetyErr.CommandHash,
		ChannelName:    metadata.ChannelName,
		Model:          metadata.Model,
		RequestID:      metadata.RequestID,
		ConversationID: metadata.ConversationID,
		AIReview:       safetyErr.AIReview,
	})
	if recordErr == nil {
		safetyErr.audited = true
		if metadata.ConversationID == "" {
			metadata.eventDeduper.markUnassigned()
		}
		return err
	}
	recordErr = fmt.Errorf("写入内容安全拦截记录失败: %w", recordErr)
	log.Printf("[ContentSafety] %v", recordErr)
	return errors.Join(err, recordErr)
}

func requestIDFromHeaders(header http.Header) string {
	if header == nil {
		return ""
	}
	for _, name := range []string{"X-Request-Id", "X-Oai-Request-Id", "Request-Id"} {
		if requestID := strings.TrimSpace(header.Get(name)); requestID != "" {
			return requestID
		}
	}
	return ""
}

type contentSafetySnapshot struct {
	settings   config.ContentSafetyConfig
	words      *sensitive.WordFilter
	info       *sensitive.InfoDetector
	credential *sensitive.CredentialDetector
	custom     *sensitive.CustomDetector
}

type contentSafetyPreRequestHook struct {
	cfgManager *config.ConfigManager
	recorder   BlockedLogRecorder
	mu         sync.Mutex
	snapshot   atomic.Pointer[contentSafetySnapshot]
}

func (h *contentSafetyPreRequestHook) Name() string     { return "content-safety" }
func (h *contentSafetyPreRequestHook) Stage() HookStage { return HookStagePreRequest }
func (h *contentSafetyPreRequestHook) Priority() int    { return 100 }

func (h *contentSafetyPreRequestHook) Run(ctx context.Context, metadata HookContext, result HookResult) (HookResult, error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if h == nil || h.cfgManager == nil {
		return result, fmt.Errorf("内容安全 Hook 未绑定配置管理器")
	}
	snapshot := metadata.preRequestSnapshot
	if snapshot == nil {
		var err error
		snapshot, err = h.currentSnapshot()
		if err != nil {
			return result, err
		}
	}
	if !snapshot.requestEnabled() {
		return result, nil
	}
	if len(result.RequestBody) == 0 || result.UpstreamRequest == nil {
		return result, nil
	}
	if metadata.redactionVault != nil {
		metadata.redactionVault.ReserveText(string(result.RequestBody))
		if err := metadata.redactionVault.Err(); err != nil {
			return result, err
		}
	}
	protocol := metadata.PayloadProtocol
	if protocol == "" {
		protocol = metadata.APIType
	}
	contentType := result.UpstreamRequest.Header.Get("Content-Type")
	if boundary, ok := utils.MultipartBoundary(contentType); ok {
		return h.runMultipartFormSafety(ctx, metadata, result, snapshot, protocol, boundary)
	}
	if !isJSONContentType(contentType, result.RequestBody) {
		return result, nil
	}

	var payload interface{}
	decoder := json.NewDecoder(bytes.NewReader(result.RequestBody))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return result, fmt.Errorf("解析内容安全请求体失败: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return result, fmt.Errorf("解析内容安全请求体失败: JSON 包含多个顶层值")
		}
		return result, fmt.Errorf("解析内容安全请求体尾部失败: %w", err)
	}
	root, ok := payload.(map[string]interface{})
	if !ok {
		return result, nil
	}
	prompts := make([]string, 0, 4)
	segments, err := extractSafetySegments(protocol, root)
	if err != nil {
		return result, err
	}
	changed, err := applyContentSafetySegments(ctx, metadata, segments, snapshot, h.recorder, &prompts)
	if err != nil {
		return result, err
	}
	if !changed {
		result.Prompts = prompts
		return result, nil
	}
	body, err := utils.MarshalJSONNoEscape(payload)
	if err != nil {
		return result, fmt.Errorf("序列化内容安全请求体失败: %w", err)
	}
	result.RequestBody = body
	result.Prompts = prompts
	return result, nil
}

func (h *contentSafetyPreRequestHook) currentSnapshot() (*contentSafetySnapshot, error) {
	settings := h.cfgManager.GetSettings().ContentSafety
	current := h.snapshot.Load()
	if current != nil && reflect.DeepEqual(current.settings, settings) {
		return current, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	current = h.snapshot.Load()
	if current != nil && reflect.DeepEqual(current.settings, settings) {
		return current, nil
	}
	words, err := sensitive.NewWordFilter(settings.SensitiveWord)
	if err != nil {
		return nil, fmt.Errorf("初始化敏感词检测器失败: %w", err)
	}
	info, err := sensitive.NewInfoDetector(settings.SensitiveInfo)
	if err != nil {
		return nil, fmt.Errorf("初始化敏感信息检测器失败: %w", err)
	}
	credential, err := sensitive.NewCredentialDetector(settings.Credential)
	if err != nil {
		return nil, fmt.Errorf("初始化凭据检测器失败: %w", err)
	}
	custom, err := sensitive.NewCustomDetector(settings.CustomRules)
	if err != nil {
		return nil, fmt.Errorf("初始化自定义规则检测器失败: %w", err)
	}
	next := &contentSafetySnapshot{settings: settings, words: words, info: info, credential: credential, custom: custom}
	h.snapshot.Store(next)
	return next, nil
}

func isJSONContentType(contentType string, body []byte) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") {
		return true
	}
	if strings.HasPrefix(mediaType, "multipart/") || mediaType == "application/x-www-form-urlencoded" {
		return false
	}
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

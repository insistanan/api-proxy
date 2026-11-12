package visionlayer

import (
	"errors"
	"net/http"
	"regexp"
	"sync"
	"time"
)

const (
	visionPromptVersion  = "vision-layer-v4"
	visionCacheTTL       = 15 * time.Minute
	maxVisionBatchImages = 10
)

type cacheEntry struct {
	result    string
	expiresAt time.Time
}

type visionImage struct {
	id          string
	fingerprint string
	block       map[string]interface{}
	cacheKey    string
	memoryKey   string
	call        *visionInflightCall
	nonBlocking bool // 已在对话中出现过但缓存未命中（之前解析失败），失败时用占位文本替代而非阻塞请求
}

// visionAnalysisProfile 保存模型自行理解任务所需的受控上下文。
// 图片中的内容不会参与上下文提取，避免图片内提示影响内部处理流程。
type visionAnalysisProfile struct {
	intentFingerprint string
	userIntent        string
}

type visionWaiter struct {
	fingerprint string
	call        *visionInflightCall
}

type visionInflightCall struct {
	done   chan struct{}
	once   sync.Once
	result string
	err    error
}

type requestError struct {
	status int
	code   string
	err    error
}

func (e *requestError) Error() string { return e.err.Error() }
func (e *requestError) Unwrap() error { return e.err }

func wrapRequestError(status int, code string, err error) error {
	if err == nil {
		return nil
	}
	var existing *requestError
	if errors.As(err, &existing) {
		return err
	}
	return &requestError{status: status, code: code, err: err}
}

// ErrorResponse 返回图片理解错误应使用的 HTTP 状态码和稳定错误码。
func ErrorResponse(err error) (int, string) {
	var target *requestError
	if errors.As(err, &target) {
		return target.status, target.code
	}
	return http.StatusUnprocessableEntity, "VISION_LAYER_UNAVAILABLE"
}

type visionBatchResponse struct {
	Images []visionBatchItem `json:"images"`
}

type visionBatchItem struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

var (
	visionNamedSectionPattern  = regexp.MustCompile(`(?i)^\s*(?:#{1,6}\s*)?(?:\[\s*)?(?:image|图片)[_\s-]*(\d+)(?:\s*\])?\s*(?:[:：-]\s*)?(.*)$`)
	visionNumberSectionPattern = regexp.MustCompile(`^\s*(\d+)\s*[\.、\):：-]\s*(.*)$`)
)

var visionCache = struct {
	sync.Mutex
	items    map[string]cacheEntry
	inflight map[string]*visionInflightCall
}{
	items:    make(map[string]cacheEntry),
	inflight: make(map[string]*visionInflightCall),
}

var visionCallCounter uint64

package metrics

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/logger"
	"github.com/BenedictKing/api-proxy/internal/pricing"
)

const defaultRequestLogLimit = 50

type RequestLogEntry struct {
	RequestID             string  `json:"requestId"`
	AttemptID             string  `json:"attemptId"`
	Timestamp             string  `json:"timestamp"`
	APIType               string  `json:"apiType"`
	Entry                 string  `json:"entry"`
	Status                string  `json:"status"`
	StatusCode            int     `json:"statusCode,omitempty"`
	Success               bool    `json:"success"`
	DurationMs            int64   `json:"durationMs"`
	FirstTokenMs          int64   `json:"firstTokenMs,omitempty"`
	TPS                   float64 `json:"tps,omitempty"`
	Model                 string  `json:"model,omitempty"`
	ResolvedModel         string  `json:"resolvedModel,omitempty"`
	Transform             string  `json:"transform,omitempty"`
	InputTokens           int     `json:"inputTokens,omitempty"`
	OutputTokens          int     `json:"outputTokens,omitempty"`
	CacheCreationTokens   int     `json:"cacheCreationTokens,omitempty"`
	CacheReadTokens       int     `json:"cacheReadTokens,omitempty"`
	CacheCreation5mTokens int     `json:"cacheCreation5mTokens,omitempty"`
	CacheCreation1hTokens int     `json:"cacheCreation1hTokens,omitempty"`
	CacheTTL              string  `json:"cacheTTL,omitempty"`
	TPM                   float64 `json:"tpm,omitempty"`
	ChannelIndex          int     `json:"channelIndex"`
	ChannelName           string  `json:"channelName,omitempty"`
	BaseURL               string  `json:"baseUrl,omitempty"`
	KeyMask               string  `json:"keyMask,omitempty"`
	ErrorType             string  `json:"errorType,omitempty"`
	ErrorMessage          string  `json:"errorMessage,omitempty"`
	Retried               bool    `json:"retried"`
	Stream                bool    `json:"stream"`
	ConversationID        string  `json:"conversationId,omitempty"`
	ParentRequestID       string  `json:"parentRequestId,omitempty"`
	// CostUSD 是读取时按**当前**单价表折算的花费，不落盘（写入的 payload 里没有这个字段）。
	// 与 TPM 的处理方式不同：TPM 只依赖本条记录自身，写死没有后果；花费依赖单价表，
	// 写死会让改价后新旧记录出现两个口径，同一条请求在日志页与统计页显示不同金额。
	CostUSD float64 `json:"costUsd,omitempty"`
	// CostPriced 区分"花费为 0"与"查不到单价"：两者 CostUSD 都是 0，
	// 界面必须把后者显式标成未计价，否则免费模型和缺价模型看起来一模一样。
	CostPriced bool `json:"costPriced"`
}

type RequestLogListOptions struct {
	APIType string
	Limit   int
}

type RequestLogStore struct {
	logStore *logger.Store
}

func NewRequestLogStore(logStore *logger.Store) *RequestLogStore {
	return &RequestLogStore{logStore: logStore}
}

func (s *RequestLogStore) Record(entry RequestLogEntry) {
	if s == nil || s.logStore == nil {
		return
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().Format(time.RFC3339Nano)
	}
	entry.APIType = normalizeRequestLogAPIType(entry.APIType)
	entry.Entry = normalizeRequestLogEntry(entry.Entry, entry.APIType)
	if entry.TPM == 0 {
		entry.TPM = calculateRequestLogTPM(entry)
	}
	// 花费是读取时折算的派生字段，落盘会造出第二个真相源，这里显式清掉。
	entry.CostUSD = 0
	entry.CostPriced = false
	payload, err := json.Marshal(entry)
	if err != nil {
		return
	}
	s.logStore.RecordRequest(logger.RequestLogRecord{
		Timestamp: entry.Timestamp,
		RequestID: entry.RequestID,
		APIType:   entry.APIType,
		Payload:   payload,
	})
}

// List 读取最近的流量日志，并按当前单价表就地折算每条的花费。
// prices 允许为 nil（未注入单价表时全部记为未计价），但不接受"跳过折算"这种沉默分支：
// 花费缺失必须表现为 CostPriced=false，而不是一个看起来正常的 0 美元。
func (s *RequestLogStore) List(opts RequestLogListOptions, prices *pricing.Store) ([]RequestLogEntry, error) {
	if s == nil || s.logStore == nil {
		return nil, nil
	}
	limit := opts.Limit
	if limit <= 0 || limit > defaultRequestLogLimit {
		limit = defaultRequestLogLimit
	}
	payloads, err := s.logStore.QueryRequestLogs(context.Background(), logger.QueryOptions{
		APIType: normalizeRequestLogAPIType(opts.APIType),
		Limit:   limit,
	})
	if err != nil {
		return nil, err
	}
	entries := make([]RequestLogEntry, 0, len(payloads))
	for _, payload := range payloads {
		var entry RequestLogEntry
		if err := json.Unmarshal(payload, &entry); err != nil {
			continue
		}
		entry.APIType = normalizeRequestLogAPIType(entry.APIType)
		entry.Entry = normalizeRequestLogEntry(entry.Entry, entry.APIType)
		fillRequestLogCost(&entry, prices)
		entries = append(entries, entry)
	}
	return entries, nil
}

// fillRequestLogCost 按 entry.Model（客户端请求的模型名）折算花费，与 request_records
// 的花费统计同一口径；想按映射后的目标模型计价，给目标模型名单独配一条单价。
// 四项用量全 0 的记录（失败、取消、未回写 usage）不折算也不标未计价。
func fillRequestLogCost(entry *RequestLogEntry, prices *pricing.Store) {
	usage := pricing.TokenUsage{
		InputTokens:         int64(entry.InputTokens),
		OutputTokens:        int64(entry.OutputTokens),
		CacheReadTokens:     int64(entry.CacheReadTokens),
		CacheCreationTokens: int64(entry.CacheCreationTokens),
	}
	entry.CostUSD = 0
	entry.CostPriced = false
	if !hasTokenUsage(usage) {
		return
	}
	breakdown, ok := prices.CostFor(entry.Model, usage, entry.APIType, 1)
	if !ok {
		return
	}
	entry.CostPriced = true
	entry.CostUSD = breakdown.TotalCost
}

func (s *RequestLogStore) Close() {
}

func normalizeRequestLogAPIType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "claude", "message", "messages":
		return "messages"
	case "codex", "response", "responses":
		return "responses"
	case "gemini":
		return "gemini"
	case "chat", "openai":
		return "chat"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeRequestLogEntry(entry string, apiType string) string {
	entry = strings.ToLower(strings.TrimSpace(entry))
	if entry != "" {
		return entry
	}
	switch normalizeRequestLogAPIType(apiType) {
	case "messages":
		return "claude"
	case "responses":
		return "codex"
	case "gemini":
		return "gemini"
	case "chat":
		return "chat"
	default:
		return apiType
	}
}

func calculateRequestLogTPM(entry RequestLogEntry) float64 {
	totalTokens := entry.InputTokens + entry.OutputTokens
	if totalTokens <= 0 || entry.DurationMs <= 0 {
		return 0
	}
	return float64(totalTokens) * 60000 / float64(entry.DurationMs)
}

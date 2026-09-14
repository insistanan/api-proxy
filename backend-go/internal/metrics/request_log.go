package metrics

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/BenedictKing/api-proxy/internal/logger"
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
}

type RequestLogListOptions struct {
	APIType string
	Limit   int
	// From/To 为 unix 毫秒时间范围（0 表示不限制）；与 Limit 同时使用时返回区间内最近 Limit 条。
	From int64
	To   int64
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

// List 读取最近的流量日志。
func (s *RequestLogStore) List(opts RequestLogListOptions) ([]RequestLogEntry, error) {
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
		From:    opts.From,
		To:      opts.To,
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
		entries = append(entries, entry)
	}
	return entries, nil
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

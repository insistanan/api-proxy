package metrics

import (
	"sort"
	"sync"
	"time"
)

const defaultChannelLogLimit = 200

type ChannelLog struct {
	RequestID             string `json:"requestId"`
	AttemptID             string `json:"attemptId"`
	Timestamp             string `json:"timestamp"`
	Status                string `json:"status"`
	StatusCode            int    `json:"statusCode,omitempty"`
	Success               bool   `json:"success"`
	DurationMs            int64  `json:"durationMs"`
	APIType               string `json:"apiType"`
	Model                 string `json:"model,omitempty"`
	InputTokens           int    `json:"inputTokens,omitempty"`
	OutputTokens          int    `json:"outputTokens,omitempty"`
	CacheCreationTokens   int    `json:"cacheCreationTokens,omitempty"`
	CacheReadTokens       int    `json:"cacheReadTokens,omitempty"`
	CacheCreation5mTokens int    `json:"cacheCreation5mTokens,omitempty"`
	CacheCreation1hTokens int    `json:"cacheCreation1hTokens,omitempty"`
	ChannelIndex          int    `json:"channelIndex"`
	ChannelName           string `json:"channelName,omitempty"`
	BaseURL               string `json:"baseUrl"`
	KeyMask               string `json:"keyMask"`
	ErrorType             string `json:"errorType,omitempty"`
	ErrorMessage          string `json:"errorMessage,omitempty"`
	Retried               bool   `json:"retried"`
	Stream                bool   `json:"stream"`
}

type ChannelLogStore struct {
	mu    sync.RWMutex
	limit int
	logs  map[int][]*ChannelLog
}

func NewChannelLogStore() *ChannelLogStore {
	return &ChannelLogStore{
		limit: defaultChannelLogLimit,
		logs:  make(map[int][]*ChannelLog),
	}
}

func (s *ChannelLogStore) Record(logEntry *ChannelLog) {
	if s == nil || logEntry == nil {
		return
	}
	if logEntry.Timestamp == "" {
		logEntry.Timestamp = time.Now().Format(time.RFC3339Nano)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	channelLogs := append([]*ChannelLog{logEntry}, s.logs[logEntry.ChannelIndex]...)
	if len(channelLogs) > s.limit {
		channelLogs = channelLogs[:s.limit]
	}
	s.logs[logEntry.ChannelIndex] = channelLogs
}

func (s *ChannelLogStore) Get(channelIndex int) []*ChannelLog {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := append([]*ChannelLog(nil), s.logs[channelIndex]...)
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Timestamp > items[j].Timestamp
	})
	return items
}

func (s *ChannelLogStore) DeleteChannel(channelIndex int) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.logs, channelIndex)
}

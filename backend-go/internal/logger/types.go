package logger

import "encoding/json"

const (
	// DefaultDBPath 与 conversations.db 一样落在 .config。
	DefaultDBPath = ".config/logs.db"

	// MaxBodyBytes 日志正文的解析输入与最终输出上限。
	MaxBodyBytes = 1 << 20

	PhaseClientRequest     = "client_request"
	PhaseClientInput       = "client_input"
	PhaseUpstreamRequest   = "upstream_request"
	PhaseUpstreamResponse  = "upstream_response"
	PhaseStreamSynth       = "stream_synth"
	PhaseRequestStart      = "request_start"
	PhaseRequestEnd        = "request_end"
	PhaseUpstreamError     = "upstream_error"
	PhaseUpstreamHeaders   = "upstream_headers"
	PhaseUpstreamAdmission = "upstream_admission"

	writeQueueSize = 2048
)

// AvailableDate 是一个存在日志的本地日历日及其三张表的总行数。
type AvailableDate struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

// AppLog 是一条运行日志（原 app.log 的 sqlite 形态）。
type AppLog struct {
	ID        int64  `json:"id"`
	Timestamp string `json:"timestamp"`
	UnixMilli int64  `json:"unixMilli"`
	Tag       string `json:"tag,omitempty"`
	Message   string `json:"message"`
}

// TrafficLog 是一次请求或响应的结构化正文。
type TrafficLog struct {
	ID            int64  `json:"id,omitempty"`
	Timestamp     string `json:"timestamp"`
	UnixMilli     int64  `json:"unixMilli,omitempty"`
	RequestID     string `json:"requestId"`
	AttemptID     string `json:"attemptId,omitempty"`
	Phase         string `json:"phase"`
	APIType       string `json:"apiType,omitempty"`
	Method        string `json:"method,omitempty"`
	URL           string `json:"url,omitempty"`
	StatusCode    int    `json:"statusCode,omitempty"`
	Stream        bool   `json:"stream,omitempty"`
	HeadersJSON   string `json:"headers,omitempty"`
	Body          string `json:"body,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
	OriginalBytes int    `json:"originalBytes,omitempty"`
	bodyPrepared  bool
}

// WithPreparedBody 绑定 PrepareBody 或 OmittedBody 的结果，避免入队前重复清洗。
// 原始长度未知时保留 0，不把省略说明的长度当成正文长度。
func (entry TrafficLog) WithPreparedBody(body string, originalBytes int, truncated bool) TrafficLog {
	entry.Body = body
	entry.OriginalBytes = originalBytes
	entry.Truncated = truncated
	entry.bodyPrepared = true
	return entry
}

// RequestLogRecord 是 Web 请求日志页的元数据行（payload 为完整 JSON）。
type RequestLogRecord struct {
	UnixMilli int64
	Timestamp string
	RequestID string
	APIType   string
	Payload   []byte
}

// QueryOptions 描述时段查询。Limit=0 表示不截断。
// Ascending=true 时按时间正序（CLI 时段查询语义）；默认倒序，配合 Limit 取最近 N 条。
type QueryOptions struct {
	From      int64
	To        int64
	RequestID string
	APIType   string
	Limit     int
	Ascending bool
}

// SystemLogListOptions 是 Web 系统日志列表查询。Limit 默认 100、封顶 200。
type SystemLogListOptions struct {
	APIType string
	Limit   int
}

// SystemLogSummary 是一次大模型请求的流量摘要（不含正文）。
type SystemLogSummary struct {
	RequestID      string   `json:"requestId"`
	Timestamp      string   `json:"timestamp"`
	UnixMilli      int64    `json:"unixMilli"`
	APIType        string   `json:"apiType,omitempty"`
	Phases         []string `json:"phases"`
	MissingPhases  []string `json:"missingPhases,omitempty"`
	StatusCode     int      `json:"statusCode,omitempty"`
	Stream         bool     `json:"stream"`
	Truncated      bool     `json:"truncated"`
	ChannelName    string   `json:"channelName,omitempty"`
	Model          string   `json:"model,omitempty"`
	Status         string   `json:"status,omitempty"`
	ConversationID string   `json:"conversationId,omitempty"`
	RequestHash    string   `json:"requestHash,omitempty"`
	Client         string   `json:"client,omitempty"`
	ErrorType      string   `json:"errorType,omitempty"`
	ErrorMessage   string   `json:"errorMessage,omitempty"`
	StartedAt      int64    `json:"startedAt,omitempty"`
}

// RequestLifecycle 只记录身份和结果，不重复保存请求正文。
type RequestLifecycle struct {
	Stream         bool   `json:"stream"`
	ConversationID string `json:"conversationId,omitempty"`
	RequestHash    string `json:"requestHash,omitempty"`
	Client         string `json:"client,omitempty"`
	BootID         string `json:"bootId,omitempty"`
	Model          string `json:"model,omitempty"`
	Status         string `json:"status,omitempty"`
	StatusCode     int    `json:"statusCode,omitempty"`
	ErrorType      string `json:"errorType,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	StartedAt      int64  `json:"startedAt"`
}

// SystemLogDetail 是一次请求的流量正文详情。
type SystemLogDetail struct {
	RequestID   string            `json:"requestId"`
	Summary     SystemLogSummary  `json:"summary"`
	TrafficLogs []TrafficLog      `json:"trafficLogs"`
	RequestLogs []json.RawMessage `json:"requestLogs,omitempty"`
}

// QueryResult 是 CLI `logs query` 的 stdout JSON。
type QueryResult struct {
	From        string            `json:"from"`
	To          string            `json:"to"`
	AppLogs     []AppLog          `json:"appLogs"`
	TrafficLogs []TrafficLog      `json:"trafficLogs"`
	RequestLogs []json.RawMessage `json:"requestLogs"`
}

// ShowResult 是 CLI `logs show` 的 stdout JSON。
type ShowResult struct {
	RequestID   string            `json:"requestId"`
	RequestLogs []json.RawMessage `json:"requestLogs"`
	TrafficLogs []TrafficLog      `json:"trafficLogs"`
}

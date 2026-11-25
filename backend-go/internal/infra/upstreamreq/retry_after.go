package upstreamreq

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ParseRetryAfter 解析上游明确给出的等待截止时间，不为缺失或无效值制造冷却。
// 过期日期表示已无需等待；是否采用本地退避由准入策略决定。
func ParseRetryAfter(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/int64(time.Second)) {
		return now.Add(time.Duration(seconds) * time.Second), true
	}
	if until, err := http.ParseTime(value); err == nil {
		if until.Before(now) {
			until = now
		}
		return until, true
	}
	return time.Time{}, false
}

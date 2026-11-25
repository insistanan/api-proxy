package tokenusage

import (
	"context"
	"time"
)

type Source string

const (
	SourceUpstream  Source = "upstream"
	SourceEstimated Source = "estimated"
	SourceMissing   Source = "missing"
)

// Fact 是一次上游尝试的用量快照。四项 Token 互不重叠。
type Fact struct {
	AttemptID     string
	At            time.Time
	Protocol      string
	Model         string
	Source        Source
	Input         int64
	Output        int64
	CacheCreation int64
	CacheRead     int64
}

// Credit 是已落盘的可用于成长的用量，按游标严格递增。
type Credit struct {
	Cursor int64
	Tokens int64
	At     int64 // 实际用量时间（毫秒），不以补记或消费时间代替。
}

// UnattributedCacheInput 保留本次输入总量，供账本排除无法作为输入子集的缓存值。
func UnattributedCacheInput(input, cached int) *int {
	if cached > 0 && cached > input {
		return &input
	}
	return nil
}

type recorderKey struct{}

func WithRecorder(ctx context.Context, record func(Fact)) context.Context {
	return context.WithValue(ctx, recorderKey{}, record)
}

func RecorderFrom(ctx context.Context) func(Fact) {
	record, _ := ctx.Value(recorderKey{}).(func(Fact))
	return record
}

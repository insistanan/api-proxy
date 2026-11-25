package pipeline

import (
	"context"
	"crypto/sha256"

	"github.com/insistanan/api-proxy/internal/safety/sensitive"
)

type blockedLogBatchRecorder interface {
	RecordBatch(context.Context, []sensitive.BlockedLog) error
}

// 每个请求前检查独占一个批次；到达容量或检查结束时同步提交。
// 去重标记只在对应记录持久化成功后发布，失败不改变重试语义。
type safetyAuditBatch struct {
	recorder BlockedLogRecorder
	entries  []sensitive.BlockedLog
	keys     []string
	pending  map[[sha256.Size]byte]struct{}
	deduper  *safetyEventDeduper
	bytes    int
	err      error
}

func (m HookContext) safetyEventSeen(key string) bool {
	if m.eventDeduper.contains(key) {
		return true
	}
	if m.auditBatch != nil {
		_, exists := m.auditBatch.pending[sha256.Sum256([]byte(key))]
		return exists
	}
	return false
}

func recordSafetyEvent(ctx context.Context, metadata HookContext, recorder BlockedLogRecorder, key string, entry sensitive.BlockedLog) error {
	if metadata.auditBatch != nil {
		return metadata.auditBatch.add(ctx, key, entry)
	}
	ctx, cancel := context.WithTimeout(ctx, gateStoreTimeout)
	defer cancel()
	if _, err := recorder.Record(ctx, entry); err != nil {
		return err
	}
	metadata.eventDeduper.mark(key, entry.ConversationID == "")
	return nil
}

func (b *safetyAuditBatch) add(ctx context.Context, key string, entry sensitive.BlockedLog) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.err != nil {
		return b.err
	}
	size := len(key) + len(entry.PromptSnippet) + len(entry.Command) + len(entry.APIType) + len(entry.BlockType) + len(entry.RuleName) + len(entry.ChannelName) + len(entry.Model) + len(entry.RequestID) + len(entry.ConversationID)
	if len(b.entries) >= 256 || b.bytes+size > 1<<20 {
		if err := b.flush(ctx); err != nil {
			return err
		}
	}
	if b.pending == nil {
		b.pending = make(map[[sha256.Size]byte]struct{})
	}
	b.entries = append(b.entries, entry)
	b.keys = append(b.keys, key)
	b.pending[sha256.Sum256([]byte(key))] = struct{}{}
	b.bytes += size
	if b.bytes >= 1<<20 {
		// 单条较大的记录直接提交；批量阈值只控制刷新时机，不拒绝合法内容。
		return b.flush(ctx)
	}
	return nil
}

func (b *safetyAuditBatch) flush(ctx context.Context) error {
	if b.err != nil || len(b.entries) == 0 {
		return b.err
	}
	ctx, cancel := context.WithTimeout(ctx, gateStoreTimeout)
	defer cancel()
	if recorder, ok := b.recorder.(blockedLogBatchRecorder); ok {
		b.err = recorder.RecordBatch(ctx, b.entries)
		if b.err != nil {
			return b.err
		}
		for i, entry := range b.entries {
			b.deduper.mark(b.keys[i], entry.ConversationID == "")
		}
	} else {
		// 非批量存储适配器保留逐条提交行为，不吞掉持久化错误。
		for i, entry := range b.entries {
			if _, b.err = b.recorder.Record(ctx, entry); b.err != nil {
				return b.err
			}
			b.deduper.mark(b.keys[i], entry.ConversationID == "")
		}
	}
	clear(b.entries)
	clear(b.keys)
	b.entries = b.entries[:0]
	b.keys = b.keys[:0]
	clear(b.pending)
	b.bytes = 0
	return nil
}

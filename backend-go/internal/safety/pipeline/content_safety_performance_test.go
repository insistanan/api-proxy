package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/safety/sensitive"
	"github.com/stretchr/testify/require"
)

type batchAuditRecorder struct {
	gateTestRecorder
	batches int
	err     error
}

func (r *batchAuditRecorder) RecordBatch(_ context.Context, entries []sensitive.BlockedLog) error {
	r.batches++
	if r.err != nil {
		return r.err
	}
	r.entries = append(r.entries, entries...)
	return nil
}

func TestSafetyAuditBatch_FailureDoesNotPublishDeduplication(t *testing.T) {
	recorder := &batchAuditRecorder{err: errors.New("存储不可用")}
	deduper := newSafetyEventDeduper()
	batch := &safetyAuditBatch{recorder: recorder, deduper: deduper}
	metadata := HookContext{eventDeduper: deduper, auditBatch: batch}
	require.NoError(t, batch.add(context.Background(), "event", sensitive.BlockedLog{RequestID: "request"}))
	require.True(t, metadata.safetyEventSeen("event"))
	require.False(t, deduper.contains("event"))
	require.ErrorIs(t, batch.flush(context.Background()), recorder.err)
	require.False(t, deduper.contains("event"))
	require.False(t, deduper.hasUnassigned())
	require.Empty(t, recorder.entries)
	recorder.err = nil
	retry := &safetyAuditBatch{recorder: recorder, deduper: deduper}
	require.NoError(t, retry.add(context.Background(), "event", sensitive.BlockedLog{RequestID: "request"}))
	require.NoError(t, retry.flush(context.Background()))
	require.True(t, deduper.contains("event"))
	require.True(t, deduper.hasUnassigned())
}

func TestSafetyAuditBatch_BoundedFlushAndLegacyRecorder(t *testing.T) {
	recorder := &batchAuditRecorder{}
	batch := &safetyAuditBatch{recorder: recorder, deduper: newSafetyEventDeduper()}
	for i := 0; i < 257; i++ {
		require.NoError(t, batch.add(context.Background(), fmt.Sprint(i), sensitive.BlockedLog{RequestID: "request"}))
	}
	require.Equal(t, 1, recorder.batches)
	require.NoError(t, batch.flush(context.Background()))
	require.Equal(t, 2, recorder.batches)
	require.Len(t, recorder.entries, 257)
	large := strings.Repeat("a", 600_000)
	require.NoError(t, batch.add(context.Background(), "large-1", sensitive.BlockedLog{PromptSnippet: large}))
	require.NoError(t, batch.add(context.Background(), "large-2", sensitive.BlockedLog{PromptSnippet: large}))
	require.Equal(t, 3, recorder.batches)
	require.LessOrEqual(t, batch.bytes, 1<<20)
	require.NoError(t, batch.flush(context.Background()))
	legacy := &gateTestRecorder{}
	batch = &safetyAuditBatch{recorder: legacy}
	require.NoError(t, batch.add(context.Background(), "legacy", sensitive.BlockedLog{RequestID: "legacy"}))
	require.NoError(t, batch.flush(context.Background()))
	require.Len(t, legacy.entries, 1)
}

func TestSafetyAuditBatch_LargeEntryFlushesWithoutSizeRejection(t *testing.T) {
	recorder := &batchAuditRecorder{}
	deduper := newSafetyEventDeduper()
	batch := &safetyAuditBatch{recorder: recorder, deduper: deduper}
	entry := sensitive.BlockedLog{PromptSnippet: strings.Repeat("a", (1<<20)+1)}
	require.NoError(t, batch.add(context.Background(), "large", entry))
	require.Equal(t, []sensitive.BlockedLog{entry}, recorder.entries)
	require.Empty(t, batch.entries)
	require.True(t, deduper.contains("large"))
}

func TestContentSafetyAudit_FlushesBeforeReturningBlockedResponse(t *testing.T) {
	recorder := &batchAuditRecorder{}
	snapshot := hardeningSnapshot(t)
	segments := []SafetySegment{
		{Source: safetySourceUser, Path: "first", Field: "password", Text: "first-secret", Mutable: true, replace: func(string) {}},
		{Source: safetySourceUser, Path: "second", Field: "password", Text: "second-secret", Signed: true},
	}
	var prompts []string
	_, err := applyContentSafetySegments(context.Background(), HookContext{
		APIType: "chat", RequestID: "request", eventDeduper: newSafetyEventDeduper(),
	}, segments, snapshot, recorder, &prompts, false)
	require.NotNil(t, ContentSafetyErrorFrom(err))
	require.Equal(t, 1, recorder.batches)
	require.Len(t, recorder.entries, 1)
	require.Equal(t, "request", recorder.entries[0].RequestID)
	require.NotContains(t, recorder.entries[0].PromptSnippet, "first-secret")
}

func TestContentSafetyAudit_CommitFailureFailsRequest(t *testing.T) {
	recorder := &batchAuditRecorder{err: errors.New("审计提交失败")}
	deduper := newSafetyEventDeduper()
	segments := []SafetySegment{{Source: safetySourceUser, Field: "password", Text: "secret", Mutable: true, replace: func(string) {}}}
	var prompts []string
	_, err := applyContentSafetySegments(context.Background(), HookContext{APIType: "chat", eventDeduper: deduper},
		segments, hardeningSnapshot(t), recorder, &prompts, false)
	require.ErrorIs(t, err, recorder.err)
	require.Empty(t, deduper.seen)
	require.Empty(t, recorder.entries)
}

func TestDetectionMemo_PromotesHotHistoryAndExpiresVersion(t *testing.T) {
	var memo safetyDetectionMemo
	text := strings.Repeat("history ", 40)
	calls := 0
	detect := func() []sensitive.RedactionMatch {
		calls++
		return []sensitive.RedactionMatch{{Start: 0, End: 7, Original: "history"}}
	}
	memo.findVersion('i', "field", text, 1, detect)
	for i := 0; i < 2048; i++ {
		memo.find('c', fmt.Sprint(i), text, func() []sensitive.RedactionMatch { return nil })
	}
	require.Equal(t, "history", memo.findVersion('i', "field", text, 1, detect)[0].Original)
	for i := 2048; i < 4096; i++ {
		memo.find('c', fmt.Sprint(i), text, func() []sensitive.RedactionMatch { return nil })
	}
	memo.findVersion('i', "field", text, 1, detect)
	require.Equal(t, 1, calls)
	memo.findVersion('i', "field", text, 2, detect)
	require.Equal(t, 2, calls)
	for _, generation := range []map[[32]byte][]sensitive.RedactionMatch{memo.current, memo.previous} {
		for _, matches := range generation {
			for _, match := range matches {
				require.Empty(t, match.Original)
			}
		}
	}
}

func TestAuditMemo_RepeatedHistoryStillWritesRequestAudit(t *testing.T) {
	snapshot := hardeningSnapshot(t)
	text := strings.Repeat("普通上下文 ", 50) + "password=K8p!rQ7vZ2"
	recorder := &batchAuditRecorder{}
	for _, requestID := range []string{"first", "second"} {
		metadata := HookContext{APIType: "chat", RequestID: requestID, eventDeduper: newSafetyEventDeduper(), preRequestSnapshot: snapshot}
		segment := SafetySegment{Source: safetySourceUser, Path: "history"}
		require.NoError(t, recordSafetyMatches(context.Background(), metadata, recorder, sensitive.BlockTypeCredential, "mask", segment, text,
			[]safetyHit{{rule: "named_secret", start: len(text) - 10, end: len(text), reveal: "[凭据已隐藏]"}}))
	}
	require.Len(t, recorder.entries, 2)
	require.Equal(t, "first", recorder.entries[0].RequestID)
	require.Equal(t, "second", recorder.entries[1].RequestID)
	for _, entry := range recorder.entries {
		require.NotContains(t, entry.PromptSnippet, "K8p!rQ7vZ2")
	}
}

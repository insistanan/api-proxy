package usageledger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
)

func TestGrowthUsage_OnlyPositiveUpstreamFactsSetActivityTime(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if err := store.Enable(ctx, time.UTC, start); err != nil {
		t.Fatal(err)
	}
	if at, err := store.LatestGrowthUsage(ctx); err != nil || at != 0 {
		t.Fatalf("空账本不应产生活跃时间: %d, %v", at, err)
	}
	facts := []tokenusage.Fact{
		{AttemptID: "缓存命中", At: start.Add(time.Hour), Source: tokenusage.SourceUpstream, CacheRead: 100},
		{AttemptID: "零用量", At: start.Add(2 * time.Hour), Source: tokenusage.SourceUpstream},
		{AttemptID: "估算", At: start.Add(3 * time.Hour), Source: tokenusage.SourceEstimated, Input: 200},
		{AttemptID: "缺失", At: start.Add(4 * time.Hour), Source: tokenusage.SourceMissing},
	}
	if err := store.flush(ctx, time.UTC, facts, nil, start.Add(5*time.Hour), false, false); err != nil {
		t.Fatal(err)
	}
	want := facts[0].At.UnixMilli()
	if at, err := store.LatestGrowthUsage(ctx); err != nil || at != want {
		t.Fatalf("缓存读取应计入活跃时间，零用量与估算不能重置时间: %d, %v", at, err)
	}
	credits, err := store.Credits(ctx, 0)
	if err != nil || len(credits) != 2 {
		t.Fatalf("成长用量来源不符: %+v, %v", credits, err)
	}
	if credits[0].Tokens != 100 || credits[0].At != want || credits[1].Tokens != 0 || credits[1].At != facts[1].At.UnixMilli() {
		t.Fatalf("增量没有保留实际发生时间: %+v", credits)
	}
	late := tokenusage.Fact{AttemptID: "迟到用量", At: start.Add(30 * time.Minute), Source: tokenusage.SourceUpstream, Output: 5}
	if err := store.flush(ctx, time.UTC, []tokenusage.Fact{late, late}, nil, start.AddDate(0, 0, 2), false, false); err != nil {
		t.Fatal(err)
	}
	if at, err := store.LatestGrowthUsage(ctx); err != nil || at != want {
		t.Fatalf("迟到补记不应改变最新实际用量时间: %d, %v", at, err)
	}
	if at, err := store.PreviousGrowthUsage(ctx, want); err != nil || at != late.At.UnixMilli() {
		t.Fatalf("前一次实际用量应包含尚未消费的迟到事实: %d, %v", at, err)
	}
	if at, err := store.PreviousGrowthUsage(ctx, facts[3].At.UnixMilli()); err != nil || at != want {
		t.Fatalf("零用量和估算不应被当成上一次活动: %d, %v", at, err)
	}
	next, err := store.Credits(ctx, credits[1].Cursor)
	if err != nil || len(next) != 1 || next[0].Tokens != 5 || next[0].At != late.At.UnixMilli() {
		t.Fatalf("补记未去重或消费时间替代了事实时间: %+v, %v", next, err)
	}
}

func TestCoverageRestoresUnavailableIntervalAfterCleanShutdown(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usage.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	firstGap := today.AddDate(0, 0, -2)
	if err := store.Enable(ctx, time.UTC, firstGap.AddDate(0, 0, -1)); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	for _, day := range []time.Time{firstGap, today} {
		if err := MarkUnavailable(path, day); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	detail, err := reopened.Daily(ctx, firstGap.Format(dayFormat), today)
	if err != nil || detail.Coverage != "partial" {
		t.Fatalf("首次停写日期应保留覆盖缺口: %+v, %v", detail, err)
	}
	detail, err = reopened.Daily(ctx, firstGap.AddDate(0, 0, -1).Format(dayFormat), today)
	if err != nil || detail.Coverage != "complete" {
		t.Fatalf("停写前完整日期不应受影响: %+v, %v", detail, err)
	}
	if _, err := os.Stat(path + unavailableSuffix); !os.IsNotExist(err) {
		t.Fatalf("停写标记应在缺口入账后清理: %v", err)
	}
}

func TestDaily_LateFactsAdvanceVersionOnce(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	day := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)
	if err := store.Enable(ctx, time.UTC, day); err != nil {
		t.Fatal(err)
	}
	fact := tokenusage.Fact{AttemptID: "按时入账", At: day.Add(time.Hour), Source: tokenusage.SourceUpstream, Input: 3}
	if err := store.flush(ctx, time.UTC, []tokenusage.Fact{fact}, nil, fact.At, false, false); err != nil {
		t.Fatal(err)
	}
	before, err := store.Daily(ctx, day.Format(dayFormat), fact.At)
	if err != nil || before.LateUpdate || before.TimeZone != "UTC" || before.Revision != 2 {
		t.Fatalf("正常入账后的票据不符: %+v, %v", before, err)
	}
	late := fact
	late.AttemptID = "迟到用量"
	nextDay := day.AddDate(0, 0, 1)
	if err := store.flush(ctx, time.UTC, []tokenusage.Fact{fact, late, late}, nil, nextDay, false, false); err != nil {
		t.Fatal(err)
	}
	after, err := store.Daily(ctx, day.Format(dayFormat), nextDay)
	if err != nil || !after.LateUpdate || after.Revision != before.Revision+1 || after.Tokens != 6 {
		t.Fatalf("迟到入账或版本去重不符: %+v, %v", after, err)
	}
}

func TestWriter_FullBatchSignalsWithoutWaitingForTick(t *testing.T) {
	store := &Store{queue: make(chan tokenusage.Fact, batchSize*2), wake: make(chan struct{}, 1)}
	now := time.Now()
	store.active.Store(&recording{start: now.UnixMilli(), zone: time.UTC})
	for i := range batchSize {
		store.Record(tokenusage.Fact{AttemptID: fmt.Sprint(i), At: now})
	}
	select {
	case <-store.wake:
	default:
		t.Fatal("已达到批大小，写入器应立即收到唤醒信号")
	}
}

func TestJournalPersistsAndDeduplicatesByAttempt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usage.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 11, 24, 0, 1, 0, 0, zone)
	store.Record(tokenusage.Fact{AttemptID: "未启用", At: start})
	if err := store.Enable(ctx, zone, start); err != nil {
		t.Fatal(err)
	}
	if err := store.Enable(ctx, time.UTC, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	store.Record(tokenusage.Fact{AttemptID: "早于启用", At: start.Add(-time.Minute)})
	usage := tokenusage.Fact{AttemptID: "重复投递", At: start.Add(time.Hour).UTC(), Model: "demo", Source: tokenusage.SourceUpstream,
		Input: 50, CacheCreation: 25, CacheRead: 100, Output: 10}
	store.Record(usage)
	store.Record(usage)
	store.Record(tokenusage.Fact{AttemptID: "估算", At: usage.At, Source: tokenusage.SourceEstimated, Input: 9})
	store.Record(tokenusage.Fact{AttemptID: "缺失", At: usage.At, Source: tokenusage.SourceMissing})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	now := start.AddDate(0, 0, 2)
	status := reopened.Status(now)
	if status.TimeZone != "Asia/Shanghai" || status.EnabledAt == nil || !status.EnabledAt.Equal(start) {
		t.Fatalf("统计设置不符: %+v", status)
	}
	detail, err := reopened.Daily(ctx, "2026-11-24", now)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Tokens != 185 || detail.Upstream.Attempts != 1 || detail.Upstream.CacheRead != 100 || detail.Estimated.Input != 9 || detail.MissingAttempts != 1 || detail.Coverage != "partial" {
		t.Fatalf("统计未按尝试去重或未区分来源: %+v", detail)
	}
	if detail.PeakHour == nil || detail.PeakHour.Hour() != 1 || len(detail.Models) != 1 || detail.Models[0].Tokens != 185 {
		t.Fatalf("统计未按时区归日或未正确汇总: %+v", detail)
	}
	days, err := reopened.Calendar(ctx, 2026, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range days {
		switch day.Date {
		case "2026-11-23", "2026-11-27":
			if day.Coverage != "unrecorded" {
				t.Errorf("预期未记录: %+v", day)
			}
		case "2026-11-24":
			if day.Coverage != "partial" || day.Tokens != detail.Tokens {
				t.Errorf("日历与票据口径不同: %+v", day)
			}
		case "2026-11-25":
			if day.Coverage != "complete" || day.Tokens != 0 {
				t.Errorf("有覆盖的零用量日不正确: %+v", day)
			}
		}
	}
}

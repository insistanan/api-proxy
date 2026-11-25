package forest

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"insistanan/api-proxy/internal/protocol/tokenusage"
)

func TestGrowth_LargeUsageSlowsLevelsAndIgnoresBatchSize(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	settle := func(parts int64) World {
		t.Helper()
		usage := &testUsage{}
		store := openTestForest(t, filepath.Join(t.TempDir(), "forest.db"), usage)
		for i := int64(1); i <= parts; i++ {
			usage.credits = append(usage.credits, tokenusage.Credit{Cursor: i, Tokens: 83000000 / parts, At: now.UnixMilli()})
			if err := store.advance(ctx, now); err != nil {
				t.Fatal(err)
			}
		}
		world, err := store.World(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		return world
	}
	whole, split := settle(1), settle(83)
	if whole.Level != 12 || whole.Counts["usage"] >= IslandCapacity || whole.Tokens != 83000000 {
		t.Fatalf("大额用量不应瞬间解锁几十级或填满整座岛: %+v", whole)
	}
	if whole.Counts["usage"] != split.Counts["usage"] || whole.Seed != split.Seed || whole.SeedGoal != split.SeedGoal ||
		whole.Level != split.Level || whole.LevelProgress != split.LevelProgress || whole.Resources != split.Resources || whole.Points != split.Points {
		t.Fatalf("相同用量分批结算改变了收益: 一次=%+v，分批=%+v", whole, split)
	}
}

func TestShop_PurchaseAndTreeAppearanceAreAtomic(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	store := openTestForest(t, filepath.Join(t.TempDir(), "forest.db"), testUsage{credits: []tokenusage.Credit{{Cursor: 1, Tokens: 20000, At: now.UnixMilli()}}})
	if err := store.advance(ctx, now); err != nil {
		t.Fatal(err)
	}
	style, accessory := 4, "ribbon"
	for _, edit := range []TreeEdit{{Style: &style}, {Accessory: &accessory}} {
		if err := store.EditTree(ctx, 1, edit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("未购买的外观不应允许使用: %v", err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE growth SET level=8; INSERT INTO rewards VALUES('fixture',500)`); err != nil {
		t.Fatal(err)
	}
	before, err := store.World(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Redeem(ctx, "maple", now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("材料不足应明确拒绝: %v", err)
	}
	failed, err := store.World(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Balance != before.Balance || failed.Resources != before.Resources {
		t.Fatal("购买失败扣除了资源或成就点")
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO forest_resources VALUES('fixture',20,10,5)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		for _, id := range []string{"maple", "ribbon"} {
			if err := store.Redeem(ctx, id, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := store.EditTree(ctx, 1, TreeEdit{Style: &style, Accessory: &accessory}); err != nil {
		t.Fatal(err)
	}
	tree, err := store.Tree(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Style != 4 || tree.Accessory != "ribbon" || tree.Kind != "usage" || tree.Variant != 7936 {
		t.Fatalf("换装应保存外观并保留树木来源和身份: %+v", tree)
	}
	final, err := store.World(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if final.Balance != before.Balance-65 || final.Resources != (Resources{Wood: 17, Seeds: 7, Dew: 5}) {
		t.Fatalf("重复购买不应重复扣费: %+v", final)
	}
	style, accessory, slot := 0, "", IslandCapacity
	if err := store.EditTree(ctx, 1, TreeEdit{Style: &style, Accessory: &accessory, Slot: &slot}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("非法位置应拒绝整次编辑: %v", err)
	}
	unchanged, err := store.Tree(ctx, 1)
	if err != nil || unchanged.Style != tree.Style || unchanged.Accessory != tree.Accessory {
		t.Fatalf("编辑失败不应部分保存: %+v, %v", unchanged, err)
	}
}

func TestEvents_KeepExactTreeAndStablePagination(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	store := openTestForest(t, filepath.Join(t.TempDir(), "forest.db"), testUsage{credits: []tokenusage.Credit{{Cursor: 1, Tokens: 83000000, At: now.UnixMilli()}}})
	if err := store.advance(ctx, now); err != nil {
		t.Fatal(err)
	}
	for key, treeID := range map[string]int64{"achievement:first": 1, "achievement:grove": 12} {
		event, err := store.Event(ctx, key)
		if err != nil || event.TreeID != treeID {
			t.Fatalf("事件应关联触发时的树: %s, %+v, %v", key, event, err)
		}
	}
	aphids, err := store.Event(ctx, "aphids:1")
	if err != nil {
		t.Fatal(err)
	}
	ladybug, err := store.Event(ctx, "ladybug:1")
	if err != nil {
		t.Fatal(err)
	}
	if aphids.TreeID == 0 || aphids.TreeID != ladybug.TreeID || aphids.Effects.Damaged != 1 || ladybug.Effects.Restored != 1 {
		t.Fatalf("虫害与修复应追溯到同一棵树: %+v, %+v", aphids, ladybug)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 130; i++ {
		if err := event(ctx, tx, fmt.Sprintf("fixture:%d", i), "2026-11-25", now, 1, "来信", "海风送来了消息。"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var cursor int64
	var instance string
	for {
		feed, err := store.Events(ctx, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if feed.Instance == "" || (instance != "" && feed.Instance != instance) || len(feed.Events) > 64 {
			t.Fatalf("消息分页契约不符: %+v", feed)
		}
		instance = feed.Instance
		for _, item := range feed.Events {
			if seen[item.ID] || item.Sequence <= cursor {
				t.Fatalf("分页重复或倒退: %+v", item)
			}
			seen[item.ID] = true
			cursor = item.Sequence
		}
		if feed.Cursor != cursor {
			t.Fatal("游标未落在最后一条消息")
		}
		if !feed.More {
			break
		}
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM forest_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if len(seen) != count {
		t.Fatalf("消息漏读: 已读 %d，实际 %d", len(seen), count)
	}
	if err := store.advance(ctx, now); err != nil {
		t.Fatal(err)
	}
	tail, err := store.Events(ctx, cursor)
	if err != nil || len(tail.Events) != 0 || tail.Cursor != cursor {
		t.Fatalf("重放产生了重复通知: %+v, %v", tail, err)
	}
}

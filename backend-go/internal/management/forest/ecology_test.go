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

func seedEcology(t *testing.T, store *Store, count int, now time.Time) {
	t.Helper()
	ctx := context.Background()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for range count {
		if err := plant(ctx, tx, "usage", now.Format("2006-01-02"), now, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func ecologyWorld(t *testing.T, store *Store, now time.Time) World {
	t.Helper()
	w, err := store.World(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestPatrol_ChangesInventoryAndTreeConditionOnlyOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	store := openTestForest(t, filepath.Join(t.TempDir(), "forest.db"), testUsage{})
	seedEcology(t, store, 6, now)
	pest, err := store.Event(ctx, "aphids:1")
	if err != nil {
		t.Fatal(err)
	}
	if pest.Effects.Damaged != 1 || len(pest.Affected) != 1 || pest.Affected[0].After != "damaged" {
		t.Fatalf("虫害必须留下具体受损树木: %+v", pest)
	}
	tree, err := store.Tree(ctx, pest.TreeID)
	if err != nil || tree.Condition != "damaged" {
		t.Fatalf("树冠状态未保存: %+v, %v", tree, err)
	}
	before := ecologyWorld(t, store, now)
	if before.Ecology.Damaged != 1 || before.Counts["usage"] != 6 {
		t.Fatalf("受损统计不符: %+v", before)
	}
	seedEcology(t, store, 7, now.Add(time.Minute))
	flight, err := store.Event(ctx, "bird:1")
	if err != nil {
		t.Fatal(err)
	}
	if flight.Effects.Seeds != 3 || flight.Effects.Wood != -1 {
		t.Fatalf("鸟带来的种子和邮费未入账: %+v", flight)
	}
	tree, err = store.Tree(ctx, pest.TreeID)
	if err != nil || tree.Condition != "healthy" || tree.ConditionRevision != 2 {
		t.Fatalf("瓢虫没有修复原来的树: %+v, %v", tree, err)
	}
	after := ecologyWorld(t, store, now)
	if after.Resources != (Resources{Wood: 12, Seeds: 5, Dew: 1}) || after.Ecology.Damaged != 0 {
		t.Fatalf("实际库存与事件不一致: %+v", after)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, key := range []string{"aphids:1", "ladybug:1", "bird:1"} {
		if err := event(ctx, tx, key, "2026-11-25", now, 1, "重复", "重复"); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	repeated := ecologyWorld(t, store, now)
	if repeated.Resources != after.Resources || repeated.Ecology != after.Ecology {
		t.Fatalf("重复事件改变了资源或树木: %+v", repeated)
	}
}

// 固定世界种子，选定测试需要的概率分支，不依赖随机测试结果。
func chooseAbsenceRoll(t *testing.T, store *Store, lastUsage int64, match func(int, int) bool) {
	t.Helper()
	ctx := context.Background()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 1000; i++ {
		if _, err := tx.ExecContext(ctx, `UPDATE forest_identity SET value=? WHERE id=1`, fmt.Sprintf("ecology-fixture-%d", i)); err != nil {
			t.Fatal(err)
		}
		rng, err := eventRandom(ctx, tx, fmt.Sprintf("absence:%d", lastUsage))
		if err != nil {
			t.Fatal(err)
		}
		if match(rng.IntN(100), rng.IntN(100)) {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("未找到测试需要的固定概率分支")
}

func TestAbsence_GraceRiskAndSingleSettlementSurviveRestart(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct{ days, want int }{{-1, 0}, {3, 0}, {4, 5}, {7, 20}, {14, 55}, {30, 75}, {365, 75}} {
		if got := absenceRisk(time.Duration(test.days) * 24 * time.Hour); got != test.want {
			t.Fatalf("闲置 %d 天概率=%d，预期 %d", test.days, got, test.want)
		}
	}
	path := filepath.Join(t.TempDir(), "forest.db")
	usage := &testUsage{credits: []tokenusage.Credit{{Cursor: 1, Tokens: 1, At: start.UnixMilli()}}}
	store := openTestForest(t, path, usage)
	seedEcology(t, store, 96, start)
	if err := store.advance(ctx, start); err != nil {
		t.Fatal(err)
	}
	chooseAbsenceRoll(t, store, start.UnixMilli(), func(chance, kind int) bool { return chance < 75 && kind < 36 })
	if err := store.Visit(ctx, start.AddDate(0, 0, 3)); err != nil {
		t.Fatal(err)
	}
	if w := ecologyWorld(t, store, start); w.Ecology.Destroyed != 0 {
		t.Fatal("宽限期发生了灾害")
	}
	returned := start.AddDate(0, 0, 30)
	if err := store.Visit(ctx, returned); err != nil {
		t.Fatal(err)
	}
	result, err := store.Event(ctx, fmt.Sprintf("absence:%d", start.UnixMilli()))
	if err != nil {
		t.Fatal(err)
	}
	if result.Effects.Destroyed != 7 || result.Effects.AwayDays != 30 || result.Effects.RiskPercent != 75 || len(result.Affected) != 7 {
		t.Fatalf("适中海啸应有受限而真实的损失: %+v", result)
	}
	before := ecologyWorld(t, store, returned)
	if before.Counts["usage"] != 89 || before.Ecology.Destroyed != 7 || before.Resources.Wood < 0 || before.Resources.Seeds < 0 || before.Resources.Dew < 0 {
		t.Fatalf("灾后统计或库存错误: %+v", before)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestForest(t, path, usage)
	for range 3 {
		if err := reopened.Visit(ctx, returned.AddDate(0, 0, 10)); err != nil {
			t.Fatal(err)
		}
	}
	after := ecologyWorld(t, reopened, returned)
	if before.Resources != after.Resources || before.Ecology != after.Ecology || before.Counts["usage"] != after.Counts["usage"] {
		t.Fatalf("重启和刷新重复处罚: %+v", after)
	}
}

func TestAbsence_NoHitAndFailedWriteDoNotReroll(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	usage := &testUsage{credits: []tokenusage.Credit{{Cursor: 1, Tokens: 1, At: start.UnixMilli()}}}
	store := openTestForest(t, filepath.Join(t.TempDir(), "forest.db"), usage)
	seedEcology(t, store, 20, start)
	if err := store.advance(ctx, start); err != nil {
		t.Fatal(err)
	}
	chooseAbsenceRoll(t, store, start.UnixMilli(), func(chance, _ int) bool { return chance >= 75 })
	before := ecologyWorld(t, store, start)
	if err := store.Visit(ctx, start.AddDate(0, 0, 7)); err != nil {
		t.Fatal(err)
	}
	if err := store.Visit(ctx, start.AddDate(0, 0, 365)); err != nil {
		t.Fatal(err)
	}
	after := ecologyWorld(t, store, start)
	if before.Resources != after.Resources || before.Ecology != after.Ecology {
		t.Fatal("未触发灾害的一次离开被重新抽取")
	}
	// 新用量开始新一段闲置；模拟入账故障，确认失败不会留下半次灾害。
	resumed := start.AddDate(0, 0, 366)
	usage.credits = append(usage.credits, tokenusage.Credit{Cursor: 2, Tokens: 1, At: resumed.UnixMilli()})
	if err := store.advance(ctx, resumed); err != nil {
		t.Fatal(err)
	}
	chooseAbsenceRoll(t, store, resumed.UnixMilli(), func(chance, kind int) bool { return chance < 75 && kind < 36 })
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER reject_disaster BEFORE INSERT ON event_affected_trees WHEN NEW.event_key LIKE 'absence:%' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.Visit(ctx, resumed.AddDate(0, 0, 30)); err == nil {
		t.Fatal("写入失败应向调用方报错")
	}
	failed := ecologyWorld(t, store, resumed)
	if failed.Resources != after.Resources || failed.Ecology != after.Ecology {
		t.Fatal("失败事务保留了部分损失")
	}
	if _, err := store.db.ExecContext(ctx, `DROP TRIGGER reject_disaster`); err != nil {
		t.Fatal(err)
	}
	if err := store.Visit(ctx, resumed.AddDate(0, 0, 30)); err != nil {
		t.Fatal(err)
	}
	result, err := store.Event(ctx, fmt.Sprintf("absence:%d", resumed.UnixMilli()))
	if err != nil || result.Effects.Destroyed != 2 || result.Effects.Dew != -1 {
		t.Fatalf("失败后重试应使用原抽取结果: %+v, %v", result, err)
	}
	if w := ecologyWorld(t, store, resumed); w.Resources.Dew != 0 {
		t.Fatal("库存不足时应记录实际损失，不能扣成负数")
	}
}

func TestActivityFollowsRealUsageFacts(t *testing.T) {
	t.Run("用量事实时间与零值迟到补记", func(t *testing.T) {
		ctx := context.Background()
		start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
		usage := &testUsage{credits: []tokenusage.Credit{{Cursor: 1, Tokens: 1, At: start.UnixMilli()}}}
		store := openTestForest(t, filepath.Join(t.TempDir(), "forest.db"), usage)
		seedEcology(t, store, 20, start)
		if err := store.advance(ctx, start); err != nil {
			t.Fatal(err)
		}
		later := start.AddDate(0, 0, 30)
		usage.credits = append(usage.credits, tokenusage.Credit{Cursor: 2, Tokens: 0, At: later.UnixMilli()}, tokenusage.Credit{Cursor: 3, Tokens: 1, At: start.Add(-time.Hour).UnixMilli()})
		if err := store.advance(ctx, later); err != nil {
			t.Fatal(err)
		}
		var active int64
		if err := store.db.QueryRowContext(ctx, `SELECT last_usage FROM forest_activity`).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active != start.UnixMilli() {
			t.Fatal("零用量或迟到补记重置了活跃时间")
		}
		before := ecologyWorld(t, store, later)
		usage.activityErr = errors.New("账本不可读")
		if err := store.Visit(ctx, later); !errors.Is(err, usage.activityErr) {
			t.Fatalf("账本失败未显式返回: %v", err)
		}
		after := ecologyWorld(t, store, later)
		if before.Resources != after.Resources || before.Ecology != after.Ecology {
			t.Fatal("未知用量状态不应造成损失")
		}
	})
	t.Run("持续积压批次不被判为离开", func(t *testing.T) {
		ctx := context.Background()
		start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
		for _, mode := range []string{"多批连续用量", "批内乱序用量", "分页乱序用量"} {
			t.Run(mode, func(t *testing.T) {
				usage := &testUsage{credits: []tokenusage.Credit{{Cursor: 1, Tokens: 1, At: start.UnixMilli()}}}
				store := openTestForest(t, filepath.Join(t.TempDir(), "forest.db"), usage)
				seedEcology(t, store, 20, start)
				if err := store.advance(ctx, start); err != nil {
					t.Fatal(err)
				}
				chooseAbsenceRoll(t, store, start.UnixMilli(), func(chance, _ int) bool { return chance < 5 })
				if mode == "多批连续用量" {
					for hour := 1; hour <= 520; hour++ {
						usage.credits = append(usage.credits, tokenusage.Credit{Cursor: int64(hour + 1), Tokens: 1, At: start.Add(time.Duration(hour) * time.Hour).UnixMilli()})
					}
				} else if mode == "分页乱序用量" {
					for i := 0; i < 256; i++ {
						usage.credits = append(usage.credits, tokenusage.Credit{Cursor: int64(i + 2), Tokens: 1, At: start.AddDate(0, 0, 9).UnixMilli()})
					}
					for day := 1; day < 9; day++ {
						usage.credits = append(usage.credits, tokenusage.Credit{Cursor: int64(len(usage.credits) + 1), Tokens: 1, At: start.AddDate(0, 0, day).UnixMilli()})
					}
				} else {
					for _, day := range []int{9, 3, 6, 1, 8, 4, 7, 2, 5} {
						usage.credits = append(usage.credits, tokenusage.Credit{Cursor: int64(len(usage.credits) + 1), Tokens: 1, At: start.AddDate(0, 0, day).UnixMilli()})
					}
				}
				latest, err := usage.LatestGrowthUsage(ctx)
				if err != nil {
					t.Fatal(err)
				}
				now := time.UnixMilli(latest)
				before := ecologyWorld(t, store, now)
				if err := store.Visit(ctx, now); err != nil {
					t.Fatal(err)
				}
				var last, checked int64
				if err := store.db.QueryRowContext(ctx, `SELECT last_usage,checked_usage FROM forest_activity`).Scan(&last, &checked); err != nil {
					t.Fatal(err)
				}
				if last != start.UnixMilli() || checked != 0 {
					t.Fatal("回访不应跳过尚未消费的实际用量")
				}
				for range 3 {
					if err := store.advance(ctx, now); err != nil {
						t.Fatal(err)
					}
				}
				after := ecologyWorld(t, store, now)
				if before.Resources != after.Resources || before.Ecology != after.Ecology {
					t.Fatal("持续使用的积压批次被误判为离开")
				}
				if err := store.db.QueryRowContext(ctx, `SELECT last_usage,checked_usage FROM forest_activity`).Scan(&last, &checked); err != nil {
					t.Fatal(err)
				}
				if last != latest || checked != 0 {
					t.Fatal("活跃时间未按事实推进或产生了误判")
				}
			})
		}
	})
	t.Run("回访早于消费时保留真实断档", func(t *testing.T) {
		ctx := context.Background()
		start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
		usage := &testUsage{credits: []tokenusage.Credit{{Cursor: 1, Tokens: 1, At: start.UnixMilli()}}}
		store := openTestForest(t, filepath.Join(t.TempDir(), "forest.db"), usage)
		seedEcology(t, store, 20, start)
		if err := store.advance(ctx, start); err != nil {
			t.Fatal(err)
		}
		chooseAbsenceRoll(t, store, start.UnixMilli(), func(chance, _ int) bool { return chance < 25 })
		returned := start.AddDate(0, 0, 8)
		usage.credits = append(usage.credits, tokenusage.Credit{Cursor: 2, Tokens: 1, At: returned.UnixMilli()})
		if err := store.Visit(ctx, returned); err != nil {
			t.Fatal(err)
		}
		if err := store.advance(ctx, returned); err != nil {
			t.Fatal(err)
		}
		e, err := store.Event(ctx, fmt.Sprintf("absence:%d", start.UnixMilli()))
		if err != nil || e.Effects.AwayDays != 8 || e.Effects.Damaged != 2 {
			t.Fatalf("回访丢失了真实断档: %+v, %v", e, err)
		}
		before := ecologyWorld(t, store, returned)
		if err := store.Visit(ctx, returned); err != nil {
			t.Fatal(err)
		}
		if err := store.advance(ctx, returned); err != nil {
			t.Fatal(err)
		}
		after := ecologyWorld(t, store, returned)
		if before.Resources != after.Resources || before.Ecology != after.Ecology {
			t.Fatal("恢复使用与回访重复结算了同一段闲置")
		}
	})
}

func TestRecovery_IsAtomicIdempotentAndKeepsIdentity(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	store := openTestForest(t, filepath.Join(t.TempDir(), "forest.db"), testUsage{})
	seedEcology(t, store, 1, now)
	name := "海边的老朋友"
	if err := store.EditTree(ctx, 1, TreeEdit{Name: &name}); err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := event(ctx, tx, "fixture-damage", "2026-11-25", now, 1, "海啸", "残桩"); err != nil {
		t.Fatal(err)
	}
	if err := changeCondition(ctx, tx, "fixture-damage", 1, "healthy", "destroyed", now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	original, err := store.Tree(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	command := RecoveryCommand{Revision: original.ConditionRevision}
	before := ecologyWorld(t, store, now)
	if err := store.RecoverTree(ctx, 1, command, now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("材料不足应拒绝重种: %v", err)
	}
	failed := ecologyWorld(t, store, now)
	if before.Resources != failed.Resources || before.Ecology != failed.Ecology {
		t.Fatal("失败重种扣费或改变了树木")
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO forest_resources VALUES('fixture',10,3,3)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER reject_recovery BEFORE INSERT ON event_affected_trees WHEN NEW.event_key LIKE 'recovery:%' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	funded := ecologyWorld(t, store, now)
	if err := store.RecoverTree(ctx, 1, command, now); err == nil {
		t.Fatal("恢复事件写入失败应报错")
	}
	rolledBack := ecologyWorld(t, store, now)
	if rolledBack.Resources != funded.Resources || rolledBack.Ecology != funded.Ecology {
		t.Fatal("恢复事件失败后仍留下了扣费或状态变化")
	}
	if _, err := store.db.ExecContext(ctx, `DROP TRIGGER reject_recovery`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := store.RecoverTree(ctx, 1, command, now); err != nil {
			t.Fatal(err)
		}
	}
	restored, err := store.Tree(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Condition != "healthy" || restored.ConditionRevision != 2 || restored.Name != original.Name || restored.Variant != original.Variant || restored.Planted != original.Planted || restored.Slot != original.Slot {
		t.Fatalf("重种破坏了身份或未恢复: %+v", restored)
	}
	after := ecologyWorld(t, store, now)
	if after.Resources != (Resources{Wood: 8, Seeds: 2, Dew: 2}) || after.Points != before.Points || after.Counts["usage"] != 1 {
		t.Fatalf("重种重复扣费或重复发奖: %+v", after)
	}
	result, err := store.Event(ctx, "recovery:1:1")
	if err != nil || result.Effects.Restored != 1 || result.Effects.Wood != -3 || result.Effects.Seeds != -1 || result.Effects.Dew != -1 {
		t.Fatalf("恢复事件的账目不完整: %+v, %v", result, err)
	}
	tx, err = store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := event(ctx, tx, "fixture-second-damage", "2026-11-25", now, 1, "风暴", "折枝"); err != nil {
		t.Fatal(err)
	}
	if err := changeCondition(ctx, tx, "fixture-second-damage", 1, "healthy", "damaged", now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverTree(ctx, 1, command, now); err != nil {
		t.Fatal(err)
	}
	stillDamaged, err := store.Tree(ctx, 1)
	if err != nil || stillDamaged.Condition != "damaged" || stillDamaged.ConditionRevision != 3 {
		t.Fatalf("旧恢复请求不应修复新一轮损伤: %+v, %v", stillDamaged, err)
	}
	if w := ecologyWorld(t, store, now); w.Resources != after.Resources {
		t.Fatal("旧恢复请求重复扣费")
	}
}

package forest

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
	"github.com/insistanan/api-proxy/internal/storage/sqlitedb"
)

type testUsage struct {
	credits     []tokenusage.Credit
	baseline    int64
	available   bool
	activityErr error
}

func TestGrowth_UnavailableBaselineKeepsExistingTargets(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	usage := &testUsage{available: true, baseline: 2400000}
	store := openTestForest(t, filepath.Join(t.TempDir(), "forest.db"), usage)
	if err := store.advance(ctx, now); err != nil {
		t.Fatal(err)
	}
	usage.available, usage.baseline = false, 0
	usage.credits = []tokenusage.Credit{{Cursor: 1, Tokens: 20000, At: now.AddDate(0, 0, 8).UnixMilli()}}
	if err := store.advance(ctx, now.AddDate(0, 0, 8)); err != nil {
		t.Fatal(err)
	}
	state, err := readGrowth(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	if state.baseline != 2400000 || state.baselineAt != now.UnixMilli() || state.seedGoal != 250000 || state.cursor != 1 {
		t.Fatalf("基准不可用时应保留原基准并继续结算: %+v", state)
	}
	usage.available = true
	if err := store.advance(ctx, now.AddDate(0, 0, 8)); err != nil {
		t.Fatal(err)
	}
	state, err = readGrowth(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	if state.baseline != 0 || state.seedGoal != 250000 {
		t.Fatalf("确认历史不足后应清除基准并保留进行中的目标: %+v", state)
	}
}

func (s testUsage) Credits(_ context.Context, after int64) ([]tokenusage.Credit, error) {
	var result []tokenusage.Credit
	for _, credit := range s.credits {
		if credit.Cursor > after {
			result = append(result, credit)
			if len(result) == 256 {
				break
			}
		}
	}
	return result, nil
}

func (testUsage) GrowthDate(now time.Time) string { return now.UTC().Format("2006-01-02") }
func (s testUsage) LatestGrowthUsage(context.Context) (int64, error) {
	var latest int64
	for _, credit := range s.credits {
		if credit.Tokens > 0 {
			latest = max(latest, credit.At)
		}
	}
	return latest, s.activityErr
}
func (s testUsage) PreviousGrowthUsage(_ context.Context, before int64) (int64, error) {
	var previous int64
	for _, credit := range s.credits {
		if credit.Tokens > 0 && credit.At < before {
			previous = max(previous, credit.At)
		}
	}
	return previous, s.activityErr
}
func (s testUsage) GrowthBaseline(context.Context, time.Time) (int64, bool, error) {
	return s.baseline, s.available, nil
}

func openTestForest(t *testing.T, path string, usage UsageSource) *Store {
	t.Helper()
	db, err := sqlitedb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return &Store{db: db, usage: usage}
}

func TestGrowth_CommitSurvivesReplayAndKeepsIslandLayout(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "forest.db")
	usage := &testUsage{credits: []tokenusage.Credit{{Cursor: 9, Tokens: 20000, At: now.UnixMilli()}}}
	store := openTestForest(t, path, usage)
	if err := store.advance(ctx, now); err != nil {
		t.Fatal(err)
	}
	name := "第一棵"
	slot := 11
	if err := store.EditTree(ctx, 1, TreeEdit{Name: &name, Slot: &slot}); err != nil {
		t.Fatal(err)
	}
	if err := store.EditIsland(ctx, 1, "起点", true); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTestForest(t, path, usage)
	for day := 1; day <= 12; day++ {
		usage.credits = append(usage.credits, tokenusage.Credit{Cursor: 9 + int64(day), Tokens: 2000000, At: now.AddDate(0, 0, day).UnixMilli()})
		if err := reopened.advance(ctx, now.AddDate(0, 0, day)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := reopened.advance(ctx, now.AddDate(0, 0, 12)); err != nil {
			t.Fatal(err)
		}
	}
	world, err := reopened.World(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if world.Tokens != 20000+24000000 || world.Counts["usage"] != IslandCapacity+1 ||
		world.Islands != 2 || world.Latest == nil || world.Latest.Island != 2 ||
		world.Points != IslandCapacity+1+(world.Level-1)*3+50+3 || len(world.Favorites) != 1 {
		t.Fatalf("重放或扩岛后的状态不符: %+v", world)
	}
	if len(world.Discoveries) < 2 || world.Discoveries[0].Kind != "usage" ||
		world.Discoveries[0].Style != 0 || world.Discoveries[0].FirstPlanted != now.UnixMilli() {
		t.Fatalf("图鉴应保留首次种植记录: %+v", world.Discoveries)
	}
	first, err := reopened.Island(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Trees) != IslandCapacity || first.Trees[0].Name != name || first.Trees[0].Slot != slot ||
		first.Trees[0].AssetVersion != 1 || first.Island.Name != "起点" || !first.Island.Favorite {
		t.Fatalf("扩岛影响了已编辑的树木或岛屿: %+v", first)
	}
}

func TestTimer_HandoffAndRestConfirmationSettleOnlyEarnedTime(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	store := openTestForest(t, filepath.Join(t.TempDir(), "forest.db"), testUsage{})
	var timer Timer
	change := func(at int, action string, minutes int64) {
		t.Helper()
		var err error
		timer, err = store.ChangeTimer(ctx, TimerCommand{Revision: timer.Revision, Action: action, Minutes: minutes},
			start.Add(time.Duration(at)*time.Minute))
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	worldAt := func(at int) World {
		t.Helper()
		world, err := store.World(ctx, start.Add(time.Duration(at)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return world
	}
	change(0, "focus", 50)
	change(12, "handoff", 15)
	waiting := worldAt(62)
	if waiting.Focus != 12*60000 || waiting.Rest != 0 || waiting.Timer.Mode != "" || waiting.Timer.WaitDue == 0 || waiting.Points != 0 {
		t.Fatalf("等待时间被计成专注或休息: %+v", waiting)
	}
	change(62, "focus", 38)
	if err := store.finishDueFocus(ctx, start.Add(104*time.Minute)); err != nil {
		t.Fatal(err)
	}
	settled := worldAt(104)
	timer = settled.Timer
	if timer.WaitStarted == 0 || timer.Mode != "focus-due" || settled.Counts["focus"] != 1 || settled.Focus != 0 {
		t.Fatalf("专注到期的结算与等待状态不符: %+v", settled)
	}
	change(104, "end-wait", 0)
	change(104, "rest", 10)
	before := worldAt(114)
	if before.Counts["rest"] != 0 {
		t.Fatalf("休息未确认便种了树: %+v", before)
	}
	old := timer.Revision
	change(114, "confirm-rest", 0)
	if _, err := store.ChangeTimer(ctx, TimerCommand{Revision: old, Action: "confirm-rest"}, start.Add(114*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("重复确认未返回冲突: %v", err)
	}
	final := worldAt(114)
	if final.Counts["focus"] != 1 || final.Counts["rest"] != 1 || final.Counts["usage"] != 0 ||
		final.Rest != 0 || final.Level != 1 || final.Points != 29 || final.Resources != (Resources{Wood: 2, Seeds: 1, Dew: 2}) {
		t.Fatalf("三类进度或奖励结算不符: %+v", final)
	}
}

package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
	"github.com/insistanan/api-proxy/internal/storage/sqlitedb"
)

type UsageSource interface {
	Credits(context.Context, int64) ([]tokenusage.Credit, error)
	GrowthDate(time.Time) string
	GrowthBaseline(context.Context, time.Time) (value int64, available bool, err error)
}

const schema = `
CREATE TABLE IF NOT EXISTS growth (
 id INTEGER PRIMARY KEY CHECK(id=1), cursor INTEGER NOT NULL DEFAULT 0,
 tokens INTEGER NOT NULL DEFAULT 0, seed_progress INTEGER NOT NULL DEFAULT 0, seed_goal INTEGER NOT NULL DEFAULT 20000,
 focus INTEGER NOT NULL DEFAULT 0, rest INTEGER NOT NULL DEFAULT 0,
 level INTEGER NOT NULL DEFAULT 1, level_progress INTEGER NOT NULL DEFAULT 0, level_goal INTEGER NOT NULL DEFAULT 20000,
 baseline INTEGER NOT NULL DEFAULT 0, baseline_at INTEGER NOT NULL DEFAULT 0, rules INTEGER NOT NULL DEFAULT 1
);
INSERT INTO growth(id) VALUES(1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS islands (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT '', favorite INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS trees (
 id INTEGER PRIMARY KEY, kind TEXT NOT NULL, style INTEGER NOT NULL, day TEXT NOT NULL, planted INTEGER NOT NULL,
 island INTEGER NOT NULL REFERENCES islands(id), slot INTEGER NOT NULL, name TEXT NOT NULL DEFAULT '',
 variant INTEGER NOT NULL, asset_version INTEGER NOT NULL DEFAULT 1, UNIQUE(island,slot)
);
CREATE INDEX IF NOT EXISTS trees_day ON trees(day,id);
CREATE TABLE IF NOT EXISTS discoveries (
 kind TEXT NOT NULL, style INTEGER NOT NULL, first_planted INTEGER NOT NULL,
 PRIMARY KEY(kind,style)
);
INSERT OR IGNORE INTO discoveries(kind,style,first_planted)
 SELECT kind,style,MIN(planted) FROM trees
 WHERE NOT EXISTS(SELECT 1 FROM discoveries) GROUP BY kind,style;
CREATE TABLE IF NOT EXISTS rewards (key TEXT PRIMARY KEY, points INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS forest_events (
 key TEXT PRIMARY KEY, day TEXT NOT NULL, at_ms INTEGER NOT NULL, island INTEGER NOT NULL, title TEXT NOT NULL, story TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS forest_events_day ON forest_events(day,at_ms);
CREATE TABLE IF NOT EXISTS timer (
 id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL DEFAULT 0,
 mode TEXT NOT NULL DEFAULT '', started INTEGER NOT NULL DEFAULT 0, due INTEGER NOT NULL DEFAULT 0,
 duration INTEGER NOT NULL DEFAULT 0, left_ms INTEGER NOT NULL DEFAULT 0,
 wait_started INTEGER NOT NULL DEFAULT 0, wait_due INTEGER NOT NULL DEFAULT 0
);
INSERT INTO timer(id) VALUES(1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS timer_segments (
 id INTEGER PRIMARY KEY, revision INTEGER NOT NULL, kind TEXT NOT NULL, started INTEGER NOT NULL,
 ended INTEGER NOT NULL, UNIQUE(revision,kind)
);`

const IslandCapacity = 96
const GroupCapacity = 8
const focusGoal int64 = 50 * 60000
const restGoal int64 = 10 * 60000

var ErrInvalid = errors.New("参数或操作无效")
var ErrConflict = errors.New("状态已变化，请刷新后重试")

type Store struct {
	db        *sql.DB
	usage     UsageSource
	cancel    context.CancelFunc
	done      chan struct{}
	syncIssue atomic.Bool
}

func Open(path string, usage UsageSource) (*Store, error) {
	db, err := sqlitedb.Open(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, errors.Join(fmt.Errorf("初始化个人森林: %w", err), db.Close())
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Store{db: db, usage: usage, cancel: cancel, done: make(chan struct{})}
	go s.consume(ctx)
	return s, nil
}

func (s *Store) Close() error {
	s.cancel()
	<-s.done
	return s.db.Close()
}

func (s *Store) consume(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			err := errors.Join(s.advance(ctx, now), s.finishDueFocus(ctx, now))
			if err != nil && ctx.Err() == nil && !s.syncIssue.Swap(true) {
				log.Printf("[Forest-Growth] 成长结算中断，将从已提交位置重试: %v", err)
			}
			if err == nil {
				s.syncIssue.Store(false)
			}
		}
	}
}

func (s *Store) finishDueFocus(ctx context.Context, now time.Time) error {
	t, err := readTimer(ctx, s.db, now)
	if err != nil || t.Mode != "focus" || t.Due > t.ServerNow {
		return err
	}
	_, err = s.ChangeTimer(ctx, TimerCommand{Revision: t.Revision, Action: "finish-focus"}, now)
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

type growth struct {
	cursor, tokens, seed, seedGoal, focus, rest, level, progress, goal, baseline, baselineAt int64
}

type growthReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readGrowth(ctx context.Context, db growthReader) (growth, error) {
	var g growth
	err := db.QueryRowContext(ctx, `SELECT cursor,tokens,seed_progress,seed_goal,focus,rest,level,level_progress,level_goal,baseline,baseline_at FROM growth WHERE id=1`).
		Scan(&g.cursor, &g.tokens, &g.seed, &g.seedGoal, &g.focus, &g.rest, &g.level, &g.progress, &g.goal, &g.baseline, &g.baselineAt)
	return g, err
}

func (g growth) save(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE growth SET cursor=?,tokens=?,seed_progress=?,seed_goal=?,focus=?,rest=?,level=?,level_progress=?,level_goal=?,baseline=?,baseline_at=? WHERE id=1`,
		g.cursor, g.tokens, g.seed, g.seedGoal, g.focus, g.rest, g.level, g.progress, g.goal, g.baseline, g.baselineAt)
	return err
}

func (s *Store) advance(ctx context.Context, now time.Time) error {
	day := s.usage.GrowthDate(now)
	if day == "" {
		return nil
	}
	g, err := readGrowth(ctx, s.db)
	if err != nil {
		return err
	}
	credits, err := s.usage.Credits(ctx, g.cursor)
	if err != nil {
		return err
	}
	refresh := now.UnixMilli()-g.baselineAt >= (7*24*time.Hour).Milliseconds() ||
		(g.baseline == 0 && now.UnixMilli()-g.baselineAt >= (24*time.Hour).Milliseconds())
	var baseline int64
	var baselineErr error
	if refresh {
		baseline, refresh, baselineErr = s.usage.GrowthBaseline(ctx, now)
	}
	if len(credits) == 0 && g.seed < g.seedGoal && g.progress < g.goal && !refresh {
		return baselineErr
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	g, err = readGrowth(ctx, tx)
	if err != nil {
		return err
	}
	if refresh {
		g.baseline, g.baselineAt = baseline, now.UnixMilli()
	}
	for _, credit := range credits {
		if credit.Cursor <= g.cursor {
			continue
		}
		g.cursor = credit.Cursor
		g.tokens += credit.Tokens
		g.seed += credit.Tokens
		g.progress += credit.Tokens
	}
	for i := 0; i < 128 && g.progress >= g.goal; i++ {
		g.progress -= g.goal
		g.level++
		g.goal = levelGoal(g.level, g.baseline)
		if err := reward(ctx, tx, fmt.Sprintf("level:%d", g.level), 3); err != nil {
			return err
		}
		if title := levelTitles[g.level]; title != "" {
			if err := event(ctx, tx, fmt.Sprintf("level:%d", g.level), day, now, 0, title, "新的树种或装饰已加入图鉴。"); err != nil {
				return err
			}
		}
	}
	for i := 0; i < 128 && g.seed >= g.seedGoal; i++ {
		g.seed -= g.seedGoal
		g.seedGoal = 100000
		if g.baseline > 0 {
			g.seedGoal = max(20000, g.baseline/12)
		}
		if err := plant(ctx, tx, "usage", day, now, g.level); err != nil {
			return err
		}
	}
	if err := g.save(ctx, tx); err != nil {
		return err
	}
	return errors.Join(tx.Commit(), baselineErr)
}

var levelTitles = map[int64]string{5: "松林开放", 10: "林间步道", 15: "白桦开放", 20: "海湾灯塔", 30: "花期开放", 50: "岛屿纪念碑"}

func levelGoal(level, baseline int64) int64 {
	linear := 20000 + 10000*(level-1)
	switch {
	case level < 20:
		return linear
	case baseline == 0:
		return 3 * linear
	default:
		return max(1, int64(float64(baseline)*(1.2+0.03*float64(level-20))))
	}
}

func reward(ctx context.Context, tx *sql.Tx, key string, points int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO rewards VALUES(?,?) ON CONFLICT DO NOTHING`, key, points)
	return err
}

func event(ctx context.Context, tx *sql.Tx, key, day string, now time.Time, island int64, title, story string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO forest_events VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, key, day, now.UnixMilli(), island, title, story)
	return err
}

func plant(ctx context.Context, tx *sql.Tx, kind, day string, now time.Time, level int64) error {
	var last, count int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM trees`).Scan(&last); err != nil {
		return err
	}
	id := last + 1
	island := (id-1)/IslandCapacity + 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO islands(id) VALUES(?) ON CONFLICT DO NOTHING`, island); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT slot FROM trees WHERE island=?`, island)
	if err != nil {
		return err
	}
	defer rows.Close()
	var occupied [IslandCapacity]bool
	for rows.Next() {
		var slot int
		if err := rows.Scan(&slot); err != nil {
			return err
		}
		occupied[slot] = true
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var slot int
	for offset := 0; offset < IslandCapacity; offset++ {
		index := offset
		switch kind {
		case "focus":
			index = (offset + 32) % IslandCapacity
		case "rest":
			index = IslandCapacity - 1 - offset
		}
		if !occupied[index] {
			slot = index
			break
		}
	}
	variant := (id*7919 + 17) % 104729
	styles := int64(4)
	if kind == "usage" {
		styles = 1
		for _, unlocked := range []int64{5, 15, 30} {
			if level >= unlocked {
				styles++
			}
		}
	}
	style := variant % styles
	if _, err := tx.ExecContext(ctx, `INSERT INTO trees(id,kind,style,day,planted,island,slot,variant) VALUES(?,?,?,?,?,?,?,?)`,
		id, kind, style, day, now.UnixMilli(), island, slot, variant); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO discoveries VALUES(?,?,?) ON CONFLICT DO NOTHING`,
		kind, style, now.UnixMilli()); err != nil {
		return err
	}
	if err := reward(ctx, tx, fmt.Sprintf("tree:%d", id), 1); err != nil {
		return err
	}
	if count == 0 {
		if err := event(ctx, tx, fmt.Sprintf("island:%d", island), day, now, island, "新岛长出第一棵树", "海图上多了一处能靠岸的地方。"); err != nil {
			return err
		}
		if (island-1)%GroupCapacity == 0 {
			group := (island-1)/GroupCapacity + 1
			if err := event(ctx, tx, fmt.Sprintf("group:%d", group), day, now, island, fmt.Sprintf("群岛 %d 已标上海图", group), "巡护员给第一座岛画出了新的航线。"); err != nil {
				return err
			}
		}
	}
	if count == IslandCapacity-1 {
		if err := event(ctx, tx, fmt.Sprintf("full:%d", island), day, now, island, "岛上林地已成形", "航线已经点亮。下一棵树会种在新岛上。"); err != nil {
			return err
		}
	}
	switch kind {
	case "rest":
		stories := []string{
			"树枝上挂着一只歪枕头，晚来的巡护员听见了很轻的呼噜。",
			"它把根伸向天空。巡护员绕着它走了两圈，还是没找到正面。",
			"它结出两只颜色不同的拖鞋，附近的海鸥抢走了左脚那只。",
			"树冠躺在草地上。巡护员把这里标成了可以发呆的地方。",
		}
		if err := event(ctx, tx, fmt.Sprintf("rest-style:%d", style), day, now, island, "怪树图鉴有了新成员", stories[style]); err != nil {
			return err
		}
	case "focus":
		if err := event(ctx, tx, "focus-first", day, now, island, "林间出现第一棵专注树", "几段认真投入的时间在步道旁长出了枝条。"); err != nil {
			return err
		}
	}
	if kind == "usage" && style == 3 {
		if err := event(ctx, tx, "flower", day, now, island, "森林迎来第一次花期", "花开的地方成了巡护路线的新一站。"); err != nil {
			return err
		}
	}
	switch count {
	case 5:
		return event(ctx, tx, fmt.Sprintf("aphids:%d", island), day, now, island, "苗圃来了一只大肥虫", "巡护员发现了缺角的叶片，决定下次回来查看。")
	case 8:
		return followUp(ctx, tx, "aphids", "ladybug", island, day, now, "瓢虫来帮忙了", "上次缺角的叶片旁落下了瓢虫，苗圃又安静下来。")
	case 16:
		return event(ctx, tx, fmt.Sprintf("squirrel:%d", island), day, now, island, "巡护员发现一处松鼠储藏点", "小路旁的石头底下，藏着一堆松果。")
	case 40:
		return event(ctx, tx, fmt.Sprintf("storm:%d", island), day, now, island, "大雨冲断了林间步道", "巡护员标出了积水处，下次扩建时会补上踏石。")
	case 44:
		return followUp(ctx, tx, "storm", "stones", island, day, now, "雨后的步道铺上了踏石", "上次标出的积水处有了踏石。下一场雨也能沿这条路到海边。")
	}
	return nil
}

func followUp(ctx context.Context, tx *sql.Tx, previous, next string, island int64, day string, now time.Time, title, story string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO forest_events
SELECT ?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM forest_events WHERE key=?) ON CONFLICT DO NOTHING`,
		fmt.Sprintf("%s:%d", next, island), day, now.UnixMilli(), island, title, story, fmt.Sprintf("%s:%d", previous, island))
	return err
}

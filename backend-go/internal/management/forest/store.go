package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/storage/sqlitedb"
)

type UsageSource interface {
	Credits(context.Context, int64) ([]tokenusage.Credit, error)
	GrowthDate(time.Time) string
	GrowthBaseline(context.Context, time.Time) (value int64, available bool, err error)
	LatestGrowthUsage(context.Context) (int64, error)
	PreviousGrowthUsage(context.Context, int64) (int64, error)
}

const schema = `
CREATE TABLE IF NOT EXISTS forest_identity (id INTEGER PRIMARY KEY CHECK(id=1), value TEXT NOT NULL);
INSERT INTO forest_identity SELECT 1,lower(hex(randomblob(16))) WHERE NOT EXISTS(SELECT 1 FROM forest_identity);
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
CREATE TABLE IF NOT EXISTS forest_resources (key TEXT PRIMARY KEY, wood INTEGER NOT NULL DEFAULT 0, seeds INTEGER NOT NULL DEFAULT 0, dew INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS growth_days (day TEXT PRIMARY KEY, tokens INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS event_trees (event_key TEXT PRIMARY KEY REFERENCES forest_events(key), tree_id INTEGER NOT NULL REFERENCES trees(id));
CREATE TABLE IF NOT EXISTS tree_accessories (tree_id INTEGER PRIMARY KEY REFERENCES trees(id), item TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS forest_events (
 key TEXT PRIMARY KEY, day TEXT NOT NULL, at_ms INTEGER NOT NULL, island INTEGER NOT NULL, title TEXT NOT NULL, story TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS forest_events_day ON forest_events(day,at_ms);
CREATE TABLE IF NOT EXISTS forest_activity (
 id INTEGER PRIMARY KEY CHECK(id=1), last_usage INTEGER NOT NULL DEFAULT 0, checked_usage INTEGER NOT NULL DEFAULT 0
);
INSERT INTO forest_activity(id) VALUES(1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS tree_condition (
 tree_id INTEGER PRIMARY KEY REFERENCES trees(id), state TEXT NOT NULL CHECK(state IN ('healthy','damaged','destroyed')),
 revision INTEGER NOT NULL, changed_at INTEGER NOT NULL, cause_key TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS event_outcomes (
 event_key TEXT PRIMARY KEY REFERENCES forest_events(key), damaged INTEGER NOT NULL DEFAULT 0,
 destroyed INTEGER NOT NULL DEFAULT 0, restored INTEGER NOT NULL DEFAULT 0,
 away_days INTEGER NOT NULL DEFAULT 0, risk_percent INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS event_affected_trees (
 event_key TEXT NOT NULL REFERENCES forest_events(key), tree_id INTEGER NOT NULL REFERENCES trees(id),
 before_state TEXT NOT NULL, after_state TEXT NOT NULL, PRIMARY KEY(event_key,tree_id)
);
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
	db          *sql.DB
	usage       UsageSource
	cancel      context.CancelFunc
	done        chan struct{}
	syncIssue   atomic.Bool
	syncPending atomic.Bool
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
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-timer.C:
			var more bool
			var err error
			deadline := time.Now().Add(250 * time.Millisecond)
			for batch := 0; batch < 16 && ctx.Err() == nil; batch++ {
				more, err = s.advanceBatch(ctx, now)
				if err != nil || !more || time.Now().After(deadline) {
					break
				}
			}
			s.syncPending.Store(more)
			err = errors.Join(err, s.finishDueFocus(ctx, now))
			if err != nil && ctx.Err() == nil && !s.syncIssue.Swap(true) {
				log.Printf("[Forest-Growth] 成长结算中断，将从已提交位置重试: %v", err)
			}
			if err == nil {
				s.syncIssue.Store(false)
			}
			delay := 2 * time.Second
			if more && err == nil {
				delay = 10 * time.Millisecond
			}
			timer.Reset(delay)
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

// advance 保留单次结算入口；后台消费使用 advanceBatch 判断是否需要继续追赶。
func (s *Store) advance(ctx context.Context, now time.Time) error {
	_, err := s.advanceBatch(ctx, now)
	return err
}
func (s *Store) advanceBatch(ctx context.Context, now time.Time) (bool, error) {
	day := s.usage.GrowthDate(now)
	if day == "" {
		return false, nil
	}
	g, err := readGrowth(ctx, s.db)
	if err != nil {
		return false, err
	}
	credits, err := s.usage.Credits(ctx, g.cursor)
	if err != nil {
		return false, err
	}
	refresh := now.UnixMilli()-g.baselineAt >= (7*24*time.Hour).Milliseconds() ||
		(g.baseline == 0 && now.UnixMilli()-g.baselineAt >= (24*time.Hour).Milliseconds())
	var baseline int64
	var baselineErr error
	if refresh {
		baseline, refresh, baselineErr = s.usage.GrowthBaseline(ctx, now)
	}
	if len(credits) == 0 && g.seed < g.seedGoal && g.progress < g.goal && !refresh {
		return false, baselineErr
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	g, err = readGrowth(ctx, tx)
	if err != nil {
		return false, err
	}
	if refresh {
		g.baseline, g.baselineAt = baseline, now.UnixMilli()
	}
	if err := s.recordActivity(ctx, tx, credits, g.cursor, now); err != nil {
		return false, err
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
	var todayTokens int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT tokens FROM growth_days WHERE day=?),0)`, day).Scan(&todayTokens); err != nil {
		return false, err
	}
	for i := 0; i < 128 && g.progress >= g.goal; i++ {
		g.progress -= g.goal
		g.level++
		g.goal = levelGoal(g.level, g.baseline)
		if err := reward(ctx, tx, fmt.Sprintf("level:%d", g.level), 3); err != nil {
			return false, err
		}
		if title := levelTitles[g.level]; title != "" {
			if err := event(ctx, tx, fmt.Sprintf("level:%d", g.level), day, now, 0, title, "新的树种会在接下来的生长中出现，口袋里也多了 3 点成就。"); err != nil {
				return false, err
			}
		}
	}
	for i := 0; i < 128 && g.seed >= g.seedGoal; i++ {
		g.seed -= g.seedGoal
		// 只累计已完成种植的消耗，避免一次大请求与多次小请求得到不同数量。
		todayTokens += g.seedGoal
		g.seedGoal = max(250000, todayTokens/12, g.baseline/12)
		if err := plant(ctx, tx, "usage", day, now, g.level); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO growth_days VALUES(?,?) ON CONFLICT(day) DO UPDATE SET tokens=excluded.tokens`, day, todayTokens); err != nil {
		return false, err
	}
	if err := g.save(ctx, tx); err != nil {
		return false, err
	}
	if err := settleAchievements(ctx, tx, day, now); err != nil {
		return false, err
	}
	return len(credits) >= tokenusage.GrowthCreditBatchSize, errors.Join(tx.Commit(), baselineErr)
}

var levelTitles = map[int64]string{5: "风里多了松针的香气", 15: "白桦在海边舒展", 30: "花期悄悄到了"}

func levelGoal(level, baseline int64) int64 {
	if level < 5 {
		return 20000 + 180000*(level-1)*(level-1)
	}
	goal := int64(2000000) + 500000*(level-5)*(level-5)
	if level >= 20 && baseline > 0 {
		goal = max(goal, int64(float64(baseline)*(1.5+0.12*float64(level-20))))
	}
	return goal
}

func reward(ctx context.Context, tx *sql.Tx, key string, points int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO rewards VALUES(?,?) ON CONFLICT DO NOTHING`, key, points)
	return err
}

func event(ctx context.Context, tx *sql.Tx, key, day string, now time.Time, island int64, title, story string) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO forest_events VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, key, day, now.UnixMilli(), island, title, story)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	if island > 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO event_trees SELECT ?,id FROM trees WHERE island=? ORDER BY id DESC LIMIT 1`, key, island)
	}
	if err != nil {
		return err
	}
	return applyPatrolOutcome(ctx, tx, key, now)
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
	choices := make([]int64, styles)
	for i := range choices {
		choices[i] = int64(i)
	}
	for _, item := range catalog {
		if item.Category != "species" || item.Kind != kind {
			continue
		}
		var owned bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rewards WHERE key=?)`, "ornament:"+item.ID).Scan(&owned); err != nil {
			return err
		}
		if owned {
			choices = append(choices, int64(item.Style))
		}
	}
	style := choices[variant%int64(len(choices))]
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
	seed, dew := int64(0), int64(0)
	if id%5 == 0 {
		seed = 1
	}
	if kind == "focus" {
		seed++
	}
	if kind == "rest" {
		dew = 2
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO forest_resources VALUES(?,?,?,?)`, fmt.Sprintf("tree:%d", id), 1, seed, dew); err != nil {
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
			"树冠上挂着一个巨大的甜甜圈。巡护员数了三次，糖针总是少一根。",
		}
		if err := event(ctx, tx, fmt.Sprintf("rest-style:%d", style), day, now, island, "怪树图鉴有了新成员", stories[style]); err != nil {
			return err
		}
	case "focus":
		if err := event(ctx, tx, "focus-first", day, now, island, "银色枝条轻轻舒展", "认真投入的时光，在步道旁留下了一棵新树。"); err != nil {
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
		tales := [][2]string{
			{"苗圃来了一只大肥虫", "它把叶片啃成了月牙，还假装自己是一根树枝。巡护员记住了这棵树，准备请瓢虫来做客。"},
			{"卷叶里传来打嗝声", "一只毛毛虫把嫩叶卷成了睡袋。它说自己只是借住，脚边却堆着三片吃剩的叶梗。"},
			{"树叶举办了缺角展览", "小虫们沿着叶缘吃出了一排波浪。巡护员决定邀请几位红色圆点的客人来整理展场。"},
		}
		tale := tales[(island-1)%int64(len(tales))]
		return event(ctx, tx, fmt.Sprintf("aphids:%d", island), day, now, island, tale[0], tale[1])
	case 8:
		return followUp(ctx, tx, "aphids", "ladybug", island, day, now, "红点小队报到", "新树刚站稳脚跟，瓢虫小队就沿着枝条出发了。上次闹腾的苗圃安静下来，只剩一只大肥虫拖着行李往海边走。")
	case 16:
		return event(ctx, tx, fmt.Sprintf("squirrel:%d", island), day, now, island, "松鼠的藏宝图画反了", "它抱着松果绕了新树三圈，终于决定把这里当作新仓库。旧仓库在哪儿，它已经忘了。")
	case 12:
		return event(ctx, tx, fmt.Sprintf("bird:%d", island), day, now, island, "海鸟捎来一只种子口袋", "它把三颗花种放在树杈上，收走一根细枝当作邮费。明天的花圃有着落了。")
	case 25:
		return event(ctx, tx, fmt.Sprintf("mushrooms:%d", island), day, now, island, "雨后开了一家蘑菇伞铺", "树根旁冒出了三把小伞。蜗牛试了最小的一把，觉得正好可以遮住自己的触角。")
	case 40:
		return event(ctx, tx, fmt.Sprintf("storm:%d", island), day, now, island, "大雨冲断了林间步道", "积水卷走了一些枝木，也折弯了树枝。巡护员标出了需要照料的地方，准备带着踏石和工具回来。")
	case 44:
		return followUp(ctx, tx, "storm", "stones", island, day, now, "雨后的步道铺上了踏石", "巡护员铺好了踏石，也照料了上次被大雨折弯的树枝。岸边还捡回了两根可以用的枝木。")
	case 64:
		return event(ctx, tx, fmt.Sprintf("fireflies:%d", island), day, now, island, "萤火虫把星星挂低了一点", "天还没完全黑，新树周围就亮起了小灯。海鸥绕着飞了一圈，坚称这是一座迷你灯塔。")
	case 80:
		return event(ctx, tx, fmt.Sprintf("owl:%d", island), day, now, island, "夜班管理员上任", "一只猫头鹰坐上树梢，认真清点每一片叶子。数到第十七片就睡着了，胸前仍挂着正在巡护的小牌子。")
	}
	return nil
}

func followUp(ctx context.Context, tx *sql.Tx, previous, next string, island int64, day string, now time.Time, title, story string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM forest_events WHERE key=?)`, fmt.Sprintf("%s:%d", previous, island)).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return event(ctx, tx, fmt.Sprintf("%s:%d", next, island), day, now, island, title, story)
}

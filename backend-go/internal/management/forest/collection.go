package forest

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Resources struct {
	Wood  int64 `json:"wood"`
	Seeds int64 `json:"seeds"`
	Dew   int64 `json:"dew"`
}

func readResources(ctx context.Context, db growthReader) (Resources, error) {
	var r Resources
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(wood),0),COALESCE(SUM(seeds),0),COALESCE(SUM(dew),0) FROM forest_resources`).Scan(&r.Wood, &r.Seeds, &r.Dew)
	return r, err
}

type Achievement struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Rarity      string `json:"rarity"`
	Target      int64  `json:"target"`
	Progress    int64  `json:"progress"`
	Points      int64  `json:"points"`
	UnlockedAt  int64  `json:"unlockedAt"`
	TreeID      int64  `json:"treeId"`
	Kind        string `json:"-"`
}

var achievements = []Achievement{
	{ID: "first", Name: "一粒种子的远行", Description: "种下第一棵树", Rarity: "初见", Target: 1, Points: 5},
	{ID: "grove", Name: "风有了形状", Description: "让 12 棵树在海风里相遇", Rarity: "精巧", Target: 12, Points: 15},
	{ID: "island", Name: "一整座绿意", Description: "种满第一座岛", Rarity: "珍藏", Target: IslandCapacity, Points: 30},
	{ID: "archipelago", Name: "星海领航员", Description: "让八座岛连成一片群岛", Rarity: "传说", Target: IslandCapacity * GroupCapacity, Points: 120},
	{ID: "focus", Name: "心流的回声", Description: "让一段专注的时光在岛上生根", Rarity: "珍藏", Target: 1, Points: 10, Kind: "focus"},
	{ID: "rest", Name: "今天也要好好发呆", Description: "休息过后，迎来第一位怪树朋友", Rarity: "珍藏", Target: 1, Points: 10, Kind: "rest"},
	{ID: "rest-grove", Name: "闲着也是一种本事", Description: "与 12 位怪树朋友一起晒太阳", Rarity: "稀有", Target: 12, Points: 45, Kind: "rest"},
}

func readAchievements(ctx context.Context, tx *sql.Tx) ([]Achievement, error) {
	counts := map[string]int64{}
	rows, err := tx.QueryContext(ctx, `SELECT kind,COUNT(*) FROM trees GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind string
		var n int64
		if err := rows.Scan(&kind, &n); err != nil {
			rows.Close()
			return nil, err
		}
		counts[kind] = n
		counts[""] += n
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	items := append([]Achievement{}, achievements...)
	for i := range items {
		a := &items[i]
		a.Progress = min(a.Target, counts[a.Kind])
		err := tx.QueryRowContext(ctx, `SELECT at_ms,COALESCE((SELECT tree_id FROM event_trees WHERE event_key=forest_events.key),0) FROM forest_events WHERE key=?`, "achievement:"+a.ID).Scan(&a.UnlockedAt, &a.TreeID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return items, nil
}

func settleAchievements(ctx context.Context, tx *sql.Tx, day string, now time.Time) error {
	items, err := readAchievements(ctx, tx)
	if err != nil {
		return err
	}
	for _, a := range items {
		if a.UnlockedAt > 0 || a.Progress < a.Target {
			continue
		}
		var treeID, islandID int64
		// 保存跨过里程碑的那棵树；同批种下多棵时仍能准确追溯。
		if err := tx.QueryRowContext(ctx, `SELECT id,island FROM trees WHERE (?='' OR kind=?) ORDER BY id LIMIT 1 OFFSET ?`, a.Kind, a.Kind, a.Target-1).Scan(&treeID, &islandID); err != nil {
			return err
		}
		key := "achievement:" + a.ID
		if err := reward(ctx, tx, key, a.Points); err != nil {
			return err
		}
		if err := event(ctx, tx, key, day, now, islandID, "成就解锁 · "+a.Name, a.Description); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO event_trees VALUES(?,?) ON CONFLICT(event_key) DO UPDATE SET tree_id=excluded.tree_id`, key, treeID); err != nil {
			return err
		}
	}
	return nil
}

const eventColumns = `rowid,key,island,COALESCE((SELECT tree_id FROM event_trees WHERE event_key=forest_events.key),0),at_ms,title,story,
COALESCE((SELECT points FROM rewards WHERE rewards.key=forest_events.key),0),
COALESCE((SELECT wood FROM forest_resources WHERE forest_resources.key=forest_events.key),0),
COALESCE((SELECT seeds FROM forest_resources WHERE forest_resources.key=forest_events.key),0),
COALESCE((SELECT dew FROM forest_resources WHERE forest_resources.key=forest_events.key),0),
COALESCE((SELECT damaged FROM event_outcomes WHERE event_key=forest_events.key),0),
COALESCE((SELECT destroyed FROM event_outcomes WHERE event_key=forest_events.key),0),
COALESCE((SELECT restored FROM event_outcomes WHERE event_key=forest_events.key),0),
COALESCE((SELECT away_days FROM event_outcomes WHERE event_key=forest_events.key),0),
COALESCE((SELECT risk_percent FROM event_outcomes WHERE event_key=forest_events.key),0)`

func scanEvent(row scanner, e *Event) error {
	return row.Scan(&e.Sequence, &e.ID, &e.Island, &e.TreeID, &e.At, &e.Title, &e.Story,
		&e.Effects.Points, &e.Effects.Wood, &e.Effects.Seeds, &e.Effects.Dew,
		&e.Effects.Damaged, &e.Effects.Destroyed, &e.Effects.Restored, &e.Effects.AwayDays, &e.Effects.RiskPercent)
}

type EventFeed struct {
	Instance string  `json:"instance"`
	Events   []Event `json:"events"`
	Cursor   int64   `json:"cursor"`
	More     bool    `json:"more"`
}

func (s *Store) Events(ctx context.Context, after int64) (EventFeed, error) {
	result := EventFeed{Events: []Event{}, Cursor: after}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT value FROM forest_identity WHERE id=1`).Scan(&result.Instance); err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+eventColumns+` FROM forest_events WHERE rowid>? ORDER BY rowid LIMIT 65`, after)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(result.Events) == 64 {
			result.More = true
			break
		}
		var e Event
		if err := scanEvent(rows, &e); err != nil {
			return result, err
		}
		result.Events = append(result.Events, e)
		result.Cursor = e.Sequence
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func (s *Store) Event(ctx context.Context, id string) (Event, error) {
	var e Event
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return e, err
	}
	defer tx.Rollback()
	err = scanEvent(tx.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM forest_events WHERE key=?`, id), &e)
	if errors.Is(err, sql.ErrNoRows) {
		return e, ErrInvalid
	}
	if err != nil {
		return e, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT tree_id,before_state,after_state FROM event_affected_trees WHERE event_key=? ORDER BY tree_id`, id)
	if err != nil {
		return e, err
	}
	defer rows.Close()
	for rows.Next() {
		var tree AffectedTree
		if err := rows.Scan(&tree.TreeID, &tree.Before, &tree.After); err != nil {
			return e, err
		}
		e.Affected = append(e.Affected, tree)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return e, err
	}
	return e, tx.Commit()
}

func (s *Store) Tree(ctx context.Context, id int64) (Tree, error) {
	t, err := scanTree(s.db.QueryRowContext(ctx, `SELECT `+treeColumns+` FROM trees WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrInvalid
	}
	return t, err
}

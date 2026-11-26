package forest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type Island struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Favorite  bool   `json:"favorite"`
	Count     int64  `json:"count"`
	Damaged   int64  `json:"damaged"`
	Destroyed int64  `json:"destroyed"`
}

type Group struct {
	ID        int64 `json:"id"`
	Islands   int64 `json:"islands"`
	Trees     int64 `json:"trees"`
	Damaged   int64 `json:"damaged"`
	Destroyed int64 `json:"destroyed"`
}

type Tree struct {
	ID                int64  `json:"id"`
	Kind              string `json:"kind"`
	Style             int    `json:"style"`
	Name              string `json:"name"`
	Island            int64  `json:"island"`
	Slot              int    `json:"slot"`
	Variant           int64  `json:"variant"`
	Planted           int64  `json:"planted"`
	AssetVersion      int    `json:"assetVersion"`
	Accessory         string `json:"accessory"`
	Condition         string `json:"condition"`
	ConditionRevision int64  `json:"conditionRevision"`
}

type Discovery struct {
	Kind         string `json:"kind"`
	Style        int    `json:"style"`
	FirstPlanted int64  `json:"firstPlanted"`
}

type Event struct {
	Sequence int64          `json:"sequence"`
	ID       string         `json:"id"`
	Island   int64          `json:"island"`
	TreeID   int64          `json:"treeId"`
	At       int64          `json:"at"`
	Title    string         `json:"title"`
	Story    string         `json:"story"`
	Effects  EventEffects   `json:"effects"`
	Affected []AffectedTree `json:"affected,omitempty"`
}

type Ornament struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Price       int64     `json:"price"`
	Level       int64     `json:"level"`
	Owned       bool      `json:"owned"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
	Rarity      string    `json:"rarity"`
	Cost        Resources `json:"cost"`
	Kind        string    `json:"kind,omitempty"`
	Style       int       `json:"style"`
}

var catalog = []Ornament{
	{ID: "trail", Name: "林间踏石", Price: 15, Level: 3, Category: "landscape", Description: "把一串月白色石头铺向海边。", Rarity: "精巧", Cost: Resources{Wood: 8}},
	{ID: "lantern", Name: "海湾灯塔", Price: 80, Level: 8, Category: "landscape", Description: "每片群岛的第一座岛，都有一盏等你回来的灯。", Rarity: "珍藏", Cost: Resources{Wood: 25, Seeds: 3}},
	{ID: "monument", Name: "星海纪念碑", Price: 240, Level: 15, Category: "landscape", Description: "在每片群岛的起点，为走过的旅程留一枚星光。", Rarity: "传说", Cost: Resources{Wood: 60, Dew: 6}},
	{ID: "maple", Name: "枫糖树", Price: 45, Level: 5, Category: "species", Description: "像一簇温暖的小火焰，秋天也舍不得熄灭。", Rarity: "珍藏", Cost: Resources{Seeds: 3}, Kind: "usage", Style: 4},
	{ID: "willow", Name: "星垂柳", Price: 90, Level: 8, Category: "species", Description: "枝梢垂下细碎星光，风一吹就亮起来。", Rarity: "稀有", Cost: Resources{Seeds: 5, Dew: 2}, Kind: "usage", Style: 5},
	{ID: "moon", Name: "月灯树", Price: 60, Level: 6, Category: "species", Description: "把安静的时光，结成一颗小小的月亮。", Rarity: "稀有", Cost: Resources{Seeds: 3, Dew: 2}, Kind: "focus", Style: 4},
	{ID: "donut", Name: "甜甜圈树", Price: 50, Level: 4, Category: "species", Description: "树冠中间少了一口。它坚持说是海鸥干的。", Rarity: "珍藏", Cost: Resources{Seeds: 2, Dew: 4}, Kind: "rest", Style: 4},
	{ID: "ribbon", Name: "风的蝴蝶结", Price: 20, Level: 2, Category: "accessory", Description: "给喜欢的树系一枚珊瑚色蝴蝶结。", Rarity: "精巧", Cost: Resources{Wood: 4}},
	{ID: "fireflies", Name: "萤火星环", Price: 70, Level: 7, Category: "accessory", Description: "一圈不会熄灭的小星星，围着树冠打转。", Rarity: "稀有", Cost: Resources{Seeds: 2, Dew: 3}},
}

type World struct {
	Tokens        int64            `json:"tokens"`
	Seed          int64            `json:"seed"`
	SeedGoal      int64            `json:"seedGoal"`
	Focus         int64            `json:"focus"`
	Rest          int64            `json:"rest"`
	Level         int64            `json:"level"`
	LevelProgress int64            `json:"levelProgress"`
	LevelGoal     int64            `json:"levelGoal"`
	Baseline      int64            `json:"baseline"`
	Points        int64            `json:"points"`
	Balance       int64            `json:"balance"`
	Islands       int64            `json:"islands"`
	Counts        map[string]int64 `json:"counts"`
	Discoveries   []Discovery      `json:"discoveries"`
	Latest        *Tree            `json:"latest"`
	Timer         Timer            `json:"timer"`
	Ornaments     []Ornament       `json:"ornaments"`
	Favorites     []Island         `json:"favorites"`
	Milestones    []Event          `json:"milestones"`
	CatchingUp    bool             `json:"catchingUp"`
	SyncIssue     bool             `json:"syncIssue"`
	Resources     Resources        `json:"resources"`
	Achievements  []Achievement    `json:"achievements"`
	Ecology       Ecology          `json:"ecology"`
}

const treeColumns = `id,kind,style,name,island,slot,variant,planted,asset_version,COALESCE((SELECT item FROM tree_accessories WHERE tree_id=trees.id),''),` + conditionSchemaColumn + `,COALESCE((SELECT revision FROM tree_condition WHERE tree_id=trees.id),0)`

type scanner interface{ Scan(...any) error }

func scanTree(row scanner) (Tree, error) {
	var t Tree
	err := row.Scan(&t.ID, &t.Kind, &t.Style, &t.Name, &t.Island, &t.Slot, &t.Variant, &t.Planted, &t.AssetVersion, &t.Accessory, &t.Condition, &t.ConditionRevision)
	return t, err
}

func (s *Store) World(ctx context.Context, now time.Time) (World, error) {
	w := World{Counts: map[string]int64{"usage": 0, "focus": 0, "rest": 0}, Ornaments: append([]Ornament{}, catalog...), Favorites: []Island{}, SyncIssue: s.syncIssue.Load()}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return w, err
	}
	defer tx.Rollback()
	g, err := readGrowth(ctx, tx)
	if err != nil {
		return w, err
	}
	w.Tokens, w.Seed, w.SeedGoal, w.Focus, w.Rest = g.tokens, g.seed, g.seedGoal, g.focus, g.rest
	w.CatchingUp = s.syncPending.Load() || g.seed >= g.seedGoal || g.progress >= g.goal
	w.Level, w.LevelProgress, w.LevelGoal, w.Baseline = g.level, g.progress, g.goal, g.baseline
	w.Timer, err = readTimer(ctx, tx, now)
	if err != nil {
		return w, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(max(points,0)),0),COALESCE(SUM(points),0) FROM rewards`).Scan(&w.Points, &w.Balance); err != nil {
		return w, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM islands`).Scan(&w.Islands); err != nil {
		return w, err
	}
	tree, err := scanTree(tx.QueryRowContext(ctx, `SELECT `+treeColumns+` FROM trees ORDER BY id DESC LIMIT 1`))
	if err == nil {
		w.Latest = &tree
	} else if !errors.Is(err, sql.ErrNoRows) {
		return w, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind,COUNT(*) FROM trees WHERE `+conditionSchemaColumn+`!='destroyed' GROUP BY kind`)
	if err != nil {
		return w, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var count int64
		if err := rows.Scan(&kind, &count); err != nil {
			return w, err
		}
		w.Counts[kind] = count
	}
	if err := rows.Err(); err != nil {
		return w, err
	}
	w.Discoveries, err = queryDiscoveries(ctx, tx)
	if err != nil {
		return w, err
	}
	for i := range w.Ornaments {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rewards WHERE key=?)`, "ornament:"+w.Ornaments[i].ID).Scan(&w.Ornaments[i].Owned); err != nil {
			return w, err
		}
	}
	w.Favorites, err = queryIslands(ctx, tx, `WHERE islands.favorite=1`)
	if err != nil {
		return w, err
	}
	w.Milestones, err = queryEvents(ctx, tx, `WHERE key LIKE 'full:%' OR key LIKE 'group:%' OR key LIKE 'level:%' OR key LIKE 'ornament:%'`)
	if err != nil {
		return w, err
	}
	w.Resources, err = readResources(ctx, tx)
	if err != nil {
		return w, err
	}
	w.Achievements, err = readAchievements(ctx, tx)
	if err != nil {
		return w, err
	}
	w.Ecology, err = readEcology(ctx, tx)
	if err != nil {
		return w, err
	}
	return w, tx.Commit()
}

type listReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryDiscoveries(ctx context.Context, db listReader) ([]Discovery, error) {
	rows, err := db.QueryContext(ctx, `SELECT kind,style,first_planted FROM discoveries ORDER BY kind,style`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Discovery{}
	for rows.Next() {
		var item Discovery
		if err := rows.Scan(&item.Kind, &item.Style, &item.FirstPlanted); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func queryIslands(ctx context.Context, db listReader, condition string, args ...any) ([]Island, error) {
	rows, err := db.QueryContext(ctx, `SELECT islands.id,islands.name,islands.favorite,
COUNT(trees.id)-COALESCE(SUM(tree_condition.state='destroyed'),0),
COALESCE(SUM(tree_condition.state='damaged'),0),COALESCE(SUM(tree_condition.state='destroyed'),0)
FROM islands LEFT JOIN trees ON trees.island=islands.id LEFT JOIN tree_condition ON tree_condition.tree_id=trees.id `+condition+` GROUP BY islands.id ORDER BY islands.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Island{}
	for rows.Next() {
		var i Island
		if err := rows.Scan(&i.ID, &i.Name, &i.Favorite, &i.Count, &i.Damaged, &i.Destroyed); err != nil {
			return nil, err
		}
		result = append(result, i)
	}
	return result, rows.Err()
}

func (s *Store) Groups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT (islands.id-1)/? AS area,COUNT(DISTINCT islands.id),
COUNT(trees.id)-COALESCE(SUM(tree_condition.state='destroyed'),0),COALESCE(SUM(tree_condition.state='damaged'),0),COALESCE(SUM(tree_condition.state='destroyed'),0)
FROM islands LEFT JOIN trees ON trees.island=islands.id LEFT JOIN tree_condition ON tree_condition.tree_id=trees.id GROUP BY area ORDER BY area`, GroupCapacity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Group{}
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Islands, &g.Trees, &g.Damaged, &g.Destroyed); err != nil {
			return nil, err
		}
		result = append(result, g)
	}
	return result, rows.Err()
}

func (s *Store) Islands(ctx context.Context, group int64) ([]Island, error) {
	if group < 0 || group > (1<<62)/GroupCapacity {
		return nil, ErrInvalid
	}
	return queryIslands(ctx, s.db, `WHERE islands.id > ? AND islands.id <= ?`, group*GroupCapacity, (group+1)*GroupCapacity)
}

type IslandView struct {
	Island Island  `json:"island"`
	Trees  []Tree  `json:"trees"`
	Events []Event `json:"events"`
}

func (s *Store) Island(ctx context.Context, id int64) (IslandView, error) {
	v := IslandView{Island: Island{ID: id}, Trees: []Tree{}, Events: []Event{}}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	items, err := queryIslands(ctx, tx, `WHERE islands.id=?`, id)
	if err != nil {
		return v, err
	}
	if len(items) == 0 && id != 1 {
		return v, ErrInvalid
	}
	if len(items) != 0 {
		v.Island = items[0]
	}
	v.Trees, err = queryTrees(ctx, tx, `WHERE island=?`, id)
	if err != nil {
		return v, err
	}
	v.Events, err = queryEvents(ctx, tx, `WHERE island=?`, id)
	if err != nil {
		return v, err
	}
	return v, tx.Commit()
}

func queryTrees(ctx context.Context, db listReader, where string, args ...any) ([]Tree, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+treeColumns+` FROM trees `+where+` ORDER BY id LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	trees := []Tree{}
	for rows.Next() {
		t, err := scanTree(rows)
		if err != nil {
			return nil, err
		}
		trees = append(trees, t)
	}
	return trees, rows.Err()
}

func queryEvents(ctx context.Context, db listReader, where string, args ...any) ([]Event, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+eventColumns+` FROM forest_events `+where+` ORDER BY rowid DESC LIMIT 20`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var e Event
		if err := scanEvent(rows, &e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

type DayView struct {
	Trees  []Tree           `json:"trees"`
	Counts map[string]int64 `json:"counts"`
	Events []Event          `json:"events"`
}

func (s *Store) Day(ctx context.Context, day string, after int64) (DayView, error) {
	result := DayView{Counts: map[string]int64{"usage": 0, "focus": 0, "rest": 0}}
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return result, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	result.Trees, err = queryTrees(ctx, tx, `WHERE day=? AND id>?`, day, after)
	if err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind,COUNT(*) FROM trees WHERE day=? GROUP BY kind`, day)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var count int64
		if err := rows.Scan(&kind, &count); err != nil {
			return result, err
		}
		result.Counts[kind] = count
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	result.Events, err = queryEvents(ctx, tx, `WHERE day=?`, day)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

type TreeEdit struct {
	Name      *string `json:"name"`
	Style     *int    `json:"style"`
	Slot      *int    `json:"slot"`
	Accessory *string `json:"accessory"`
}

func (s *Store) EditTree(ctx context.Context, id int64, edit TreeEdit) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := scanTree(tx.QueryRowContext(ctx, `SELECT `+treeColumns+` FROM trees WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	if edit.Name != nil {
		t.Name = strings.TrimSpace(*edit.Name)
		if utf8.RuneCountInString(t.Name) > 30 {
			return ErrInvalid
		}
	}
	if edit.Style != nil {
		if *edit.Style < 0 || *edit.Style > 5 || (t.Kind != "usage" && *edit.Style > 4) {
			return ErrInvalid
		}
		if *edit.Style >= 4 {
			var itemID string
			for _, item := range catalog {
				if item.Category == "species" && item.Kind == t.Kind && item.Style == *edit.Style {
					itemID = item.ID
				}
			}
			var owned bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rewards WHERE key=?)`, "ornament:"+itemID).Scan(&owned); err != nil {
				return err
			}
			if itemID == "" || !owned {
				return ErrInvalid
			}
		} else if t.Kind == "usage" {
			g, err := readGrowth(ctx, tx)
			if err != nil {
				return err
			}
			if g.level < []int64{1, 5, 15, 30}[*edit.Style] {
				return ErrInvalid
			}
		}
		t.Style = *edit.Style
	}
	if edit.Accessory != nil {
		if *edit.Accessory != "" {
			var valid, owned bool
			for _, item := range catalog {
				if item.ID == *edit.Accessory && item.Category == "accessory" {
					valid = true
				}
			}
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rewards WHERE key=?)`, "ornament:"+*edit.Accessory).Scan(&owned); err != nil {
				return err
			}
			if !valid || !owned {
				return ErrInvalid
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tree_accessories VALUES(?,?) ON CONFLICT(tree_id) DO UPDATE SET item=excluded.item`, id, *edit.Accessory); err != nil {
			return err
		}
	}
	if edit.Slot != nil {
		if *edit.Slot < 0 || *edit.Slot >= IslandCapacity {
			return ErrInvalid
		}
		var occupied bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trees WHERE island=? AND slot=? AND id!=?)`, t.Island, *edit.Slot, id).Scan(&occupied); err != nil {
			return err
		}
		if occupied {
			return ErrConflict
		}
		t.Slot = *edit.Slot
	}
	if _, err := tx.ExecContext(ctx, `UPDATE trees SET name=?,style=?,slot=? WHERE id=?`, t.Name, t.Style, t.Slot, id); err != nil {
		return err
	}
	if edit.Style != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO discoveries VALUES(?,?,?) ON CONFLICT DO NOTHING`, t.Kind, t.Style, time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) EditIsland(ctx context.Context, id int64, name string, favorite bool) error {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 30 {
		return ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE islands SET name=?,favorite=? WHERE id=?`, name, favorite, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrInvalid
	}
	return err
}

func (s *Store) Redeem(ctx context.Context, id string, now time.Time) error {
	var item Ornament
	for _, candidate := range catalog {
		if candidate.ID == id {
			item = candidate
			break
		}
	}
	if item.ID == "" {
		return ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owned bool
	var balance, level int64
	key := "ornament:" + id
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rewards WHERE key=?),COALESCE(SUM(points),0),(SELECT level FROM growth WHERE id=1) FROM rewards`, key).
		Scan(&owned, &balance, &level); err != nil {
		return err
	}
	if owned {
		return tx.Commit()
	}
	if level < item.Level || balance < item.Price {
		return fmt.Errorf("%w：等级或成就点不足", ErrInvalid)
	}
	resources, err := readResources(ctx, tx)
	if err != nil {
		return err
	}
	if resources.Wood < item.Cost.Wood || resources.Seeds < item.Cost.Seeds || resources.Dew < item.Cost.Dew {
		return fmt.Errorf("%w：材料不足", ErrInvalid)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO forest_resources VALUES(?,?,?,?)`, key, -item.Cost.Wood, -item.Cost.Seeds, -item.Cost.Dew); err != nil {
		return err
	}
	if err := reward(ctx, tx, key, -item.Price); err != nil {
		return err
	}
	if err := event(ctx, tx, key, s.usage.GrowthDate(now), now, 0, "兑换了"+item.Name, fmt.Sprintf("使用了 %d 点成就余额。", item.Price)); err != nil {
		return err
	}
	return tx.Commit()
}

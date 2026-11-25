package forest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
)

const conditionSchemaColumn = `COALESCE((SELECT state FROM tree_condition WHERE tree_id=trees.id),'healthy')`

type EventEffects struct {
	Resources
	Points      int64 `json:"points"`
	Damaged     int64 `json:"damaged"`
	Destroyed   int64 `json:"destroyed"`
	Restored    int64 `json:"restored"`
	AwayDays    int   `json:"awayDays"`
	RiskPercent int   `json:"riskPercent"`
}

type AffectedTree struct {
	TreeID int64  `json:"treeId"`
	Before string `json:"before"`
	After  string `json:"after"`
}

type Ecology struct {
	Damaged     int64     `json:"damaged"`
	Destroyed   int64     `json:"destroyed"`
	RepairCost  Resources `json:"repairCost"`
	ReplantCost Resources `json:"replantCost"`
}

var repairCost = Resources{Wood: 2, Dew: 1}
var replantCost = Resources{Wood: 3, Seeds: 1, Dew: 1}

func readEcology(ctx context.Context, tx *sql.Tx) (Ecology, error) {
	result := Ecology{RepairCost: repairCost, ReplantCost: replantCost}
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(state='damaged'),0),COALESCE(SUM(state='destroyed'),0) FROM tree_condition`).Scan(&result.Damaged, &result.Destroyed)
	return result, err
}

// 同一事件的随机序列由世界和事件键固定，事务重试、重启和刷新不会重新抽取结果。
func eventRandom(ctx context.Context, tx *sql.Tx, key string) (*rand.Rand, error) {
	var identity string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM forest_identity WHERE id=1`).Scan(&identity); err != nil {
		return nil, err
	}
	seed := sha256.Sum256([]byte(identity + ":" + key))
	return rand.New(rand.NewPCG(binary.LittleEndian.Uint64(seed[:8]), binary.LittleEndian.Uint64(seed[8:16]))), nil
}

func changeCondition(ctx context.Context, tx *sql.Tx, key string, treeID int64, before, after string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO tree_condition(tree_id,state,revision,changed_at,cause_key) VALUES(?,?,1,?,?)
ON CONFLICT(tree_id) DO UPDATE SET state=excluded.state,revision=revision+1,changed_at=excluded.changed_at,cause_key=excluded.cause_key`, treeID, after, now.UnixMilli(), key); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO event_affected_trees VALUES(?,?,?,?)`, key, treeID, before, after)
	return err
}

func affectTrees(ctx context.Context, tx *sql.Tx, key, before, after string, limit int, island, cutoff int64, cause string, now time.Time) (int64, error) {
	if limit == 0 {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM trees WHERE `+conditionSchemaColumn+`=?
AND (?=0 OR island=?) AND planted<=? AND (?='' OR EXISTS(SELECT 1 FROM tree_condition WHERE tree_id=trees.id AND cause_key=?)) ORDER BY id`, before, island, island, cutoff, cause, cause)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, errors.Join(err, rows.Close())
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, err
	}
	rng, err := eventRandom(ctx, tx, key+":"+after)
	if err != nil {
		return 0, err
	}
	rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	ids = ids[:min(limit, len(ids))]
	for _, id := range ids {
		if err := changeCondition(ctx, tx, key, id, before, after, now); err != nil {
			return 0, err
		}
	}
	if len(ids) > 0 {
		// 详情优先定位实际受影响的树，事件仍保留所有受影响对象。
		if _, err := tx.ExecContext(ctx, `INSERT INTO event_trees VALUES(?,?) ON CONFLICT(event_key) DO UPDATE SET tree_id=excluded.tree_id`, key, ids[0]); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE forest_events SET island=(SELECT island FROM trees WHERE id=?) WHERE key=?`, ids[0], key); err != nil {
			return 0, err
		}
	}
	return int64(len(ids)), nil
}

func eventResources(ctx context.Context, tx *sql.Tx, key string, delta Resources) error {
	available, err := readResources(ctx, tx)
	if err != nil {
		return err
	}
	// 灾害最多损失当前库存，记录实际损失，不制造负库存。
	delta.Wood = max(delta.Wood, -available.Wood)
	delta.Seeds = max(delta.Seeds, -available.Seeds)
	delta.Dew = max(delta.Dew, -available.Dew)
	_, err = tx.ExecContext(ctx, `INSERT INTO forest_resources VALUES(?,?,?,?)`, key, delta.Wood, delta.Seeds, delta.Dew)
	return err
}

func applyPatrolOutcome(ctx context.Context, tx *sql.Tx, key string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO event_outcomes(event_key) VALUES(?)`, key); err != nil {
		return err
	}
	kind, _, _ := strings.Cut(key, ":")
	var island int64
	if err := tx.QueryRowContext(ctx, `SELECT island FROM forest_events WHERE key=?`, key).Scan(&island); err != nil {
		return err
	}
	var delta Resources
	var damaged, restored int64
	var err error
	switch kind {
	case "aphids":
		damaged, err = affectTrees(ctx, tx, key, "healthy", "damaged", 1, island, now.UnixMilli(), "", now)
	case "ladybug":
		restored, err = affectTrees(ctx, tx, key, "damaged", "healthy", IslandCapacity, island, now.UnixMilli(), fmt.Sprintf("aphids:%d", island), now)
		delta.Dew = 1
	case "bird":
		delta = Resources{Wood: -1, Seeds: 3}
	case "squirrel":
		delta.Seeds = 2
	case "mushrooms":
		delta.Dew = 2
	case "storm":
		damaged, err = affectTrees(ctx, tx, key, "healthy", "damaged", 2, island, now.UnixMilli(), "", now)
		delta.Wood = -3
	case "stones":
		restored, err = affectTrees(ctx, tx, key, "damaged", "healthy", IslandCapacity, island, now.UnixMilli(), fmt.Sprintf("storm:%d", island), now)
		delta.Wood = 2
	case "fireflies":
		delta.Dew = 3
	case "owl":
		if err := reward(ctx, tx, key, 3); err != nil {
			return err
		}
	default:
		return nil
	}
	if err != nil {
		return err
	}
	if delta != (Resources{}) {
		if err := eventResources(ctx, tx, key, delta); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE event_outcomes SET damaged=?,restored=? WHERE event_key=?`, damaged, restored, key)
	return err
}

func absenceRisk(elapsed time.Duration) int {
	return min(75, max(0, int(elapsed/(24*time.Hour))-3)*5)
}

func settleAbsence(ctx context.Context, tx *sql.Tx, lastUsage, checkedUsage int64, returnAt, now time.Time, day string) error {
	if lastUsage == 0 || checkedUsage >= lastUsage {
		return nil
	}
	elapsed := returnAt.Sub(time.UnixMilli(lastUsage))
	risk := absenceRisk(elapsed)
	if risk == 0 {
		return nil
	}
	// 未触发灾害也结算本次离开；同一段闲置只抽取一次。
	if _, err := tx.ExecContext(ctx, `UPDATE forest_activity SET checked_usage=? WHERE id=1`, lastUsage); err != nil {
		return err
	}
	key := fmt.Sprintf("absence:%d", lastUsage)
	rng, err := eventRandom(ctx, tx, key)
	if err != nil {
		return err
	}
	if rng.IntN(100) >= risk {
		return nil
	}
	var healthy int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM trees WHERE `+conditionSchemaColumn+`='healthy' AND planted<=?`, returnAt.UnixMilli()).Scan(&healthy); err != nil {
		return err
	}
	if healthy == 0 {
		return nil
	}
	days := int(elapsed / (24 * time.Hour))
	title, story := "虫群趁着安静搬了进来", "久未打理的树冠成了虫群的餐桌，几根枝条需要重新照料。"
	count := min(48, max(1, (healthy*min(20, 4+days/2)+99)/100))
	to := "damaged"
	delta := Resources{Seeds: -int64(min(3, days/7+1))}
	if days >= 14 && healthy >= 10 && rng.IntN(100) < min(50, 20+days-14) {
		title, story = "海啸越过了海岸", "沉寂的海湾遭遇了一次海啸，部分树木只剩残桩。带上种子和材料，可以让这些位置重新长出绿意。"
		to = "destroyed"
		count = min(24, max(1, (healthy*min(10, 3+days/7)+99)/100))
		delta = Resources{Wood: -int64(min(8, days/4)), Seeds: -2, Dew: -2}
	} else if days >= 7 && rng.IntN(100) < 50 {
		title, story = "海风折弯了枝条", "一场无人照看的风暴穿过林地，树冠留下了折痕，岸边也冲走了一些材料。"
		delta = Resources{Wood: -int64(min(6, days/3)), Dew: -1}
	}
	if err := event(ctx, tx, key, day, now, 0, title, story); err != nil {
		return err
	}
	affected, err := affectTrees(ctx, tx, key, "healthy", to, count, 0, returnAt.UnixMilli(), "", now)
	if err != nil {
		return err
	}
	var damaged, destroyed int64
	if to == "destroyed" {
		destroyed = affected
	} else {
		damaged = affected
	}
	if err := eventResources(ctx, tx, key, delta); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE event_outcomes SET damaged=?,destroyed=?,away_days=?,risk_percent=? WHERE event_key=?`, damaged, destroyed, days, risk, key)
	return err
}

func (s *Store) recordActivity(ctx context.Context, tx *sql.Tx, credits []tokenusage.Credit, cursor int64, now time.Time) error {
	var times []int64
	for _, credit := range credits {
		if credit.Cursor <= cursor || credit.Tokens <= 0 {
			continue
		}
		if credit.At <= 0 {
			return fmt.Errorf("用量 %d 缺少实际发生时间", credit.Cursor)
		}
		times = append(times, credit.At)
	}
	if len(times) == 0 {
		return nil
	}
	var last, checked int64
	if err := tx.QueryRowContext(ctx, `SELECT last_usage,checked_usage FROM forest_activity WHERE id=1`).Scan(&last, &checked); err != nil {
		return err
	}
	// 一个批次可能积压多天或含迟到事实，只检查按实际时间排列后的相邻间隔。
	slices.Sort(times)
	for _, at := range times {
		if at <= last {
			continue
		}
		if last > checked && absenceRisk(time.Duration(at-last)*time.Millisecond) > 0 {
			// 分页边界也可能乱序；确认整个账本中没有被漏过的活跃时间。
			previous, err := s.usage.PreviousGrowthUsage(ctx, at)
			if err != nil {
				return err
			}
			last = max(last, previous)
		}
		if err := settleAbsence(ctx, tx, last, checked, time.UnixMilli(at), now, s.usage.GrowthDate(now)); err != nil {
			return err
		}
		last = at
	}
	_, err := tx.ExecContext(ctx, `UPDATE forest_activity SET last_usage=? WHERE id=1`, last)
	return err
}

func (s *Store) Visit(ctx context.Context, now time.Time) error {
	day := s.usage.GrowthDate(now)
	if day == "" {
		return nil
	}
	latest, err := s.usage.LatestGrowthUsage(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var last, checked int64
	if err := tx.QueryRowContext(ctx, `SELECT last_usage,checked_usage FROM forest_activity WHERE id=1`).Scan(&last, &checked); err != nil {
		return err
	}
	// 回访只判断最新真实用量之后的闲置；消费进度仍由增量事实推进。
	if err := settleAbsence(ctx, tx, max(last, latest), checked, now, now, day); err != nil {
		return err
	}
	return tx.Commit()
}

type RecoveryCommand struct {
	Revision int64 `json:"revision"`
}

func (s *Store) RecoverTree(ctx context.Context, id int64, cmd RecoveryCommand, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tree, err := scanTree(tx.QueryRowContext(ctx, `SELECT `+treeColumns+` FROM trees WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	key := fmt.Sprintf("recovery:%d:%d", id, cmd.Revision)
	var done bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM forest_events WHERE key=?)`, key).Scan(&done); err != nil {
		return err
	}
	if done {
		return tx.Commit()
	}
	if tree.ConditionRevision != cmd.Revision {
		return ErrConflict
	}
	cost, title := repairCost, "枝条重新舒展开了"
	switch tree.Condition {
	case "destroyed":
		cost, title = replantCost, "残桩旁重新长出了绿意"
	case "damaged":
	default:
		return fmt.Errorf("%w：这棵树现在不需要照料", ErrInvalid)
	}
	resources, err := readResources(ctx, tx)
	if err != nil {
		return err
	}
	if resources.Wood < cost.Wood || resources.Seeds < cost.Seeds || resources.Dew < cost.Dew {
		return fmt.Errorf("%w：照料这棵树所需的材料不足", ErrInvalid)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO forest_resources VALUES(?,?,?,?)`, key, -cost.Wood, -cost.Seeds, -cost.Dew); err != nil {
		return err
	}
	if err := event(ctx, tx, key, s.usage.GrowthDate(now), now, tree.Island, title, "你带来了材料，也带回了这片树荫。"); err != nil {
		return err
	}
	if err := changeCondition(ctx, tx, key, id, tree.Condition, "healthy", now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE event_trees SET tree_id=? WHERE event_key=?`, id, key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE event_outcomes SET restored=1 WHERE event_key=?`, key); err != nil {
		return err
	}
	return tx.Commit()
}

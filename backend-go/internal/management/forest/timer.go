package forest

import (
	"context"
	"fmt"
	"time"
)

type Timer struct {
	Revision    int64  `json:"revision"`
	Mode        string `json:"mode"`
	Started     int64  `json:"started"`
	Due         int64  `json:"due"`
	Duration    int64  `json:"duration"`
	Left        int64  `json:"left"`
	WaitStarted int64  `json:"waitStarted"`
	WaitDue     int64  `json:"waitDue"`
	ServerNow   int64  `json:"serverNow"`
}

type TimerCommand struct {
	Revision int64  `json:"revision"`
	Action   string `json:"action"`
	Minutes  int64  `json:"minutes"`
}

func (s *Store) Timer(ctx context.Context, now time.Time) (Timer, error) {
	return readTimer(ctx, s.db, now)
}

func readTimer(ctx context.Context, db growthReader, now time.Time) (Timer, error) {
	t := Timer{ServerNow: now.UnixMilli()}
	err := db.QueryRowContext(ctx, `SELECT revision,mode,started,due,duration,left_ms,wait_started,wait_due FROM timer WHERE id=1`).
		Scan(&t.Revision, &t.Mode, &t.Started, &t.Due, &t.Duration, &t.Left, &t.WaitStarted, &t.WaitDue)
	return t, err
}

func (s *Store) ChangeTimer(ctx context.Context, cmd TimerCommand, now time.Time) (Timer, error) {
	day := s.usage.GrowthDate(now)
	if day == "" || cmd.Minutes < 0 || cmd.Minutes > 240 {
		return Timer{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Timer{}, err
	}
	defer tx.Rollback()
	t, err := readTimer(ctx, tx, now)
	if err != nil {
		return t, err
	}
	if t.Revision != cmd.Revision {
		return t, ErrConflict
	}
	g, err := readGrowth(ctx, tx)
	if err != nil {
		return t, err
	}
	start := func(mode string, duration int64) {
		t.Mode, t.Started, t.Due, t.Duration, t.Left = mode, t.ServerNow, t.ServerNow+duration, duration, 0
	}
	clear := func() {
		t.Mode, t.Started, t.Due, t.Duration, t.Left = "", 0, 0, 0, 0
	}
	addSegment := func(kind string, from, to int64) error {
		if to <= from {
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO timer_segments(revision,kind,started,ended) VALUES(?,?,?,?)`, t.Revision, kind, from, to)
		return err
	}
	finishFocus := func() error {
		if t.Mode != "focus" {
			return nil
		}
		end := min(t.Due, t.ServerNow)
		if err := addSegment("focus", t.Started, end); err != nil {
			return err
		}
		g.focus += max(0, end-t.Started)
		return nil
	}
	switch cmd.Action {
	case "finish-focus":
		if t.Mode != "focus" || t.Due > t.ServerNow {
			return t, ErrInvalid
		}
		if err := finishFocus(); err != nil {
			return t, err
		}
		t.Mode = "focus-due"
	case "focus", "rest":
		if cmd.Minutes == 0 || t.Mode == "rest" {
			return t, ErrInvalid
		}
		if err := finishFocus(); err != nil {
			return t, err
		}
		start(cmd.Action, cmd.Minutes*60000)
	case "pause":
		if t.Mode != "focus" || t.Due <= t.ServerNow {
			return t, ErrInvalid
		}
		if err := finishFocus(); err != nil {
			return t, err
		}
		t.Mode, t.Started, t.Left, t.Due = "paused", 0, t.Due-t.ServerNow, 0
	case "resume":
		if t.Mode != "paused" || t.Left == 0 {
			return t, ErrInvalid
		}
		start("focus", t.Left)
	case "handoff":
		if t.WaitStarted != 0 {
			return t, ErrInvalid
		}
		if err := finishFocus(); err != nil {
			return t, err
		}
		if t.Mode != "rest" {
			clear()
		}
		t.WaitStarted = t.ServerNow
		if cmd.Minutes > 0 {
			t.WaitDue = t.ServerNow + cmd.Minutes*60000
		}
	case "end-wait":
		if t.WaitStarted == 0 {
			return t, ErrInvalid
		}
		if err := addSegment("wait", t.WaitStarted, t.ServerNow); err != nil {
			return t, err
		}
		t.WaitStarted, t.WaitDue = 0, 0
	case "confirm-rest":
		if t.Mode != "rest" || t.Due > t.ServerNow {
			return t, ErrInvalid
		}
		if err := addSegment("rest", t.Started, t.Due); err != nil {
			return t, err
		}
		g.rest += t.Duration
		if err := reward(ctx, tx, "daily-rest:"+day, 2); err != nil {
			return t, err
		}
		if err := event(ctx, tx, "daily-rest:"+day, day, now, 0, "林地安静了一会儿", "你抽空歇了歇，海风也跟着慢下来。今天的第一段休息，带来了 2 点成就。"); err != nil {
			return t, err
		}
		clear()
	case "end":
		if t.Mode == "" {
			return t, ErrInvalid
		}
		if err := finishFocus(); err != nil {
			return t, err
		}
		clear()
	default:
		return t, ErrInvalid
	}
	for g.focus >= focusGoal {
		g.focus -= focusGoal
		if err := plant(ctx, tx, "focus", day, now, g.level); err != nil {
			return t, err
		}
	}
	for g.rest >= restGoal {
		g.rest -= restGoal
		if err := plant(ctx, tx, "rest", day, now, g.level); err != nil {
			return t, err
		}
	}
	t.Revision++
	if _, err := tx.ExecContext(ctx, `UPDATE timer SET revision=?,mode=?,started=?,due=?,duration=?,left_ms=?,wait_started=?,wait_due=? WHERE id=1`,
		t.Revision, t.Mode, t.Started, t.Due, t.Duration, t.Left, t.WaitStarted, t.WaitDue); err != nil {
		return t, fmt.Errorf("保存计时状态: %w", err)
	}
	if err := g.save(ctx, tx); err != nil {
		return t, err
	}
	if err := settleAchievements(ctx, tx, day, now); err != nil {
		return t, err
	}
	return t, tx.Commit()
}

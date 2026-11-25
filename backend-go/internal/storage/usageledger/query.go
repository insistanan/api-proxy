package usageledger

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Status struct {
	EnabledAt *time.Time `json:"enabledAt"`
	TimeZone  string     `json:"timeZone"`
	Today     string     `json:"today"`
	SyncIssue bool       `json:"syncIssue"`
}

type Day struct {
	Date     string `json:"date"`
	Tokens   int64  `json:"tokens"`
	Coverage string `json:"coverage"`
}

type Tokens struct {
	Input         int64 `json:"input"`
	Output        int64 `json:"output"`
	CacheCreation int64 `json:"cacheCreation"`
	CacheRead     int64 `json:"cacheRead"`
}

type UsageTotal struct {
	Tokens
	Attempts int64 `json:"attempts"`
}

type Model struct {
	Name   string `json:"name"`
	Tokens int64  `json:"tokens"`
}

type Detail struct {
	Day
	Upstream        UsageTotal `json:"upstream"`
	Estimated       UsageTotal `json:"estimated"`
	MissingAttempts int64      `json:"missingAttempts"`
	Models          []Model    `json:"models"`
	PeakHour        *time.Time `json:"peakHour"`
	UpdatedAt       *time.Time `json:"updatedAt"`
	TimeZone        string     `json:"timeZone"`
	Revision        int64      `json:"revision"`
	LateUpdate      bool       `json:"lateUpdate"`
}

func (s *Store) Status(now time.Time) Status {
	active := s.active.Load()
	if active == nil {
		return Status{Today: now.Format(dayFormat)}
	}
	start := time.UnixMilli(active.start).In(active.zone)
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{EnabledAt: &start, TimeZone: active.zone.String(), Today: now.In(active.zone).Format(dayFormat),
		SyncIssue: s.writeFailed || s.pendingLosses || len(s.losses) > 0}
}

func (s *Store) Calendar(ctx context.Context, year int, now time.Time) ([]Day, error) {
	active := s.active.Load()
	if active == nil {
		return []Day{}, nil
	}
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(1, 0, 0)
	today := now.In(active.zone).Format(dayFormat)
	query := `SELECT day,
SUM(CASE WHEN source = 'upstream' THEN input + output + creation + cache_read ELSE 0 END),
MAX(CASE WHEN source != 'upstream' THEN 1 ELSE 0 END)
FROM usage_buckets WHERE day >= ? AND day < ? GROUP BY day`
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, query, from.Format(dayFormat), to.Format(dayFormat))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]Day)
	for rows.Next() {
		var day Day
		var incomplete bool
		if err := rows.Scan(&day.Date, &day.Tokens, &incomplete); err != nil {
			return nil, err
		}
		if incomplete {
			day.Coverage = "partial"
		}
		values[day.Date] = day
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT start_day, end_day FROM usage_gaps WHERE end_day >= ? AND start_day < ?`, from.Format(dayFormat), to.Format(dayFormat))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var gaps [][2]string
	for rows.Next() {
		var bounds [2]string
		if err := rows.Scan(&bounds[0], &bounds[1]); err != nil {
			return nil, err
		}
		gaps = append(gaps, bounds)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]Day, 0, 366)
	for at := from; at.Before(to); at = at.AddDate(0, 0, 1) {
		key := at.Format(dayFormat)
		day := values[key]
		day.Date = key
		incomplete := day.Coverage == "partial"
		for _, gap := range gaps {
			if key >= gap[0] && key <= gap[1] {
				incomplete = true
				break
			}
		}
		day.Coverage = active.coverage(key, today, incomplete)
		result = append(result, day)
	}
	return result, tx.Commit()
}

func (active *recording) coverage(date, today string, incomplete bool) string {
	start := time.UnixMilli(active.start).In(active.zone)
	first := start.Format(dayFormat)
	if date > today || date < first {
		return "unrecorded"
	}
	atMidnight := start.Hour() == 0 && start.Minute() == 0 && start.Second() == 0 && start.Nanosecond() == 0
	if incomplete || (date == first && !atMidnight) {
		return "partial"
	}
	if date == today {
		return "open"
	}
	return "complete"
}

func (s *Store) Daily(ctx context.Context, date string, now time.Time) (Detail, error) {
	detail := Detail{Day: Day{Date: date, Coverage: "unrecorded"}, Models: []Model{}}
	active := s.active.Load()
	if active == nil {
		return detail, nil
	}
	detail.TimeZone = active.zone.String()
	if _, err := time.Parse(dayFormat, date); err != nil {
		return detail, fmt.Errorf("无效的统计日期: %w", err)
	}
	today := now.In(active.zone).Format(dayFormat)
	if active.coverage(date, today, false) == "unrecorded" {
		return detail, nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return detail, err
	}
	defer tx.Rollback()
	var gap bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM usage_gaps WHERE start_day <= ? AND end_day >= ?)`, date, date).Scan(&gap); err != nil {
		return detail, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT source, SUM(input), SUM(output), SUM(creation), SUM(cache_read), SUM(attempts), MAX(updated)
FROM usage_buckets WHERE day = ? GROUP BY source`, date)
	if err != nil {
		return detail, err
	}
	defer rows.Close()
	var updated int64
	for rows.Next() {
		var source string
		var value UsageTotal
		var stamp int64
		if err := rows.Scan(&source, &value.Input, &value.Output, &value.CacheCreation, &value.CacheRead, &value.Attempts, &stamp); err != nil {
			return detail, err
		}
		updated = max(updated, stamp)
		switch source {
		case "upstream":
			detail.Upstream = value
		case "estimated":
			detail.Estimated = value
		case "missing":
			detail.MissingAttempts = value.Attempts
		}
	}
	if err := rows.Err(); err != nil {
		return detail, err
	}
	detail.Coverage = active.coverage(date, today, gap || detail.Estimated.Attempts > 0 || detail.MissingAttempts > 0)
	detail.Tokens = detail.Upstream.Input + detail.Upstream.Output + detail.Upstream.CacheCreation + detail.Upstream.CacheRead
	// 由已去重的事实与覆盖缺口推导，已有账本也能得到稳定的票据版本。
	detail.Revision = 1 + detail.Upstream.Attempts + detail.Estimated.Attempts + detail.MissingAttempts
	if gap {
		detail.Revision++
	}
	if updated > 0 {
		stamp := time.UnixMilli(updated).In(active.zone)
		detail.UpdatedAt = &stamp
		detail.LateUpdate = date < stamp.Format(dayFormat)
	}
	if detail.Tokens > 0 {
		query := `SELECT hour_ms FROM usage_buckets WHERE day = ? AND source = 'upstream'
GROUP BY hour_ms ORDER BY SUM(input + output + creation + cache_read) DESC, hour_ms LIMIT 1`
		var hour int64
		if err := tx.QueryRowContext(ctx, query, date).Scan(&hour); err != nil {
			return detail, err
		}
		peak := time.UnixMilli(hour).In(active.zone)
		detail.PeakHour = &peak
		rows, err := tx.QueryContext(ctx, `SELECT model, SUM(input + output + creation + cache_read) AS total
FROM usage_buckets WHERE day = ? AND source = 'upstream' GROUP BY model ORDER BY total DESC, model LIMIT 12`, date)
		if err != nil {
			return detail, err
		}
		defer rows.Close()
		for rows.Next() {
			var model Model
			if err := rows.Scan(&model.Name, &model.Tokens); err != nil {
				return detail, err
			}
			detail.Models = append(detail.Models, model)
		}
		if err := rows.Err(); err != nil {
			return detail, err
		}
	}
	return detail, tx.Commit()
}

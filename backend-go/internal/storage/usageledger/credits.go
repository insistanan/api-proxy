package usageledger

import (
	"context"
	"time"

	"github.com/insistanan/api-proxy/internal/protocol/tokenusage"
)

func (s *Store) Credits(ctx context.Context, after int64) ([]tokenusage.Credit, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, input + output + creation + cache_read
FROM usage_facts WHERE seq > ? AND source = 'upstream' ORDER BY seq LIMIT 256`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var credits []tokenusage.Credit
	for rows.Next() {
		var item tokenusage.Credit
		if err := rows.Scan(&item.Cursor, &item.Tokens); err != nil {
			return nil, err
		}
		credits = append(credits, item)
	}
	return credits, rows.Err()
}

func (s *Store) GrowthDate(now time.Time) string {
	active := s.active.Load()
	if active == nil {
		return ""
	}
	return now.In(active.zone).Format(dayFormat)
}

// available 区分已完成判定但历史不足，与账本故障期间无法判定。
func (s *Store) GrowthBaseline(ctx context.Context, now time.Time) (value int64, available bool, err error) {
	status := s.Status(now)
	if status.EnabledAt == nil || status.SyncIssue {
		return 0, false, nil
	}
	today, err := time.Parse(dayFormat, status.Today)
	if err != nil {
		return 0, false, err
	}
	first := today.AddDate(0, 0, -28)
	var total, recorded, activeDays int64
	for year := first.Year(); year <= today.Year(); year++ {
		days, err := s.Calendar(ctx, year, now)
		if err != nil {
			return 0, false, err
		}
		for _, day := range days {
			if day.Date < first.Format(dayFormat) || day.Date >= status.Today || day.Coverage != "complete" {
				continue
			}
			recorded++
			total += day.Tokens
			if day.Tokens > 0 {
				activeDays++
			}
		}
	}
	if s.Status(now).SyncIssue {
		return 0, false, nil
	}
	if recorded < 14 || activeDays < 7 {
		return 0, true, nil
	}
	return max(1, total/recorded), true, nil
}

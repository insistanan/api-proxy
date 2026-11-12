package conversation

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type ListOptions struct {
	Query    string
	Kind     string
	Status   string
	Order    string
	Page     int
	PageSize int
}

type Page struct {
	Conversations []*Record `json:"conversations"`
	Total         int       `json:"total"`
	Page          int       `json:"page"`
	PageSize      int       `json:"pageSize"`
}

// QueryPage 在数据库分页后才解码正文；发送状态始终采用当前进程的实时快照。
func (r *Registry) QueryPage(options ListOptions) (Page, error) {
	page := Page{Conversations: make([]*Record, 0), Page: options.Page, PageSize: options.PageSize}
	if options.Page < 1 || options.PageSize < 1 || options.PageSize > 100 {
		return page, fmt.Errorf("分页参数超出范围")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(options.Query))
	if r.store == nil {
		for _, rec := range r.records {
			if options.Kind != "" && options.Kind != rec.APIKind {
				continue
			}
			if options.Status == "active" && !rec.IsSending || options.Status == "idle" && rec.IsSending {
				continue
			}
			if q != "" && !matchRecordQuery(rec, q) {
				continue
			}
			page.Conversations = append(page.Conversations, cloneRecord(rec))
		}
		sort.Slice(page.Conversations, func(i, j int) bool {
			a, b := page.Conversations[i], page.Conversations[j]
			if options.Order == "status" && a.IsSending != b.IsSending {
				return a.IsSending
			}
			if !a.FirstSeenAt.Equal(b.FirstSeenAt) {
				return a.FirstSeenAt.After(b.FirstSeenAt)
			}
			return a.ID < b.ID
		})
		page.Total = len(page.Conversations)
		if options.Page-1 > page.Total/options.PageSize {
			page.Conversations = page.Conversations[:0]
			return page, nil
		}
		start := min((options.Page-1)*options.PageSize, page.Total)
		page.Conversations = page.Conversations[start:min(start+options.PageSize, page.Total)]
		return page, nil
	}
	activeIDs := make([]string, 0)
	for id, rec := range r.records {
		if rec.ActiveRequests > 0 {
			activeIDs = append(activeIDs, id)
		}
	}
	active, err := json.Marshal(activeIDs)
	if err != nil {
		return page, err
	}
	activeSQL := `c.id IN (SELECT value FROM json_each(?))`
	where := `WHERE 1=1`
	args := make([]any, 0)
	if options.Kind != "" {
		where += ` AND c.api_kind = ?`
		args = append(args, options.Kind)
	}
	if options.Status == "active" || options.Status == "idle" {
		where += ` AND `
		if options.Status == "idle" {
			where += `NOT `
		}
		where += activeSQL
		args = append(args, string(active))
	}
	if q != "" {
		where += ` AND (instr(lower(c.id || char(10) || c.name || char(10) || c.api_kind || char(10) ||
 c.last_model || char(10) || c.last_resolved_model || char(10) || c.first_prompt || char(10) || c.last_error || char(10) ||
 COALESCE(json_extract(NULLIF(c.route_override_json, ''), '$.kind'), '') || char(10) ||
 COALESCE(json_extract(NULLIF(c.route_override_json, ''), '$.channelIndex'), '') || char(10) ||
 COALESCE(json_extract(NULLIF(c.route_override_json, ''), '$.channelName'), '') || char(10) ||
 COALESCE(json_extract(NULLIF(c.last_resolved_json, ''), '$.channelName'), '')), ?) > 0
 OR EXISTS (SELECT 1 FROM json_each(c.prompts_json) WHERE instr(lower(value), ?) > 0))`
		args = append(args, q, q)
	}
	if err := r.store.db.QueryRow(`SELECT COUNT(*) FROM conversations c `+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	if options.Page-1 > page.Total/options.PageSize {
		return page, nil
	}
	order := ` ORDER BY `
	if options.Order == "status" {
		order += activeSQL + ` DESC, `
		args = append(args, string(active))
	}
	order += `c.first_seen_at DESC, c.id ASC LIMIT ? OFFSET ?`
	args = append(args, options.PageSize, (options.Page-1)*options.PageSize)
	records, err := r.store.loadRecords(where+order, args...)
	if err != nil {
		return page, err
	}
	for _, rec := range records {
		if cached := r.records[rec.ID]; cached != nil {
			rec = cloneRecord(cached)
		}
		page.Conversations = append(page.Conversations, rec)
	}
	return page, nil
}

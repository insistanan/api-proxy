package availability

import (
	"sort"
	"time"
)

const (
	BucketMillis = int64(time.Hour / time.Millisecond)
	MinSamples   = 5
)

// Aggregate 是数据库已聚合的小时统计；Recent 只统计最近一小时。
type Aggregate struct {
	ChannelID     string
	Model         string
	CandidateID   string
	Outcome       string
	Reason        string
	Bucket        int64
	Count         int64
	Recent        int64
	LastSeen      int64
	LastSequence  int64
	CooldownUntil int64
}

type Counts struct {
	Success     int64 `json:"success"`
	Failure     int64 `json:"failure"`
	Unsupported int64 `json:"unsupported"`
	Credential  int64 `json:"credential"`
	RateLimited int64 `json:"rateLimited"`
	Ignored     int64 `json:"ignored"`
}

func (c *Counts) add(outcome string, count int64) {
	switch outcome {
	case Success:
		c.Success += count
	case Failure:
		c.Failure += count
	case Unsupported:
		c.Unsupported += count
	case Credential:
		c.Credential += count
	case RateLimited:
		c.RateLimited += count
	case Ignored:
		c.Ignored += count
	}
}

func (c Counts) Eligible() int64 {
	return c.Success + c.Failure + c.Unsupported + c.Credential + c.RateLimited
}

type Bucket struct {
	From int64 `json:"from"`
	Counts
}

type Candidate struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Endpoint      string `json:"endpoint"`
	Protocol      string `json:"protocol"`
	State         string `json:"state"`
	LastSeen      int64  `json:"lastSeen"`
	LastOutcome   string `json:"lastOutcome"`
	LastReason    string `json:"lastReason"`
	CooldownUntil int64  `json:"cooldownUntil"`
	Counts
	Recent        Counts `json:"recent"`
	lastEffective int64
	lastSequence  int64
}

type Row struct {
	ChannelID   string `json:"channelId"`
	ChannelName string `json:"channelName"`
	Model       string `json:"model"`
	State       string `json:"state"`
	LastSeen    int64  `json:"lastSeen"`
	Counts
	Recent     Counts      `json:"recent"`
	Buckets    []Bucket    `json:"buckets"`
	Candidates []Candidate `json:"candidates"`
}

// Summarize 只接受当前配置仍存在的候选；换 Key、URL 或协议后不会继承旧故障。
func Summarize(channelID, name, model string, candidates []Candidate, aggregates []Aggregate, now time.Time) Row {
	row := Row{ChannelID: channelID, ChannelName: name, Model: model, State: "unknown", Candidates: append([]Candidate{}, candidates...), Buckets: make([]Bucket, 24)}
	start := now.UnixMilli()/BucketMillis*BucketMillis - 23*BucketMillis
	for i := range row.Buckets {
		row.Buckets[i].From = start + int64(i)*BucketMillis
	}
	indices := make(map[string]int, len(candidates))
	for i, candidate := range candidates {
		indices[candidate.ID] = i
	}
	for _, a := range aggregates {
		i, ok := indices[a.CandidateID]
		if !ok || a.ChannelID != channelID || a.Model != model {
			continue
		}
		candidate := &row.Candidates[i]
		candidate.Counts.add(a.Outcome, a.Count)
		candidate.Recent.add(a.Outcome, a.Recent)
		candidate.LastSeen = max(candidate.LastSeen, a.LastSeen)
		if a.Outcome != Ignored && (a.LastSeen > candidate.lastEffective || a.LastSeen == candidate.lastEffective && a.LastSequence > candidate.lastSequence) {
			candidate.lastEffective = a.LastSeen
			candidate.lastSequence = a.LastSequence
			candidate.LastOutcome = a.Outcome
			candidate.LastReason = a.Reason
			candidate.CooldownUntil = a.CooldownUntil
		}
		row.Counts.add(a.Outcome, a.Count)
		row.Recent.add(a.Outcome, a.Recent)
		row.LastSeen = max(row.LastSeen, a.LastSeen)
		bucket := (a.Bucket - start) / BucketMillis
		if a.Bucket >= start && bucket < int64(len(row.Buckets)) {
			row.Buckets[bucket].Counts.add(a.Outcome, a.Count)
		}
	}
	allUnsupported, allCooling := len(candidates) > 0, len(candidates) > 0
	degraded := false
	for i := range row.Candidates {
		c := &row.Candidates[i]
		c.State = "unknown"
		if c.LastOutcome == RateLimited && c.CooldownUntil > now.UnixMilli() {
			c.State = "cooling"
		} else if c.lastEffective >= now.Add(-time.Hour).UnixMilli() {
			switch c.LastOutcome {
			case Unsupported:
				c.State = "unsupported"
			case Credential, Failure:
				c.State = "degraded"
			default:
				c.State = rateState(c.Recent)
			}
		}
		allUnsupported = allUnsupported && c.State == "unsupported"
		allCooling = allCooling && c.State == "cooling"
		degraded = degraded || c.State == "degraded" || c.State == "unsupported" || c.State == "cooling"
	}
	switch {
	case allUnsupported:
		row.State = "unsupported"
	case allCooling:
		row.State = "cooling"
	case degraded:
		row.State = "degraded"
	default:
		row.State = rateState(row.Recent)
	}
	sort.Slice(row.Candidates, func(i, j int) bool { return row.Candidates[i].Label < row.Candidates[j].Label })
	return row
}

func rateState(counts Counts) string {
	if counts.Eligible() < MinSamples {
		return "unknown"
	}
	if float64(counts.Success)/float64(counts.Eligible()) >= 0.95 {
		return "healthy"
	}
	return "degraded"
}

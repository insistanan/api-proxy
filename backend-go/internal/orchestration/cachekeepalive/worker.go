package cachekeepalive

import (
	"container/heap"
	"context"
	"fmt"
	"time"
)

func (m *Manager) loop(changes <-chan struct{}) {
	defer m.workers.Done()
	tick := time.NewTicker(time.Second)
	cleanup := time.NewTicker(cleanupInterval)
	defer tick.Stop()
	defer cleanup.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-changes:
			m.invalidate()
		case <-cleanup.C:
			m.clean()
		case <-tick.C:
			m.dispatch()
		}
	}
}

func (m *Manager) invalidate() {
	_, revision := m.state()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.entries {
		if e.revision != revision && e.snapshot != nil {
			m.stopEntry(e, "配置已更新，等待新的真实请求")
		}
	}
}

func (m *Manager) clean() {
	m.invalidate()
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for key, e := range m.entries {
		// 尚未解析的候选也不能被长时间流式请求无限保留。
		if e.snapshot != nil && e.snapshot.body == nil && now.Sub(e.snapshot.lastSent) >= time.Hour {
			m.stopEntry(e, "候选超过一小时仍未进入空闲准备阶段")
		}
		if e.active != 0 {
			continue
		}
		if e.snapshot != nil && !e.deadline.IsZero() && !now.Before(e.deadline) {
			m.stopEntry(e, "已达到最长保活时间")
		}
		if e.snapshot == nil && now.Sub(e.updated) >= statusRetention {
			delete(m.entries, key)
		}
	}
}

func (m *Manager) dispatch() {
	_, revision := m.state()
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for len(m.queue) > 0 && !m.queue[0].due.After(now) {
		e := heap.Pop(&m.queue).(*entry)
		s := e.snapshot
		if m.stopped || s == nil || e.active != 0 {
			continue
		}
		if e.revision != revision {
			m.stopEntry(e, "配置已更新，等待新的真实请求")
			continue
		}
		if !now.Before(e.deadline) {
			m.stopEntry(e, "已达到最长保活时间")
			continue
		}
		prepare := s.body == nil
		if !prepare {
			if reason := stopReason(e, now); reason != "" {
				m.stopEntry(e, reason)
				continue
			}
		}
		ctx, cancel := context.WithTimeout(m.ctx, e.deadline.Sub(now))
		job := work{entry: e, generation: e.generation, snapshot: s, ctx: ctx, prepare: prepare}
		select {
		case m.jobs <- job:
			s.refs++
			e.cancel = cancel
			e.status.NextAt = 0
			e.status.State = "sending"
			if prepare {
				e.status.State = "preparing"
			}
		default:
			cancel()
			// 没有空闲工作线程时只保留该会话的一个到期项，不堆积发送任务。
			m.schedule(e, now.Add(time.Second))
			return
		}
	}
}

func stopReason(e *entry, now time.Time) string {
	s := e.snapshot
	if s.policy.MaxAttempts > 0 && e.status.Attempts >= s.policy.MaxAttempts {
		return "已达到保活次数上限"
	}
	if !now.Before(e.deadline) {
		return "已达到最长保活时间"
	}
	if s.ttl > 0 && !now.Before(s.lastSent.Add(s.ttl-expiryMargin)) {
		return "已错过缓存续期窗口，不重建过期缓存"
	}
	used := e.status.InputTokens + e.status.CacheRead + e.status.CacheCreation + e.status.OutputTokens
	// 保留整段输入和最大输出的预算；实际扣费无法由网关核验。
	if s.mode != "explicit" && used+s.input+int64(len(s.policy.Prompt))+32+int64(s.policy.MaxOutputTokens) > s.policy.MaxTotalTokens {
		return "剩余 Token 预算不足以发送下一次心跳"
	}
	return ""
}

func (m *Manager) worker() {
	defer m.workers.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case job := <-m.jobs:
			m.run(job)
		}
	}
}

func (m *Manager) validWork(job work, revision uint64) bool {
	e := job.entry
	return !m.stopped && m.entries[e.key] == e && e.generation == job.generation &&
		e.revision == revision && e.snapshot == job.snapshot && e.active == 0
}

func (m *Manager) run(job work) {
	defer func() {
		m.mu.Lock()
		m.release(job.snapshot)
		m.mu.Unlock()
	}()
	if job.ctx.Err() != nil {
		m.mu.Lock()
		if e := job.entry; m.entries[e.key] == e && e.snapshot == job.snapshot && e.generation == job.generation {
			m.stopEntry(e, "保活任务已取消或达到持续时间上限")
		}
		m.mu.Unlock()
		return
	}
	if job.prepare {
		body, model, ttl, err := prepareBody(job.snapshot)
		if err == nil && job.snapshot.mode != "explicit" && m.checkPrompt != nil {
			err = m.checkPrompt(job.ctx, job.snapshot.policy.Prompt, job.snapshot.source.ConversationID)
		}
		_, revision := m.state()
		m.mu.Lock()
		defer m.mu.Unlock()
		if !m.validWork(job, revision) {
			return
		}
		e, s := job.entry, job.snapshot
		if job.ctx.Err() != nil {
			m.stopEntry(e, "保活准备已达到持续时间上限")
			return
		}
		e.cancel()
		e.cancel = nil
		if err != nil {
			m.stopEntry(e, err.Error())
			return
		}
		s.body, s.ttl, s.source.Open, s.model = body, ttl, nil, model
		e.status.Model, e.status.State, e.status.CacheMode = model, "ready", s.mode
		if reason := stopReason(e, m.now()); reason != "" {
			m.stopEntry(e, reason)
			return
		}
		// 第一轮遵守用户配置的空闲时间；不能因真实请求响应较慢而提前发送。
		due := e.updated.Add(time.Duration(s.policy.IntervalSeconds) * time.Second)
		if s.mode == "explicit" {
			due = m.now()
		}
		if due.Before(m.now()) {
			due = m.now()
		}
		m.schedule(e, minTime(due, e.deadline))
		return
	}

	_, revision := m.state()
	m.mu.Lock()
	valid := m.validWork(job, revision) && job.ctx.Err() == nil
	m.mu.Unlock()
	if !valid {
		return
	}
	result := m.send(job.ctx, job.snapshot)
	_, revision = m.state()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.validWork(job, revision) {
		return // 已被新请求取消的结果仅保留独立日志，不触碰新一轮状态。
	}
	e, s := job.entry, job.snapshot
	contextErr := job.ctx.Err()
	e.cancel()
	e.cancel = nil
	e.updated = m.now()
	e.status.UpdatedAt = e.updated.UnixMilli()
	if result.sent || s.mode == "explicit" && result.err == nil {
		e.status.Attempts++
		e.status.LastAt = result.sentAt.UnixMilli()
	}
	input, read, creation, output, usageErr := usageNumbers(result.usage)
	if usageErr == nil {
		e.status.InputTokens += input
		e.status.CacheRead += read
		e.status.CacheCreation += creation
		e.status.OutputTokens += output
		e.status.CacheHit = float64(read) * 100 / float64(input+read+creation)
	}
	if result.err != nil {
		m.stopEntry(e, result.err.Error())
		return
	}
	if contextErr != nil {
		m.stopEntry(e, "保活任务达到持续时间上限")
		return
	}
	if s.mode == "explicit" {
		e.status.ExpiresAt = result.expiresAt.UnixMilli()
		s.lastSent = result.sentAt
		if reason := stopReason(e, m.now()); reason != "" {
			m.stopEntry(e, reason)
			return
		}
		e.status.State = "ready"
		m.schedule(e, minTime(s.lastSent.Add(time.Duration(s.policy.IntervalSeconds)*time.Second), e.deadline))
		return
	}
	if usageErr != nil {
		m.stopEntry(e, usageErr.Error())
		return
	}
	threshold := int64(s.policy.MinCacheHitPercent)
	if read == 0 || read*100 < s.baseline*threshold || e.status.CacheHit*100 < s.baselineHit*float64(threshold) ||
		s.read > 0 && read*100 < s.read*threshold {
		m.stopEntry(e, fmt.Sprintf("缓存覆盖不足（本次 %.1f%%），停止继续付费保活", e.status.CacheHit))
		return
	}
	s.read = read
	s.input = input + read + creation
	s.lastSent = result.sentAt
	if reason := stopReason(e, m.now()); reason != "" {
		m.stopEntry(e, reason)
		return
	}
	e.status.State = "ready"
	m.schedule(e, minTime(s.lastSent.Add(time.Duration(s.policy.IntervalSeconds)*time.Second), e.deadline))
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

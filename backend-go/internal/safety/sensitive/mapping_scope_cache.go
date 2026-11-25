package sensitive

import (
	"maps"
	"strings"
	"sync"
	"time"
)

const (
	scopeCacheEntries = 32
	scopeCacheBudget  = 16 << 20
	scopeCacheTTL     = 5 * time.Minute
)

// 快照只属于一个映射库与一个授权版本；请求复制 map，字符串与替换表只读共享。
type scopeMappingSnapshot struct {
	byOriginal    map[string]string
	byPlaceholder map[string]string
	bytes         int
	revision      int64
	expires       time.Time
	once          sync.Once
	replacer      *strings.Replacer
}

func (s *scopeMappingSnapshot) maskReplacer() *strings.Replacer {
	s.once.Do(func() { s.replacer = newMaskReplacer(s.byOriginal) })
	return s.replacer
}

func (s *scopeMappingSnapshot) install(v *Vault) {
	v.byOriginal = maps.Clone(s.byOriginal)
	v.byPlaceholder = maps.Clone(s.byPlaceholder)
	v.bytes = s.bytes
	for placeholder := range s.byPlaceholder {
		v.scopeKnown[placeholder] = struct{}{}
	}
	v.scopeSnapshot = s
}

type scopeCacheEntry struct {
	snapshot *scopeMappingSnapshot
	used     uint64
	cost     int
}

type scopeMappingCache struct {
	mu      sync.Mutex
	entries map[string]scopeCacheEntry
	bytes   int
	clock   uint64
	closed  bool
}

func (c *scopeMappingCache) get(scope string, revision int64, now time.Time) *scopeMappingSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[scope]
	if c.closed || !ok {
		return nil
	}
	if entry.snapshot.revision != revision || !now.Before(entry.snapshot.expires) {
		c.removeLocked(scope)
		return nil
	}
	c.clock++
	entry.used = c.clock
	c.entries[scope] = entry
	return entry.snapshot
}

func (c *scopeMappingCache) put(scope string, revision int64, expires time.Time, v *Vault) *scopeMappingSnapshot {
	// 计入 map 和替换表的保守预算；实际堆内存仍需通过基准测量。
	cost := v.bytes*16 + len(v.byPlaceholder)*512
	if len(v.byPlaceholder) == 0 || cost > scopeCacheBudget {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.removeLocked(scope)
	for len(c.entries) >= scopeCacheEntries || c.bytes+cost > scopeCacheBudget {
		var oldest string
		var used uint64 = ^uint64(0)
		for key, entry := range c.entries {
			if entry.used < used {
				oldest, used = key, entry.used
			}
		}
		c.removeLocked(oldest)
	}
	if c.entries == nil {
		c.entries = make(map[string]scopeCacheEntry)
	}
	snapshot := &scopeMappingSnapshot{
		byOriginal: maps.Clone(v.byOriginal), byPlaceholder: maps.Clone(v.byPlaceholder),
		bytes: v.bytes, revision: revision, expires: expires,
	}
	c.clock++
	c.entries[scope] = scopeCacheEntry{snapshot: snapshot, used: c.clock, cost: cost}
	c.bytes += cost
	return snapshot
}

func (c *scopeMappingCache) removeLocked(scope string) {
	c.bytes -= c.entries[scope].cost
	delete(c.entries, scope)
}

func (c *scopeMappingCache) purge(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for scope, entry := range c.entries {
		if !now.Before(entry.snapshot.expires) {
			c.removeLocked(scope)
		}
	}
}

func (c *scopeMappingCache) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	clear(c.entries)
	c.bytes = 0
}

package clients

import (
	"context"
	"sync"
	"time"
)

const (
	// remoteSkillCacheTTL 覆盖“预览后思考一会儿再点安装”的常见间隔，
	// 同时限制仓库更新后拿到旧版本的时间窗口。
	remoteSkillCacheTTL = 10 * time.Minute
	// remoteSkillCacheMaxBytes 限制缓存占用的内存，超出后按最早过期时间淘汰。
	remoteSkillCacheMaxBytes = 64 << 20
)

type remoteSkillCacheEntry struct {
	pkg       *remoteSkillPackage
	expiresAt time.Time
	size      int64
}

// remoteSkillCache 缓存已下载并校验的远程 Skill 包。
// 包内容在写入缓存后视为只读，预览和安装只读取，不修改 Files。
type remoteSkillCache struct {
	mu         sync.Mutex
	entries    map[string]remoteSkillCacheEntry
	totalBytes int64
	flights    map[string]*remoteSkillFlight
}

type remoteSkillFlight struct {
	done chan struct{}
	pkg  *remoteSkillPackage
	err  error
}

// 首个请求拥有下载 Context；等待者可独立取消。任务结束即删除进行中记录。
func (c *remoteSkillCache) load(ctx context.Context, key string, fetch func() (*remoteSkillPackage, error)) (*remoteSkillPackage, error) {
	c.mu.Lock()
	if c.flights == nil {
		c.flights = make(map[string]*remoteSkillFlight)
	}
	if pending, ok := c.flights[key]; ok {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending.done:
			return pending.pkg, pending.err
		}
	}
	pending := &remoteSkillFlight{done: make(chan struct{})}
	c.flights[key] = pending
	c.mu.Unlock()
	if cached, ok := c.get(key); ok {
		pending.pkg = cached
	} else {
		pending.pkg, pending.err = fetch()
		if pending.err == nil {
			c.put(key, pending.pkg)
		}
	}
	c.mu.Lock()
	delete(c.flights, key)
	close(pending.done)
	c.mu.Unlock()
	return pending.pkg, pending.err
}

func (c *remoteSkillCache) get(key string) (*remoteSkillPackage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		c.totalBytes -= entry.size
		return nil, false
	}
	return entry.pkg, true
}

func (c *remoteSkillCache) put(key string, pkg *remoteSkillPackage) {
	if pkg == nil {
		return
	}
	size := remoteSkillPackageBytes(pkg)
	if size > remoteSkillCacheMaxBytes {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]remoteSkillCacheEntry)
	}
	now := time.Now()
	c.evictExpiredLocked(now)
	if existing, exists := c.entries[key]; exists {
		c.totalBytes -= existing.size
		delete(c.entries, key)
	}
	for c.totalBytes+size > remoteSkillCacheMaxBytes && len(c.entries) > 0 {
		c.evictOldestLocked()
	}
	c.entries[key] = remoteSkillCacheEntry{
		pkg:       pkg,
		expiresAt: now.Add(remoteSkillCacheTTL),
		size:      size,
	}
	c.totalBytes += size
}

func (c *remoteSkillCache) evictExpiredLocked(now time.Time) {
	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
			c.totalBytes -= entry.size
		}
	}
}

func (c *remoteSkillCache) evictOldestLocked() {
	oldestKey := ""
	var oldestExpiry time.Time
	for key, entry := range c.entries {
		if oldestKey == "" || entry.expiresAt.Before(oldestExpiry) {
			oldestKey = key
			oldestExpiry = entry.expiresAt
		}
	}
	if oldestKey == "" {
		return
	}
	c.totalBytes -= c.entries[oldestKey].size
	delete(c.entries, oldestKey)
}

func remoteSkillPackageBytes(pkg *remoteSkillPackage) int64 {
	if pkg == nil {
		return 0
	}
	var size int64
	for _, content := range pkg.Files {
		size += int64(len(content))
	}
	return size
}

package session

import "time"

// 亲和是可丢弃提示。条数与单条长度共同限制驻留内存，不拒绝正常代理请求。
const maxAffinityEntries = 4096
const maxAffinityFieldBytes = 4096

func makeAffinityRoom[T any](entries map[string]*T, key string, usedAt func(*T) time.Time) {
	if _, exists := entries[key]; exists || len(entries) < maxAffinityEntries {
		return
	}
	var oldestKey string
	var oldest time.Time
	for candidate, value := range entries {
		at := usedAt(value)
		if oldest.IsZero() || at.Before(oldest) {
			oldestKey, oldest = candidate, at
		}
	}
	delete(entries, oldestKey)
}

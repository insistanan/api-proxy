package visionlayer

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/insistanan/api-proxy/internal/scheduler"
)

func buildImageCacheKey(fingerprint string, kind scheduler.ChannelKind, model string) string {
	// 缓存键仅基于图片指纹、渠道类型和模型，不包含 channelID 和 intentFingerprint。
	// 这样同一张图片在同一对话中只解析一次，不管：
	// - 用哪个 vision channel 解析的（回退到其他渠道后仍能命中缓存）
	// - 用户后续消息的文本内容如何变化（历史图片块重发时不会重新识图）
	profile := strings.Join([]string{
		visionPromptVersion,
		string(kind),
		strings.TrimSpace(model),
		strings.TrimSpace(fingerprint),
	}, "\n")
	sum := sha256.Sum256([]byte(profile))
	return hex.EncodeToString(sum[:])
}

func memoryCacheKey(conversationID string, key string) string {
	return conversationID + "\x00" + key
}

// loadKnownImageFingerprints 返回当前对话中已记录的图片指纹集合。
// 用于判断某张图片是否在之前的请求中出现过——如果出现过但缓存未命中，
// 说明之前解析失败了，应对其采用非阻塞模式。
func loadKnownImageFingerprints(channelScheduler *scheduler.ChannelScheduler, conversationID string) map[string]bool {
	if channelScheduler == nil {
		return nil
	}
	registry := channelScheduler.GetConversationRegistry()
	if registry == nil {
		return nil
	}
	record, ok := registry.Get(conversationID)
	if !ok || record == nil {
		return nil
	}
	result := make(map[string]bool, len(record.ImageFingerprints))
	for _, fp := range record.ImageFingerprints {
		fp = strings.TrimSpace(fp)
		if fp != "" {
			result[fp] = true
		}
	}
	return result
}

func loadCache(channelScheduler *scheduler.ChannelScheduler, conversationID string, key string) (string, bool, error) {
	memoryKey := memoryCacheKey(conversationID, key)
	visionCache.Lock()
	entry, ok := visionCache.items[memoryKey]
	if ok && !time.Now().After(entry.expiresAt) {
		visionCache.Unlock()
		return entry.result, true, nil
	}
	if ok {
		delete(visionCache.items, memoryKey)
	}
	visionCache.Unlock()

	if channelScheduler == nil || channelScheduler.GetConversationRegistry() == nil {
		return "", false, nil
	}
	result, ok, err := channelScheduler.GetConversationRegistry().LoadConversationImageUnderstanding(conversationID, key)
	if err != nil || !ok {
		return result, ok, err
	}
	storeMemoryCache(memoryKey, result)
	return result, true, nil
}

func claimAnalysis(key string) (string, bool, *visionInflightCall, bool) {
	now := time.Now()
	visionCache.Lock()
	defer visionCache.Unlock()
	if entry, ok := visionCache.items[key]; ok {
		if !now.After(entry.expiresAt) {
			return entry.result, true, nil, false
		}
		delete(visionCache.items, key)
	}
	if call := visionCache.inflight[key]; call != nil {
		return "", false, call, false
	}
	call := &visionInflightCall{done: make(chan struct{})}
	visionCache.inflight[key] = call
	return "", false, call, true
}

func finishAnalysis(key string, call *visionInflightCall, result string, err error) {
	if call == nil {
		return
	}
	call.once.Do(func() {
		visionCache.Lock()
		call.result = result
		call.err = err
		if visionCache.inflight[key] == call {
			delete(visionCache.inflight, key)
		}
		close(call.done)
		visionCache.Unlock()
	})
}

func failPendingAnalyses(images []visionImage, err error) {
	for _, image := range images {
		finishAnalysis(image.memoryKey, image.call, "", err)
	}
}

func storeCache(channelScheduler *scheduler.ChannelScheduler, conversationID string, key string, result string) error {
	if channelScheduler != nil && channelScheduler.GetConversationRegistry() != nil {
		if err := channelScheduler.GetConversationRegistry().SaveConversationImageUnderstanding(conversationID, key, result); err != nil {
			return err
		}
	}
	storeMemoryCache(memoryCacheKey(conversationID, key), result)
	return nil
}

// ClearCache 清空进程内图片理解缓存；持久化记录由对话删除逻辑清理。
func ClearCache() {
	visionCache.Lock()
	visionCache.items = make(map[string]cacheEntry)
	visionCache.Unlock()
}

// ClearConversationCache 清除单个已删除对话的进程内图片理解结果。
func ClearConversationCache(conversationID string) {
	prefix := strings.TrimSpace(conversationID) + "\x00"
	if prefix == "\x00" {
		return
	}
	visionCache.Lock()
	for key := range visionCache.items {
		if strings.HasPrefix(key, prefix) {
			delete(visionCache.items, key)
		}
	}
	visionCache.Unlock()
}

func storeMemoryCache(key string, result string) {
	visionCache.Lock()
	defer visionCache.Unlock()
	now := time.Now()
	if len(visionCache.items) >= 512 {
		for existingKey, entry := range visionCache.items {
			if now.After(entry.expiresAt) {
				delete(visionCache.items, existingKey)
			}
		}
	}
	for len(visionCache.items) >= 512 {
		oldestKey := ""
		var oldestExpiry time.Time
		for existingKey, entry := range visionCache.items {
			if oldestKey == "" || entry.expiresAt.Before(oldestExpiry) {
				oldestKey = existingKey
				oldestExpiry = entry.expiresAt
			}
		}
		if oldestKey == "" {
			break
		}
		delete(visionCache.items, oldestKey)
	}
	visionCache.items[key] = cacheEntry{result: result, expiresAt: now.Add(visionCacheTTL)}
}

package visionlayer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"insistanan/api-proxy/internal/infra/config"
)

// visionCacheStore 只要求对话内图片结果的读写，不依赖渠道调度。
type visionCacheStore interface {
	LoadConversationImageUnderstanding(ctx context.Context, conversationID, cacheKey string) (string, bool, error)
	SaveConversationImageUnderstanding(ctx context.Context, conversationID, cacheKey, result string) error
}

func buildImageCacheKey(fingerprint string, upstream *config.UpstreamConfig, resolvedModel string, profile visionAnalysisProfile, images []visionImage) string {
	// 使用实际发送的模型和分析文本；渠道或端点变化也不能复用其他来源的结果。
	// %q 转义每个字段，避免模型名或用户文本中的分隔符造成键碰撞。
	fields := []string{
		visionCacheVersion, visionPromptVersion, upstream.ID, upstream.ServiceType,
		strconv.FormatBool(upstream.UseFullURL), resolvedModel, profile.userIntent, fingerprint,
	}
	fields = append(fields, upstream.GetAllBaseURLs()...)
	// 多图观察可能包含比较结论；图片编号、顺序或同批图片改变后必须重新分析。
	batchFields := make([]string, 0, len(images)*2)
	for _, image := range images {
		batchFields = append(batchFields, image.id, image.fingerprint)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%q\n%q", fields, batchFields)))
	return hex.EncodeToString(sum[:])
}

func memoryCacheKey(conversationID string, key string) string {
	return conversationID + "\x00" + key
}

// loadCache 由占位持有者读取持久层；占位必须先于 I/O 建立，才能识别清理前的回填。
func loadCache(ctx context.Context, store visionCacheStore, conversationID string, image visionImage) (string, bool, error) {
	if store == nil {
		return "", false, nil
	}
	result, ok, err := store.LoadConversationImageUnderstanding(ctx, conversationID, image.cacheKey)
	if err != nil || !ok {
		return result, ok, err
	}
	storeMemoryCache(image.memoryKey, image.call, result)
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

func finishAnalysis(key string, call *visionInflightCall, result visionAnalysisResult) {
	if call == nil {
		return
	}
	call.once.Do(func() {
		visionCache.Lock()
		call.visionAnalysisResult = result
		if visionCache.inflight[key] == call {
			delete(visionCache.inflight, key)
		}
		close(call.done)
		visionCache.Unlock()
	})
}

func failPendingAnalyses(images []visionImage, result visionAnalysisResult) {
	for _, image := range images {
		finishAnalysis(image.memoryKey, image.call, result)
	}
}

func storeCache(ctx context.Context, store visionCacheStore, conversationID string, image visionImage, result string) error {
	if store != nil {
		if err := store.SaveConversationImageUnderstanding(ctx, conversationID, image.cacheKey, result); err != nil {
			return err
		}
	}
	storeMemoryCache(image.memoryKey, image.call, result)
	return nil
}

// ClearCache 清空进程内缓存并使在途占位失效；持久化记录由对话删除逻辑清理。
func ClearCache() {
	visionCache.Lock()
	clear(visionCache.items)
	clear(visionCache.inflight)
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
	for key := range visionCache.inflight {
		if strings.HasPrefix(key, prefix) {
			delete(visionCache.inflight, key)
		}
	}
	visionCache.Unlock()
}

func storeMemoryCache(key string, call *visionInflightCall, result string) {
	visionCache.Lock()
	defer visionCache.Unlock()
	// 占位身份就是本次分析的失效标记；清理后旧调用仍可收尾，但不能回填或覆盖新结果。
	if call == nil || visionCache.inflight[key] != call {
		return
	}
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

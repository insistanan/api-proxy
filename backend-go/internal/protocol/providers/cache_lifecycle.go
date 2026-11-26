package providers

import (
	"strings"

	"insistanan/api-proxy/internal/dialogue/conversation"
	"insistanan/api-proxy/internal/dialogue/session"
)

// BindConversationCaches 在装配时登记一次；HTTP 删除、TTL 及停机共享失效入口。
func BindConversationCaches(registry *conversation.Registry) {
	registry.RegisterCacheInvalidator("protocol", func(id string) {
		if id == "" {
			session.DefaultResponseChainManager().ClearAll()
		} else {
			session.DefaultResponseChainManager().Clear(id)
		}
		defaultGeminiShadowStore.clearConversation(id)
		reasoningCache.clearConversation(id)
	})
}

func (s *geminiShadowStore) clearConversation(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.order[:0]
	for _, key := range s.order {
		if id == "" || key.SessionID == id {
			s.usedBytes -= shadowBytes(key, s.sessions[key])
			delete(s.sessions, key)
		} else {
			kept = append(kept, key)
		}
	}
	s.order = kept
}

func (c *reasoningContentCache) clearConversation(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := reasoningFingerprint(id) + ":"
	kept := c.order[:0]
	for _, key := range c.order {
		if id == "" || strings.HasPrefix(key, prefix) {
			c.usedBytes -= len(key) + len(c.entries[key])
			delete(c.entries, key)
		} else {
			kept = append(kept, key)
		}
	}
	c.order = kept
}

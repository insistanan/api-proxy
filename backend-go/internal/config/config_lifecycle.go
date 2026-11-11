package config

import (
	"fmt"
	"log"
	"time"
)

func (cm *ConfigManager) DuplicateChannel(kind string, index int) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(kind)
	if err != nil {
		return err
	}
	*cs.upstreams, err = insertClonedUpstream(*cs.upstreams, index, time.Now())
	if err != nil {
		return err
	}
	return cm.saveConfigLocked(candidate)
}

func (cm *ConfigManager) TidyProblemChannels(kind string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	candidate := cloneConfig(cm.config)
	cs, err := candidate.channelSetFor(kind)
	if err != nil {
		return err
	}
	if !reorderProblemChannelsStable(*cs.upstreams) {
		return nil
	}
	return cm.saveConfigLocked(candidate)
}

func (cm *ConfigManager) startChannelLifecycleWorker() {
	defer cm.workers.Done()
	ticker := time.NewTicker(channelLifecycleTick)
	defer ticker.Stop()

	lastDeprecatedCleanup := time.Now()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			cleanupDeprecated := now.Sub(lastDeprecatedCleanup) >= deprecatedCleanupInterval
			if err := cm.runChannelLifecycle(now, cleanupDeprecated); err != nil {
				log.Printf("[Config-Lifecycle] 警告: 渠道生命周期维护失败: %v", err)
			}
			if cleanupDeprecated {
				lastDeprecatedCleanup = now
			}
		case <-cm.stopChan:
			return
		}
	}
}

func (cm *ConfigManager) runChannelLifecycle(now time.Time, cleanupDeprecated bool) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	candidate := cloneConfig(cm.config)
	changed := false
	var listChanged bool
	candidate.Upstream, listChanged = applyLifecycleToUpstreams(candidate.Upstream, now, cleanupDeprecated, "Messages")
	changed = changed || listChanged
	candidate.ResponsesUpstream, listChanged = applyLifecycleToUpstreams(candidate.ResponsesUpstream, now, cleanupDeprecated, "Responses")
	changed = changed || listChanged
	candidate.GeminiUpstream, listChanged = applyLifecycleToUpstreams(candidate.GeminiUpstream, now, cleanupDeprecated, "Gemini")
	changed = changed || listChanged
	candidate.ChatUpstream, listChanged = applyLifecycleToUpstreams(candidate.ChatUpstream, now, cleanupDeprecated, "Chat")
	changed = changed || listChanged
	candidate.ImagesUpstream, listChanged = applyLifecycleToUpstreams(candidate.ImagesUpstream, now, cleanupDeprecated, "Images")
	changed = changed || listChanged
	candidate.EmbeddingsUpstream, listChanged = applyLifecycleToUpstreams(candidate.EmbeddingsUpstream, now, cleanupDeprecated, "Embeddings")
	changed = changed || listChanged
	candidate.DecisionsUpstream, listChanged = applyLifecycleToUpstreams(candidate.DecisionsUpstream, now, cleanupDeprecated, "Decisions")
	changed = changed || listChanged

	if !changed {
		return nil
	}
	if err := cm.saveConfigLocked(candidate); err != nil {
		return fmt.Errorf("保存生命周期维护结果失败: %w", err)
	}
	return nil
}

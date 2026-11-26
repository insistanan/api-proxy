package config

import (
	"errors"
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
	if err := rejectBuiltinChannel(*cs.upstreams, index); err != nil {
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
	var blocked []error
	for _, set := range candidate.channelSets() {
		for index := range *set.upstreams {
			before := (*set.upstreams)[index]
			next, itemChanged := applyChannelLifecycle(before, now, cleanupDeprecated)
			if !itemChanged {
				continue
			}
			(*set.upstreams)[index] = next
			err := validateConfig(&candidate)
			if err == nil && cm.validateResources != nil {
				err = cm.validateResources(cm.config, candidate)
			}
			if err != nil {
				(*set.upstreams)[index] = before
				blocked = append(blocked, fmt.Errorf("保留受引用约束的渠道 %s/%s: %w", set.kind, before.ID, err))
				continue
			}
			if channelStatusPool(&before) != channelStatusPool(&next) {
				moveChannelToStatusPoolTail(*set.upstreams, index)
			}
			changed = true
		}
	}

	if !changed {
		return errors.Join(blocked...)
	}
	if err := cm.saveConfigLocked(candidate); err != nil {
		return fmt.Errorf("保存生命周期维护结果失败: %w", err)
	}
	return errors.Join(blocked...)
}

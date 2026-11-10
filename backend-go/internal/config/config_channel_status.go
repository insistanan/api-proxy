// 渠道顺序与生命周期：状态分组、促销和优先级调整。
package config

import (
	"fmt"
	"log"
	"sort"
	"time"
)

// reorderOp 重新排序渠道优先级（调用方必须持锁）
func reorderOp(upstreams []UpstreamConfig, order []int, label string) error {
	if len(order) == 0 {
		return fmt.Errorf("排序数组不能为空")
	}
	seen := make(map[int]bool)
	for _, idx := range order {
		if idx < 0 || idx >= len(upstreams) {
			return fmt.Errorf("无效的渠道索引: %d", idx)
		}
		if seen[idx] {
			return fmt.Errorf("重复的渠道索引: %d", idx)
		}
		seen[idx] = true
	}
	// UI 仅传入故障转移序列。将未提交渠道按当前顺序补齐，保证全量优先级连续且唯一。
	allOrder := make([]int, 0, len(upstreams))
	allOrder = append(allOrder, order...)
	remaining := make([]int, 0, len(upstreams)-len(order))
	for index := range upstreams {
		if !seen[index] {
			remaining = append(remaining, index)
		}
	}
	sort.SliceStable(remaining, func(i, j int) bool {
		left, right := remaining[i], remaining[j]
		leftPriority := GetChannelPriority(&upstreams[left], left)
		rightPriority := GetChannelPriority(&upstreams[right], right)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return left < right
	})
	allOrder = append(allOrder, remaining...)
	poolPriorities := make(map[string]int)
	for _, idx := range allOrder {
		scope := upstreamPriorityScope(&upstreams[idx])
		poolPriorities[scope]++
		upstreams[idx].Priority = poolPriorities[scope]
	}
	log.Printf("[Config-Reorder] 已更新 %s 渠道优先级顺序 (%d 个显式渠道，%d 个总渠道)", label, len(order), len(allOrder))
	return nil
}

func moveChannelToStatusPoolTail(upstreams []UpstreamConfig, targetIndex int) {
	ordered := make([]int, 0, len(upstreams)-1)
	for index := range upstreams {
		if index != targetIndex && upstreamPriorityScope(&upstreams[index]) == upstreamPriorityScope(&upstreams[targetIndex]) {
			ordered = append(ordered, index)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		leftPriority := GetChannelPriority(&upstreams[left], left)
		rightPriority := GetChannelPriority(&upstreams[right], right)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return left < right
	})

	active := make([]int, 0, len(ordered)+1)
	suspended := make([]int, 0, len(ordered)+1)
	inactive := make([]int, 0, len(ordered)+1)
	for _, index := range ordered {
		switch channelStatusPool(&upstreams[index]) {
		case 0:
			active = append(active, index)
		case 1:
			suspended = append(suspended, index)
		default:
			inactive = append(inactive, index)
		}
	}
	switch channelStatusPool(&upstreams[targetIndex]) {
	case 0:
		active = append(active, targetIndex)
	case 1:
		suspended = append(suspended, targetIndex)
	default:
		inactive = append(inactive, targetIndex)
	}
	ordered = append(active, suspended...)
	ordered = append(ordered, inactive...)
	for priority, index := range ordered {
		upstreams[index].Priority = priority + 1
	}
}

func channelStatusPool(upstream *UpstreamConfig) int {
	switch GetChannelStatus(upstream) {
	case ChannelStatusSuspended:
		return 1
	case ChannelStatusDisabled, ChannelStatusDeprecated, ChannelStatusDeleted:
		return 2
	default:
		return 0
	}
}

// setStatusOp 设置渠道状态（调用方必须持锁）
func setStatusOp(upstreams []UpstreamConfig, index int, status string, label string) error {
	if index < 0 || index >= len(upstreams) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}
	previousPool := channelStatusPool(&upstreams[index])
	if err := setUpstreamStatus(&upstreams[index], status, time.Now()); err != nil {
		return err
	}
	if previousPool != channelStatusPool(&upstreams[index]) {
		moveChannelToStatusPoolTail(upstreams, index)
	}
	finalStatus := upstreams[index].Status
	if finalStatus == "suspended" || finalStatus == "deprecated" {
		log.Printf("[Config-Status] 已清除渠道 [%d] %s 的促销期", index, upstreams[index].Name)
	}
	log.Printf("[Config-Status] 已设置 %s 渠道 [%d] %s 状态为: %s", label, index, upstreams[index].Name, finalStatus)
	return nil
}

// setPromotionOp 设置渠道促销期（调用方必须持锁）
func setPromotionOp(upstreams []UpstreamConfig, index int, duration time.Duration, count int, label string) error {
	if index < 0 || index >= len(upstreams) {
		return fmt.Errorf("无效的上游索引: %d", index)
	}
	upstream := &upstreams[index]
	if duration < 0 || count < 0 {
		return fmt.Errorf("促销时长和次数不能为负数")
	}
	upstream.PromotionUntil = nil
	upstream.PromotionCount = count
	if duration > 0 {
		until := time.Now().Add(duration)
		upstream.PromotionUntil = &until
	}
	if duration > 0 || count > 0 {
		log.Printf("[Config-Promotion] 已设置 %s 渠道 [%d] %s 促销限制: 时长=%v, 次数=%d", label, index, upstream.Name, duration, count)
	} else {
		log.Printf("[Config-Promotion] 已清除 %s 渠道 [%d] %s 的促销期", label, index, upstream.Name)
	}
	return nil
}

// consumePromotionOp 消费促销计数（调用方必须持锁），返回是否需要保存
func consumePromotionOp(upstreams []UpstreamConfig, index int, label string) bool {
	if index < 0 || index >= len(upstreams) {
		return false
	}
	upstream := &upstreams[index]
	if upstream.PromotionCount <= 0 || !IsChannelInPromotion(upstream) {
		return false
	}
	upstream.PromotionCount--
	if upstream.PromotionCount == 0 {
		upstream.PromotionUntil = nil
		log.Printf("[Config-Promotion] %s渠道 [%d] %s 促销次数已用完，自动清除促销状态", label, index, upstream.Name)
	}
	return true
}

// getPromotedOp 获取处于促销期的渠道索引
func getPromotedOp(upstreams []UpstreamConfig) (int, bool) {
	for i := range upstreams {
		if IsChannelInPromotion(&upstreams[i]) {
			return i, true
		}
	}
	return -1, false
}

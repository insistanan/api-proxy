package config

import (
	"time"
)

func (cm *ConfigManager) SetChatChannelPromotion(index int, duration time.Duration, count int) error {
	return cm.SetChannelPromotionFor("chat", index, duration, count)
}

package assistant

import (
	"fmt"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/config"
)

// 配置知识手册 + 当前配置摘要 + 行动协议，拼装为系统提示词。

// manualText 是项目配置知识手册。与 docs/ 的同步责任：本手册只覆盖
// 用户配置时需要理解的概念与决策（协议选型、分组/兜底/映射语义），
// 内部实现细节以 docs/flows.md 为准，手册不复述。
const manualText = `# 项目配置手册（精简版）

## 协议（五类渠道池，互相独立）
本代理对外暴露五个入口，每类入口有自己的渠道池与分组：
- messages（/v1/messages，Claude 协议）：Claude Code 等 Anthropic 系客户端使用。
- responses（/v1/responses，OpenAI Responses 协议）：Codex CLI 等 OpenAI 系客户端使用。
- chat（/v1/chat/completions，OpenAI Chat 协议）：通用 OpenAI 兼容客户端使用。
- gemini（/v1beta，Gemini 原生协议）：Gemini CLI 等使用。
- images（图片生成）：不参与对话调度。

## 协议转换（serviceType 决定上游真实协议）
渠道所在池（kind）决定客户端用什么协议进来；渠道的 serviceType 决定发往上游时转换成什么协议：
- 例：messages 池里的渠道 serviceType=openai → Claude Code 发 Claude 请求，代理转换为 OpenAI Chat 发上游。
- serviceType 取值：claude / openai / gemini / responses。
- 若上游本身就是目标协议（如 OpenAI 官方），kind 与 serviceType 同名即可透传，无转换损耗。

## 渠道字段速查
- baseUrl：上游地址（支持 # 后缀跳过版本前缀拼接；多 BaseURL failover 可配 baseUrls）。
- defaultModel：上游默认模型。
- modelMapping：源模型 → 目标模型列表（按序尝试），用于客户端请求的模型名映射到上游真实模型；"*" 通配全部。
- visionCapable：渠道原生支持图片理解。
- visionLayerEnabled + visionLayerChannelId：为不支持图片的渠道指定图片理解渠道（旁路调用另一渠道解析图片后回填文本）。

## 分组（Pool）与兜底
- 每类协议的渠道池可划分为多个分组，分组按 modelMatcher（模型名匹配规则）路由请求。
- 未命中任何分组时进入默认分组（兜底）；分组内按优先级排序尝试，渠道失败后依次故障转移。
- 兜底分组 = 没有归属分组的渠道集合，作为最后的保险。

## 配置建议
- 想让 Claude Code 用 OpenAI 渠道：在 messages 池建渠道，serviceType=openai，defaultModel 填上游模型。
- 想按模型分流（如 codex 请求走 A 渠道、其他走 B）：建两个分组，modelMatcher 分别写模型名规则，把渠道挂进对应分组。
- 模型名不匹配时：用 modelMapping 把客户端模型名映射到上游真实模型。`

// actionProtocolText 是行动协议说明。围栏用变量拼接（raw string 里无法写字面反引号）。
var actionProtocolText = strings.Join([]string{
	"# 行动协议",
	"你可以在回复中嵌入行动块，代理会替用户执行配置变更。行动块格式（必须严格遵循）：",
	"",
	"```assistant-action",
	`{"action":"create_channel","kind":"messages","name":"渠道名","baseUrl":"https://api.example.com","serviceType":"openai","defaultModel":"gpt-5","poolName":"分组名"}`,
	"```",
	"",
	"可用行动（白名单）：",
	"1. create_channel：新建渠道。字段：kind（messages/responses/chat/gemini）、name、baseUrl、serviceType、defaultModel、poolName（可选，分组须已存在）、visionCapable（可选）、visionLayerEnabled / visionLayerChannelId / visionLayerModel（可选）。注意：不设 API Key——新建后提醒用户在渠道页填写。",
	"2. create_pool：新建分组。字段：kind、name、modelMatcher（模型名匹配规则，可空）。",
	"3. set_model_mapping：设置渠道模型映射。字段：kind、channelId（稳定渠道 ID，见当前配置摘要）、modelMapping（对象：源模型 → 目标模型数组）。",
	"4. update_channel：更新渠道。字段：kind、channelId、baseUrl / defaultModel / poolName / visionCapable / visionLayerEnabled / visionLayerChannelId / visionLayerModel（只传要改的）。绝不携带任何 API Key 字段——Key 永不由助手触碰。",
	"",
	"规则：",
	"- 行动块之外用自然语言向用户解释做了什么、为什么。",
	"- 需要先确认信息（如上游协议、模型名）时，先提问再行动。",
	"- 不确定时不要行动，解释清楚让用户手动配置。",
	"- 当前配置摘要中的 API Key 已掩码，channelId 以摘要中的 ID 为准。",
}, "\n")

// buildConfigSummary 生成当前配置的动态摘要。API Key 一律掩码，
// 绝不把明文 Key 放进提示词（发送给外部大模型）。
func buildConfigSummary(cfgManager *config.ConfigManager) string {
	cfg := cfgManager.GetConfig()
	var builder strings.Builder
	builder.WriteString("# 当前配置摘要\n")
	kinds := []struct {
		label     string
		upstreams []config.UpstreamConfig
		pools     []config.ChannelPool
	}{
		{"messages", cfg.Upstream, cfgManager.GetChannelPools("messages")},
		{"responses", cfg.ResponsesUpstream, cfgManager.GetChannelPools("responses")},
		{"chat", cfg.ChatUpstream, cfgManager.GetChannelPools("chat")},
		{"gemini", cfg.GeminiUpstream, cfgManager.GetChannelPools("gemini")},
	}
	for _, entry := range kinds {
		builder.WriteString(fmt.Sprintf("\n## %s 渠道（%d 个）\n", entry.label, len(entry.upstreams)))
		if len(entry.pools) > 0 {
			builder.WriteString("分组：")
			for _, pool := range entry.pools {
				matcher := pool.ModelMatcher
				if matcher == "" {
					matcher = "(兜底)"
				}
				builder.WriteString(fmt.Sprintf("%s[%s] ", pool.Name, matcher))
			}
			builder.WriteString("\n")
		}
		if len(entry.upstreams) == 0 {
			builder.WriteString("（无渠道）\n")
			continue
		}
		for index := range entry.upstreams {
			upstream := entry.upstreams[index]
			poolNote := "兜底分组"
			if upstream.PoolID != "" {
				poolNote = "分组ID=" + upstream.PoolID
			}
			keyNote := fmt.Sprintf("%d 个 Key（已隐藏）", len(upstream.APIKeys))
			builder.WriteString(fmt.Sprintf("- ID=%s | %s | serviceType=%s | %s | %s | 默认模型=%s",
				upstream.ID, upstream.Name, upstream.ServiceType, upstream.BaseURL, keyNote, upstream.DefaultModel))
			if upstream.Status != "" && upstream.Status != config.ChannelStatusActive {
				builder.WriteString(" | 状态=" + upstream.Status)
			}
			if len(upstream.ModelMapping) > 0 {
				builder.WriteString(" | 有模型映射")
			}
			builder.WriteString(fmt.Sprintf(" | %s\n", poolNote))
		}
	}
	return builder.String()
}

// buildSystemPrompt 拼装完整系统提示词。
func buildSystemPrompt(cfgManager *config.ConfigManager) string {
	return strings.Join([]string{
		"你是本代理管理界面的 AI 配置助手。用简体中文回答。你的职责：",
		"1. 解答配置问题（协议、协议转换、分组、兜底、模型映射、图片理解等概念）；",
		"2. 按用户意图代为执行配置变更（通过行动协议，见下文）。",
		"回答要具体、贴合当前配置，不编造字段。",
		"",
		manualText,
		"",
		buildConfigSummary(cfgManager),
		"",
		actionProtocolText,
	}, "\n")
}

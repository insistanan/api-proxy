package utils

// 上游 usage 合理性校验。
//
// 背景：代理已有完整的「语义转换」框架（各家 usage 口径互转，见 converters / providers），
// 但没有任何一条路径校验过「上游回报的数字与代理实际发出去的请求是否对得上」。
// 实测中转渠道会双向乱报，同一分钟内相邻两次请求：
//
//	448KB / 340 条消息  →  上游报 input_tokens=26032   （少报约 5 倍）
//	 24KB /   2 条消息  →  上游报 input_tokens=209736  （多报约 30 倍）
//
// 客户端全都拿这个数字决定何时压缩上下文（Claude Code 约 95% 容量、Codex
// model_auto_compact_token_limit、OpenCode isOverflow、Cursor 200K），所以假值会直接
// 表现为「压缩后立刻又要压缩」（多报）或「撞满上限也不压缩」（少报）。
//
// Anthropic 官方文档亦记载过同类现象：cache_read_input_tokens 可能包含多次内部调用的
// 累积读取而非真实上下文，导致 SDK 过早触发压缩，官方建议是另行测量真实上下文长度。
// 本文件即扮演这个「另行测量」的角色——用本地估算做交叉验证，代替额外的 token 计数请求。

const (
	// usageSanityRatio 触发校正所需的偏差倍数。
	// 本地估算是字符数近似（CJK 1.5、其他 3.5 字符/token），对正常上游有 ±30% 误差，
	// 2 倍留足空间：正常渠道永远够不到这条线，只有数量级级别的错报才会被判定。
	usageSanityRatio = 2

	// usageSanityMinTokens 介入所需的最小量级。
	// 只处理「长上下文被错报」这一类——小对话本来就不会触发任何客户端的压缩阈值，
	// 错报也无害，不值得为它承担估算误差的风险。
	usageSanityMinTokens = 20000

	// maxSafeEstimatedInputTokens 只允许小请求在“上游完全没有 usage”时使用本地估算。
	// 长上下文通常由客户端完整重发，直接把整个请求体估算值写入客户端 usage 会导致
	// 压缩后仍被判定为满载，形成 compact thrash。
	maxSafeEstimatedInputTokens = 8000

	// UsageSanityMinTokensForTest 导出的量级门槛，供测试与调用方对齐（校验逻辑只读私有常量）。
	UsageSanityMinTokensForTest = usageSanityMinTokens
)

// SafeEstimatedInputTokens 返回适合填补客户端 usage 的本地估算值。
// 超过上限返回 0，表示宁可保留缺失值，也不把长上下文的整包估算冒充精确 usage。
func SafeEstimatedInputTokens(estimated int) int {
	if estimated <= 0 || estimated > maxSafeEstimatedInputTokens {
		return 0
	}
	return estimated
}

// AnthropicCachedInputTokens 返回 Anthropic usage 中需要与 input_tokens 相加的缓存量。
// cache_creation_input_tokens 是缓存创建总量；5m/1h 字段只是总量的 TTL 明细，不能
// 在总量存在时再次相加。部分兼容上游只返回 TTL 明细，此时才用两档之和补足总量。
func AnthropicCachedInputTokens(cacheRead, cacheCreation, cacheCreation5m, cacheCreation1h int) int {
	if cacheRead < 0 {
		cacheRead = 0
	}
	if cacheCreation < 0 {
		cacheCreation = 0
	}
	if cacheCreation5m < 0 {
		cacheCreation5m = 0
	}
	if cacheCreation1h < 0 {
		cacheCreation1h = 0
	}

	cacheCreationTotal := cacheCreation
	if cacheCreationTotal == 0 {
		cacheCreationTotal = saturatingAdd(cacheCreation5m, cacheCreation1h)
	}
	return saturatingAdd(cacheRead, cacheCreationTotal)
}

// AnthropicUsageCorrection 描述只用于客户端出口的 Anthropic usage 校正结果。
// ClearCache 表示上游缓存量本身已大于可信的整个请求规模，调用方必须同时清除
// cache_read/cache_creation 及其明细，不能只改 input_tokens 后留下仍然虚假的总量。
type AnthropicUsageCorrection struct {
	InputTokens int
	ClearCache  bool
}

// SanityCheckedAnthropicUsage 校验完整的 Anthropic 输入 usage 元组。
// 正常情况下保留上游缓存字段，只重建 uncached input_tokens；只有缓存量本身已经
// 与请求规模数量级冲突时，才清除客户端副本中的缓存字段，并用估算总量作为 input。
func SanityCheckedAnthropicUsage(
	estimatedTotal int,
	upstreamInput int,
	cacheRead int,
	cacheCreation int,
	cacheCreation5m int,
	cacheCreation1h int,
) (AnthropicUsageCorrection, bool) {
	return SanityCheckedAnthropicUsageForRequest(
		nil,
		estimatedTotal,
		upstreamInput,
		cacheRead,
		cacheCreation,
		cacheCreation5m,
		cacheCreation1h,
	)
}

// SanityCheckedAnthropicUsageForRequest 与 SanityCheckedAnthropicUsage 相同，
// 但请求含不透明媒体时禁止用本地估算覆盖上游 usage。
func SanityCheckedAnthropicUsageForRequest(
	requestBody []byte,
	estimatedTotal int,
	upstreamInput int,
	cacheRead int,
	cacheCreation int,
	cacheCreation5m int,
	cacheCreation1h int,
) (AnthropicUsageCorrection, bool) {
	if RequestContainsOpaqueMedia(requestBody) {
		return AnthropicUsageCorrection{}, false
	}
	upstreamCached := AnthropicCachedInputTokens(
		cacheRead,
		cacheCreation,
		cacheCreation5m,
		cacheCreation1h,
	)
	correctedInput, needsCorrection := SanityCheckedInputTokens(
		estimatedTotal,
		upstreamInput,
		upstreamCached,
	)
	if !needsCorrection {
		return AnthropicUsageCorrection{}, false
	}

	if upstreamCached > estimatedTotal {
		return AnthropicUsageCorrection{
			InputTokens: estimatedTotal,
			ClearCache:  true,
		}, true
	}

	return AnthropicUsageCorrection{InputTokens: correctedInput}, true
}

// SanityCheckedInputTokens 交叉验证上游回报的上下文规模，返回应当下发给客户端的 input_tokens。
//
// estimatedTotal 是代理本地估算的真实上下文总规模（含缓存部分，即整个请求体的量）。
// upstreamInput 是上游回报的 input_tokens；0/1 且没有单独缓存量时视为缺失占位，
// 不使用整包本地估算填回长请求，避免与 SafeEstimatedInputTokens 的保护互相抵消。
// upstreamCached 是上游**单独回报、且不包含在 input_tokens 内**的缓存 token 总量：
//   - Anthropic 语义（input_tokens 是未缓存余量）：传 cache_read + cache_creation 总量；
//     5m/1h 明细仅在总字段缺失时求和，可直接使用 AnthropicCachedInputTokens。
//   - OpenAI 语义（cached_tokens 已含在 input_tokens 内）：传 0，否则会把缓存算两遍。
//
// 第二个返回值为 false 表示上游数据可信，调用方必须原样保留上游值。
// 返回值按 Anthropic 语义给出（不含缓存部分），使 input + cached 之和回到真实规模，
// 不与调用方保留的缓存字段重叠。
func SanityCheckedInputTokens(estimatedTotal, upstreamInput, upstreamCached int) (int, bool) {
	return SanityCheckedInputTokensForRequest(nil, estimatedTotal, upstreamInput, upstreamCached)
}

// SanityCheckedInputTokensForRequest 与 SanityCheckedInputTokens 相同，
// 但请求含图片/PDF/文件时直接信任上游 usage，避免把 base64 估算当成长文本覆盖客户端。
func SanityCheckedInputTokensForRequest(requestBody []byte, estimatedTotal, upstreamInput, upstreamCached int) (int, bool) {
	if RequestContainsOpaqueMedia(requestBody) {
		return 0, false
	}
	if estimatedTotal <= 0 {
		return 0, false
	}
	if upstreamInput < 0 {
		upstreamInput = 0
	}
	if upstreamCached < 0 {
		upstreamCached = 0
	}
	upstreamTotal := saturatingAdd(upstreamInput, upstreamCached)
	if upstreamInput <= 1 && upstreamCached == 0 {
		return 0, false
	}

	// 量级门槛按双边取：多报场景下真实上下文很小（估算够不到门槛），
	// 但上游报出的假大值本身就是需要拦截的对象，只看估算侧会漏掉整类问题。
	if estimatedTotal < usageSanityMinTokens && upstreamTotal < usageSanityMinTokens {
		return 0, false
	}

	// 双向判定：上游明确少报与上游虚报都会误导客户端的压缩决策，
	// 只挡一个方向等于只修一半。完全缺失/零值交给各协议自己的安全补全路径。
	underReported := exceedsByRatio(estimatedTotal, upstreamTotal)
	overReported := exceedsByRatio(upstreamTotal, estimatedTotal)
	if !underReported && !overReported {
		return 0, false
	}

	corrected := estimatedTotal - upstreamCached
	if corrected < 0 {
		corrected = 0
	}
	if corrected == upstreamInput {
		return 0, false
	}
	return corrected, true
}

// exceedsByRatio 判断 larger 是否严格大于 smaller 的 usageSanityRatio 倍，
// 使用除法而不是乘法，避免上游恶意/损坏的大整数让乘法溢出后误判。
func exceedsByRatio(larger, smaller int) bool {
	if larger <= 0 || smaller < 0 {
		return false
	}
	threshold := larger / usageSanityRatio
	if larger%usageSanityRatio != 0 {
		threshold++
	}
	return smaller < threshold
}

func saturatingAdd(left, right int) int {
	if left <= 0 {
		return right
	}
	if right <= 0 {
		return left
	}
	maxInt := int(^uint(0) >> 1)
	if left > maxInt-right {
		return maxInt
	}
	return left + right
}

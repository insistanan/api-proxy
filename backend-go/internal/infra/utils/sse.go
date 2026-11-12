package utils

import (
	"bytes"
	"strings"
)

// sseDataPrefix 是 SSE data 字段名与冒号。
// 按 W3C server-sent events 规范，冒号后的单个空格是可选的，
// 故前缀判定只到冒号为止，空白交由载荷清理处理。
const sseDataPrefix = "data:"

// SSEDataLinePrefix 是**输出**侧写 data 行时使用的规范前缀，
// 含 SSE 规范允许省略但业界普遍带上的那个空格。
// 判定输入一律用 ParseSSEDataLine，不要拿本常量做 HasPrefix。
const SSEDataLinePrefix = sseDataPrefix + " "

// SSEDoneMarker 是 OpenAI 系流（Chat Completions / Responses）的终止标记载荷。
const SSEDoneMarker = "[DONE]"

// ParseSSEDataLine 解析一行 SSE 文本，返回 data 字段载荷与该行是否为 data 行。
// 五协议流式链路（providers / handlers / converters）判定 data 行的唯一出处。
//
// 规则：
//   - 前缀只匹配 "data:"，冒号后的空格按 SSE 规范视为可选——"data:{...}" 与 "data: {...}" 等价；
//     部分上游不带空格，严格匹配 "data: " 会静默丢弃整行，故一律走本函数；
//   - 载荷做首尾空白清理（含规范允许的那个前导空格），因此 "data: [DONE] " 也能判出终止标记；
//   - 非 data 行（event: / id: / retry: / 注释行 / 空行）返回 ok=false。
//
// 载荷可能是 SSEDoneMarker 或空串；只关心 JSON 载荷的调用点请用 SSEDataJSON。
// 需要区分"终止标记"与"非 data 行"的调用点（例如据此 break 出读流循环）用本函数。
func ParseSSEDataLine(line string) (string, bool) {
	if !strings.HasPrefix(line, sseDataPrefix) {
		return "", false
	}
	return strings.TrimSpace(line[len(sseDataPrefix):]), true
}

// SSEDataJSON 在 ParseSSEDataLine 之上滤掉终止标记与空载荷，只返回待解析的 JSON 文本。
// 适用于"拿到 JSON 才处理，否则跳过本行"的绝大多数调用点。
func SSEDataJSON(line string) (string, bool) {
	payload, ok := ParseSSEDataLine(line)
	if !ok || payload == "" || payload == SSEDoneMarker {
		return "", false
	}
	return payload, true
}

// ParseSSEDataLineBytes 是 ParseSSEDataLine 的 []byte 版本，
// 供逐行处理原始字节的热路径使用，避免每行一次字符串拷贝。
// 语义与 ParseSSEDataLine 完全一致。
func ParseSSEDataLineBytes(line []byte) ([]byte, bool) {
	if !bytes.HasPrefix(line, []byte(sseDataPrefix)) {
		return nil, false
	}
	return bytes.TrimSpace(line[len(sseDataPrefix):]), true
}

// RewriteSSEDataLines 逐行重写一个 SSE 事件的 data 载荷，是"拆行改载荷再拼回"这类
// 事件改写（token 修补 / usage 注入 / id-model 改写 / 整体替换）的唯一骨架。
//
// 保真契约：非 data 行、以及 rewrite 返回 changed=false 的 data 行，**原样**写回
// （保留上游原始的 `data:` / `data: ` 写法）；行分隔符按 strings.Split 的逆运算补，
// 因此无任何一行被改写时返回值与入参逐字节相同——不会多出或吞掉尾部空行。
// 各调用点此前自行 `WriteString(line); WriteString("\n")` 的写法会给
// 以 "\n\n" 结尾的事件多补一个换行，故一律走本函数。
//
// rewrite 收到的载荷已按 ParseSSEDataLine 清理首尾空白；它对每个 data 行都会被调用，
// 包括 SSEDoneMarker 这类非 JSON 载荷——只处理 JSON 的调用点靠 json.Unmarshal 失败
// 返回 changed=false 即可，本函数不替调用方做隐式过滤。
// 被改写的行统一写 SSEDataLinePrefix 规范前缀。
//
// 第二个返回值指示是否有任意一行被改写，供调用方决定是否回退到原事件。
func RewriteSSEDataLines(event string, rewrite func(payload string) (string, bool)) (string, bool) {
	lines := strings.Split(event, "\n")

	var result strings.Builder
	result.Grow(len(event))
	anyChanged := false

	for i, line := range lines {
		if i > 0 {
			result.WriteString("\n")
		}

		payload, isData := ParseSSEDataLine(line)
		if !isData {
			result.WriteString(line)
			continue
		}

		newPayload, changed := rewrite(payload)
		if !changed {
			result.WriteString(line)
			continue
		}

		anyChanged = true
		result.WriteString(SSEDataLinePrefix)
		result.WriteString(newPayload)
	}

	return result.String(), anyChanged
}

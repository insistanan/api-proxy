package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	claudeCodeFingerprintSalt = "59cf53e54c78"
	claudeCodeBillingPrefix   = "x-anthropic-billing-header:"
)

var (
	claudeCodeUserAgentPattern    = regexp.MustCompile(`(?i)^claude-cli/\d+\.\d+\.\d+`)
	legacyClaudeCodeUserIDPattern = regexp.MustCompile(
		`^user_[a-fA-F0-9]{64}_account_[a-fA-F0-9-]*_session_[a-fA-F0-9-]{36}$`,
	)
	claudeCodeRequiredBetas = []string{
		"claude-code-20250219",
		"oauth-2025-04-20",
		"interleaved-thinking-2025-05-14",
	}
)

type claudeCodeMetadataUserID struct {
	DeviceID    string `json:"device_id"`
	AccountUUID string `json:"account_uuid"`
	SessionID   string `json:"session_id"`
}

func isClaudeCodeUserAgent(userAgent string) bool {
	return claudeCodeUserAgentPattern.MatchString(strings.TrimSpace(userAgent))
}

func mergeClaudeCodeBetaHeaders(existingHeaderValues []string) string {
	mergedBetaValues := make([]string, 0, len(existingHeaderValues)+len(claudeCodeRequiredBetas))
	seenBetaValues := make(map[string]struct{})

	appendUniqueBetaValues := func(headerValue string) {
		for _, betaValue := range strings.Split(headerValue, ",") {
			trimmedBetaValue := strings.TrimSpace(betaValue)
			if trimmedBetaValue == "" {
				continue
			}

			normalizedBetaValue := strings.ToLower(trimmedBetaValue)
			if _, alreadyAdded := seenBetaValues[normalizedBetaValue]; alreadyAdded {
				continue
			}

			seenBetaValues[normalizedBetaValue] = struct{}{}
			mergedBetaValues = append(mergedBetaValues, trimmedBetaValue)
		}
	}

	for _, existingHeaderValue := range existingHeaderValues {
		appendUniqueBetaValues(existingHeaderValue)
	}
	for _, requiredBetaValue := range claudeCodeRequiredBetas {
		appendUniqueBetaValues(requiredBetaValue)
	}

	return strings.Join(mergedBetaValues, ",")
}

// ApplyClaudeCodeBodyDisguise 补齐严格 Claude Code 网关用于识别客户端的请求体特征。
// 原有 system 和 metadata 内容会被保留，仅在缺少计费标记或合法 user_id 时补充。
func ApplyClaudeCodeBodyDisguise(bodyBytes []byte, sessionID string) []byte {
	if len(bodyBytes) == 0 {
		return bodyBytes
	}

	decoder := json.NewDecoder(strings.NewReader(string(bodyBytes)))
	decoder.UseNumber()

	var requestPayload map[string]interface{}
	if err := decoder.Decode(&requestPayload); err != nil || requestPayload == nil {
		return bodyBytes
	}

	if sessionID == "" {
		sessionID = fallbackDisguiseSessionID
	}

	ensureClaudeCodeBillingSystemBlock(requestPayload)
	ensureClaudeCodeMetadataUserID(requestPayload, sessionID)

	modifiedBodyBytes, err := MarshalJSONNoEscape(requestPayload)
	if err != nil {
		return bodyBytes
	}

	return modifiedBodyBytes
}

func ensureClaudeCodeBillingSystemBlock(requestPayload map[string]interface{}) {
	existingSystemValue := requestPayload["system"]
	if hasClaudeCodeBillingSystemBlock(existingSystemValue) {
		return
	}

	billingSystemBlock := map[string]interface{}{
		"type": "text",
		"text": buildClaudeCodeBillingText(requestPayload),
	}

	switch typedSystemValue := existingSystemValue.(type) {
	case []interface{}:
		requestPayload["system"] = append([]interface{}{billingSystemBlock}, typedSystemValue...)
	case string:
		systemBlocks := []interface{}{billingSystemBlock}
		if typedSystemValue != "" {
			systemBlocks = append(systemBlocks, map[string]interface{}{
				"type": "text",
				"text": typedSystemValue,
			})
		}
		requestPayload["system"] = systemBlocks
	case nil:
		requestPayload["system"] = []interface{}{billingSystemBlock}
	default:
		// 非标准 system 类型交由上游按原有行为校验，避免伪装逻辑静默丢弃用户内容。
		return
	}
}

func hasClaudeCodeBillingSystemBlock(systemValue interface{}) bool {
	systemBlocks, isSystemBlockList := systemValue.([]interface{})
	if !isSystemBlockList {
		return false
	}

	for _, systemBlockValue := range systemBlocks {
		systemBlock, isSystemBlock := systemBlockValue.(map[string]interface{})
		if !isSystemBlock {
			continue
		}

		textValue, hasTextValue := systemBlock["text"].(string)
		if hasTextValue && strings.HasPrefix(textValue, claudeCodeBillingPrefix) && strings.Contains(textValue, "cc_entrypoint=") {
			return true
		}
	}

	return false
}

func buildClaudeCodeBillingText(requestPayload map[string]interface{}) string {
	fingerprint := computeClaudeCodeFingerprint(requestPayload)
	return fmt.Sprintf(
		"%s cc_version=%s.%s; cc_entrypoint=cli;",
		claudeCodeBillingPrefix,
		claudeCodeDisguiseVersion(),
		fingerprint,
	)
}

func computeClaudeCodeFingerprint(requestPayload map[string]interface{}) string {
	firstUserText := extractFirstClaudeCodeUserText(requestPayload)
	fingerprintCharacters := []byte{'0', '0', '0'}
	characterIndices := []int{4, 7, 20}

	for characterPosition, characterIndex := range characterIndices {
		if characterIndex < len(firstUserText) {
			fingerprintCharacters[characterPosition] = firstUserText[characterIndex]
		}
	}

	fingerprintDigest := sha256.Sum256([]byte(
		claudeCodeFingerprintSalt + string(fingerprintCharacters) + claudeCodeDisguiseVersion(),
	))
	return hex.EncodeToString(fingerprintDigest[:])[:3]
}

func extractFirstClaudeCodeUserText(requestPayload map[string]interface{}) string {
	messages, hasMessages := requestPayload["messages"].([]interface{})
	if !hasMessages {
		return ""
	}

	for _, messageValue := range messages {
		message, isMessage := messageValue.(map[string]interface{})
		if !isMessage || message["role"] != "user" {
			continue
		}

		switch contentValue := message["content"].(type) {
		case string:
			return contentValue
		case []interface{}:
			for _, contentBlockValue := range contentValue {
				contentBlock, isContentBlock := contentBlockValue.(map[string]interface{})
				if !isContentBlock || contentBlock["type"] != "text" {
					continue
				}
				if textValue, hasTextValue := contentBlock["text"].(string); hasTextValue {
					return textValue
				}
			}
		}

		return ""
	}

	return ""
}

func ensureClaudeCodeMetadataUserID(requestPayload map[string]interface{}, sessionID string) {
	metadata, hasMetadata := requestPayload["metadata"].(map[string]interface{})
	if !hasMetadata {
		metadata = make(map[string]interface{})
		requestPayload["metadata"] = metadata
	}

	if existingUserID, hasUserID := metadata["user_id"].(string); hasUserID && isValidClaudeCodeMetadataUserID(existingUserID) {
		return
	}

	deviceIDDigest := sha256.Sum256([]byte("api-proxy:" + sessionID))
	metadataUserIDBytes, err := json.Marshal(claudeCodeMetadataUserID{
		DeviceID:    hex.EncodeToString(deviceIDDigest[:]),
		AccountUUID: "",
		SessionID:   sessionID,
	})
	if err != nil {
		return
	}

	metadata["user_id"] = string(metadataUserIDBytes)
}

func isValidClaudeCodeMetadataUserID(userID string) bool {
	trimmedUserID := strings.TrimSpace(userID)
	if trimmedUserID == "" {
		return false
	}

	if strings.HasPrefix(trimmedUserID, "{") {
		var parsedUserID claudeCodeMetadataUserID
		if err := json.Unmarshal([]byte(trimmedUserID), &parsedUserID); err != nil {
			return false
		}
		return parsedUserID.DeviceID != "" && parsedUserID.SessionID != ""
	}

	return legacyClaudeCodeUserIDPattern.MatchString(trimmedUserID)
}

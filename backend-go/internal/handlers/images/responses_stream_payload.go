package images

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BenedictKing/api-proxy/internal/types"
	"github.com/BenedictKing/api-proxy/internal/utils"
	"github.com/tidwall/gjson"
)

// responsesImageEvent 只保留转换所需的字段视图，不构建包含全部 base64 的对象树。
type responsesImageEvent struct {
	Type         string
	ImageJSON    string
	PartialIndex int64
	OutputFormat string
	Item         gjson.Result
	Response     gjson.Result
	Error        gjson.Result
	Message      string
}

func parseResponsesImageEvent(raw, name string) (responsesImageEvent, error) {
	if !gjson.Valid(raw) {
		return responsesImageEvent{}, fmt.Errorf("Responses 图片事件不是有效的 JSON")
	}
	root := gjson.Parse(raw)
	if !root.IsObject() {
		return responsesImageEvent{}, fmt.Errorf("Responses 图片事件必须是 JSON 对象")
	}
	event := responsesImageEvent{Type: name}
	root.ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "type":
			event.Type = value.Str
		case "partial_image_b64":
			if value.Type == gjson.String && value.Raw != `""` {
				event.ImageJSON = value.Raw
			}
		case "partial_image_index":
			event.PartialIndex = value.Int()
		case "output_format":
			event.OutputFormat = value.Str
		case "item":
			event.Item = value
		case "response":
			event.Response = value
		case "error":
			event.Error = value
		case "message":
			event.Message = value.Str
		}
		return true
	})
	return event, nil
}

func imageItemFromJSON(raw gjson.Result) responsesImageItem {
	var item responsesImageItem
	raw.ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "id":
			item.ID = value.Str
		case "type":
			item.Type = value.Str
		case "status":
			item.Status = value.Str
		case "result":
			if value.Type == gjson.String && value.Raw != `""` {
				item.ResultJSON = value.Raw
			}
		case "revised_prompt":
			item.RevisedPrompt = value.Str
		case "output_format":
			item.OutputFormat = value.Str
		case "background":
			item.Background = value.Str
		case "size":
			item.Size = value.Str
		case "quality":
			item.Quality = value.Str
		case "usage":
			item.Usage = json.RawMessage(value.Raw)
		}
		return true
	})
	return item
}

func imageStreamItemKey(item responsesImageItem) string {
	if item.ID != "" {
		// gjson 的小字段可能是大事件字符串的子串，克隆 ID 后才能及时释放整张图片。
		return "id:" + strings.Clone(item.ID)
	}
	// 缺少 ID 的兼容上游按图片原始 JSON 计算摘要，不把大图片字符串当 map 键。
	digest := sha256.New()
	buffer := utils.GetChunkBuffer32KB()
	defer utils.PutChunkBuffer32KB(buffer)
	remaining := item.ResultJSON
	for len(remaining) > 0 {
		n := copy(buffer, remaining)
		digest.Write(buffer[:n]) // hash.Hash.Write 按契约不会返回错误。
		remaining = remaining[n:]
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

func responsesImageStreamMetadata(raw gjson.Result) (int64, *types.Usage, gjson.Result, error) {
	if !raw.IsObject() {
		return 0, nil, gjson.Result{}, fmt.Errorf("Responses 完成事件缺少 response 对象")
	}
	var created int64
	var usage *types.Usage
	var output gjson.Result
	var resultErr error
	raw.ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "created_at":
			created = value.Int()
		case "status":
			if value.Str != "" && value.Str != "completed" {
				resultErr = fmt.Errorf("Responses 图片请求未完成，状态为 %s", value.Str)
			}
		case "error":
			if value.Type != gjson.Null {
				resultErr = responsesImageError(json.RawMessage(value.Raw))
			}
		case "usage":
			parsed, err := responsesImageUsage(json.RawMessage(value.Raw))
			if err != nil {
				resultErr = fmt.Errorf("解析 Responses 图片用量失败: %w", err)
			} else {
				usage = parsed
			}
		case "output":
			output = value
		}
		return true
	})
	return created, usage, output, resultErr
}

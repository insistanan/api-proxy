package visionlayer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/orchestration/scheduler"
	"insistanan/api-proxy/internal/protocol/visionmedia"
)

func TestPrepareRequestSkipsImagesProtocolImageToImage(t *testing.T) {
	body := []byte(`{"model":"gpt-4.1","input":[{"role":"user","content":[{"type":"input_text","text":"给猫加帽子"},{"type":"input_image","image_url":"https://example.com/cat.png"}]}],"tools":[{"type":"image_generation","model":"gpt-image-2"}]}`)
	req, err := http.NewRequest(http.MethodPost, "https://upstream.example.com/v1/responses", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("创建请求失败: %v", err)
	}
	err = PrepareRequest(nil, nil, nil, nil, scheduler.ChannelKindImages, &config.UpstreamConfig{
		Name: "shuai", ServiceType: "responses",
	}, "gpt-image-2", "", req)
	if err != nil {
		t.Fatalf("Images 图生图不应进入图片理解层: %v", err)
	}
	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("读取请求体失败: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("Images 图生图请求体被改写: %s", got)
	}
}

func TestPrepareRequest_RejectsMalformedJSON(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		contentType string
		body        string
		wantError   string
	}{
		{name: "损坏的正文", contentType: "application/json", body: `{"messages":`, wantError: "请求 JSON 失败"},
		{name: "缺少媒体类型仍校验 JSON", body: `{"messages":`, wantError: "请求 JSON 失败"},
		{name: "多个顶层值", contentType: "application/json; charset=utf-8", body: `{"messages":[]} {}`, wantError: "多个顶层 JSON 值"},
		{name: "尾随垃圾", contentType: "application/vnd.proxy+json", body: `{"messages":[]} invalid`, wantError: "JSON 尾部失败"},
		{name: "非对象", contentType: "application/json", body: `[]`, wantError: "JSON 必须是对象"},
		{name: "无效媒体类型", contentType: `application/json; charset="`, body: `{}`, wantError: "媒体类型失败"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(testCase.body))
			request.Header.Set("Content-Type", testCase.contentType)
			err := PrepareRequest(nil, nil, nil, nil, scheduler.ChannelKindMessages, &config.UpstreamConfig{ServiceType: "claude"}, "test", "", request)
			require.ErrorContains(t, err, testCase.wantError)
			status, code := ErrorResponse(err)
			require.Equal(t, http.StatusInternalServerError, status)
			require.Equal(t, "VISION_LAYER_INTERNAL_ERROR", code)
			body, readErr := io.ReadAll(request.Body)
			require.NoError(t, readErr)
			require.Equal(t, testCase.body, string(body), "失败时不能留下只消费一半的请求体")
		})
	}
}

func TestPrepareRequest_SkipsDeclaredNonJSON(t *testing.T) {
	for _, contentType := range []string{"multipart/form-data; boundary=sample", "text/plain", "application/octet-stream"} {
		t.Run(contentType, func(t *testing.T) {
			const body = "this is not JSON"
			request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
			request.Header.Set("Content-Type", contentType)
			originalBody := request.Body
			require.NoError(t, PrepareRequest(nil, nil, nil, nil, scheduler.ChannelKindMessages, &config.UpstreamConfig{ServiceType: "claude"}, "test", "", request))
			require.True(t, request.Body == originalBody, "明确的非 JSON 正文无需消费或重建")
			actual, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			require.Equal(t, body, string(actual))
		})
	}
}

func TestPrepareRequest_AcceptsCompleteJSONObject(t *testing.T) {
	for _, contentType := range []string{"", "application/json; charset=utf-8", "application/vnd.proxy+json"} {
		t.Run(contentType, func(t *testing.T) {
			const body = "{\"messages\":[]} \r\n\t"
			request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
			request.Header.Set("Content-Type", contentType)
			require.NoError(t, PrepareRequest(nil, nil, nil, nil, scheduler.ChannelKindMessages, &config.UpstreamConfig{ServiceType: "claude"}, "test", "", request))
			actual, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			require.Equal(t, body, string(actual))
		})
	}
}

func TestTransformImages_ResponsesToolOutputsPreserveBusinessFields(t *testing.T) {
	for _, itemType := range []string{"function_call_output", "custom_tool_call_output"} {
		t.Run(itemType, func(t *testing.T) {
			var payload map[string]interface{}
			require.NoError(t, json.Unmarshal([]byte(`{
				"input":[
					{"type":"`+itemType+`","call_id":"call-1","output":[
						{"type":"input_text","text":"工具结果"},
						{"type":"input_image","image_url":"https://example.com/tool.png"}
					]},
					{"type":"function_call","arguments":{"output":[{"type":"input_image","image_url":"https://example.com/business.png"}]}},
					{"type":"message","content":[{"type":"input_text","text":"用户问题"}],"output":[{"type":"input_image","image_url":"https://example.com/extension.png"}]}
				],
				"metadata":{"content":[{"type":"input_image","image_url":"https://example.com/metadata.png"}]},
				"tools":[{"parameters":{"example":{"type":"input_image","image_url":"https://example.com/schema.png"}}}]
			}`), &payload))
			items := payload["input"].([]interface{})
			item := items[0].(map[string]interface{})
			unchanged, err := json.Marshal([]interface{}{items[1:], payload["metadata"], payload["tools"]})
			require.NoError(t, err)
			images, err := collectImagesForUpstream(payload, "responses")
			require.NoError(t, err)
			require.Len(t, images, 1)
			fingerprint := visionmedia.ImageFingerprintForBlock(images[0])
			_, err = transformImagesForUpstream(payload, "responses", map[string]string{fingerprint: "工具截图"})
			require.NoError(t, err)
			require.Equal(t, itemType, item["type"])
			require.Equal(t, "call-1", item["call_id"])
			require.Equal(t, []interface{}{
				map[string]interface{}{"type": "input_text", "text": "工具结果"},
				map[string]interface{}{"type": "input_text", "text": visionResultText("工具截图")},
			}, item["output"], "直接校验协议输出，不能只依赖同一个提取器自证")
			after, err := json.Marshal([]interface{}{items[1:], payload["metadata"], payload["tools"]})
			require.NoError(t, err)
			require.Equal(t, unchanged, after, "工具参数、普通消息扩展字段、元数据和 schema 不属于图片替换边界")
		})
	}
}

func TestTransformImages_ClaudeNestedToolResult(t *testing.T) {
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"text","text":"说明"},{"type":"image","source":{"type":"url","url":"https://example.com/tool.png"}}]}]}]}`), &payload))
	image := map[string]interface{}{"type": "image", "source": map[string]interface{}{"type": "url", "url": "https://example.com/tool.png"}}
	_, err := transformImagesForUpstream(payload, "claude", map[string]string{visionmedia.ImageFingerprintForBlock(image): "工具截图"})
	require.NoError(t, err)
	message := payload["messages"].([]interface{})[0].(map[string]interface{})
	result := message["content"].([]interface{})[0].(map[string]interface{})
	require.Equal(t, "call-1", result["tool_use_id"])
	require.Equal(t, []interface{}{
		map[string]interface{}{"type": "text", "text": "说明"},
		map[string]interface{}{"type": "text", "text": visionResultText("工具截图")},
	}, result["content"])
}

func TestTransformImages_MissingDescriptionDoesNotPartiallyRewrite(t *testing.T) {
	const body = `{"input":[{"type":"message","content":[{"type":"input_image","image_url":"https://example.com/first.png"},{"type":"input_image","image_url":"https://example.com/second.png"}]}]}`
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(body), &payload))
	images, err := collectImagesForUpstream(payload, "responses")
	require.NoError(t, err)
	require.Len(t, images, 2)
	_, err = transformImagesForUpstream(payload, "responses", map[string]string{visionmedia.ImageFingerprintForBlock(images[0]): "只有第一张结果"})
	require.ErrorContains(t, err, "未找到图片指纹对应的理解结果")
	after, err := json.Marshal(payload)
	require.NoError(t, err)
	require.JSONEq(t, body, string(after))
}

func TestReplaceImagesUsesEachImageOwnDescription(t *testing.T) {
	first := map[string]interface{}{
		"type": "image",
		"source": map[string]interface{}{
			"type": "base64",
			"data": "aGVsbG8=",
		},
	}
	second := map[string]interface{}{
		"type": "image",
		"source": map[string]interface{}{
			"type": "base64",
			"data": "d29ybGQ=",
		},
	}
	payload := map[string]interface{}{
		"messages": []interface{}{map[string]interface{}{
			"role":    "user",
			"content": []interface{}{first, second},
		}},
	}
	descriptions := map[string]string{
		visionmedia.ImageFingerprintForBlock(first):  "第一张图片：你好",
		visionmedia.ImageFingerprintForBlock(second): "第二张图片：世界",
	}

	transformed, err := transformImagesForUpstream(payload, "claude", descriptions)
	if err != nil {
		t.Fatalf("transformImagesForUpstream() error = %v", err)
	}

	result := transformed.(map[string]interface{})
	message := result["messages"].([]interface{})[0].(map[string]interface{})
	items := message["content"].([]interface{})
	firstText := items[0].(map[string]interface{})["text"].(string)
	secondText := items[1].(map[string]interface{})["text"].(string)
	if !strings.Contains(firstText, "第一张图片：你好") || strings.Contains(firstText, "第二张图片：世界") {
		t.Fatalf("first image replacement = %q", firstText)
	}
	if !strings.Contains(secondText, "第二张图片：世界") || strings.Contains(secondText, "第一张图片：你好") {
		t.Fatalf("second image replacement = %q", secondText)
	}
}

func TestImageCacheKeyIncludesVisionProfile(t *testing.T) {
	fingerprint := "sha256:example"
	upstream := &config.UpstreamConfig{ID: "vision", ServiceType: "claude", BaseURL: "https://vision.example"}
	profile := resolveVisionAnalysisProfile(strings.Repeat("观察", 310) + "读取 A")
	images := []visionImage{{id: "image_1", fingerprint: fingerprint}, {id: "image_2", fingerprint: "second"}}
	first := buildImageCacheKey(fingerprint, upstream, "vision-model-a", profile, images)
	require.Equal(t, first, buildImageCacheKey(fingerprint, upstream, "vision-model-a", profile, images))
	for _, scenario := range []string{"model", "channel", "endpoint", "protocol", "question", "case", "companion", "order", "id", "batch-size"} {
		t.Run(scenario, func(t *testing.T) {
			changed := upstream.Clone()
			model := "vision-model-a"
			intent := profile
			batch := append([]visionImage(nil), images...)
			switch scenario {
			case "model":
				model = "vision-model-b"
			case "channel":
				changed.ID = "other"
			case "endpoint":
				changed.BaseURL = "https://other.example"
			case "protocol":
				changed.ServiceType = "openai"
			case "question":
				intent = resolveVisionAnalysisProfile(strings.Repeat("观察", 310) + "读取 B")
			case "case":
				intent = resolveVisionAnalysisProfile(strings.Repeat("观察", 310) + "读取 a")
			case "companion":
				batch[1].fingerprint = "other-image"
			case "order":
				batch[0], batch[1] = batch[1], batch[0]
			case "id":
				batch[0].id = "image_11"
			case "batch-size":
				batch = batch[:1]
			}
			require.NotEqual(t, first, buildImageCacheKey(fingerprint, changed, model, intent, batch))
		})
	}
}

func TestResolveVisionAnalysisProfileUsesUserQuestion(t *testing.T) {
	profile := resolveVisionAnalysisProfile("请分析这张截图中的错误和修复线索")
	require.Equal(t, "请分析这张截图中的错误和修复线索", profile.userIntent)
}

func TestExtractUserText_TruncatesAtCharacterBoundary(t *testing.T) {
	for _, input := range []string{strings.Repeat("中", 800), strings.Repeat("😀中", 500), strings.Repeat("a", 2100)} {
		text := extractUserText(input)
		require.True(t, utf8.ValidString(text))
		require.LessOrEqual(t, len(text), 2000)
		require.GreaterOrEqual(t, len(text), 1997)
		require.True(t, strings.HasSuffix(input, text))
	}
	require.Equal(t, "中文😀", extractUserText("中文😀"))
}

func TestTransformImagesForTargetProtocols(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		image       map[string]interface{}
		payload     func(map[string]interface{}) map[string]interface{}
	}{
		{
			name:        "Claude Messages",
			serviceType: "claude",
			image: map[string]interface{}{
				"type": "image",
				"source": map[string]interface{}{
					"type": "base64",
					"data": "aGVsbG8=",
				},
			},
			payload: func(image map[string]interface{}) map[string]interface{} {
				return map[string]interface{}{"messages": []interface{}{map[string]interface{}{
					"role": "user", "content": []interface{}{image},
				}}}
			},
		},
		{
			name:        "OpenAI Chat",
			serviceType: "openai",
			image: map[string]interface{}{
				"type":      "image_url",
				"image_url": map[string]interface{}{"url": "https://example.com/image.png"},
			},
			payload: func(image map[string]interface{}) map[string]interface{} {
				return map[string]interface{}{"messages": []interface{}{map[string]interface{}{
					"role": "user", "content": []interface{}{image},
				}}}
			},
		},
		{
			name:        "OpenAI Responses",
			serviceType: "responses",
			image: map[string]interface{}{
				"type":      "input_image",
				"image_url": "https://example.com/image.png",
			},
			payload: func(image map[string]interface{}) map[string]interface{} {
				return map[string]interface{}{"input": []interface{}{map[string]interface{}{
					"type": "message", "role": "user", "content": []interface{}{image},
				}}}
			},
		},
		{
			name:        "Gemini",
			serviceType: "gemini",
			image: map[string]interface{}{
				"inlineData": map[string]interface{}{"mimeType": "image/png", "data": "aGVsbG8="},
			},
			payload: func(image map[string]interface{}) map[string]interface{} {
				return map[string]interface{}{"contents": []interface{}{map[string]interface{}{
					"role": "user", "parts": []interface{}{image},
				}}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := tt.payload(tt.image)
			fingerprint := visionmedia.ImageFingerprintForBlock(tt.image)
			transformed, err := transformImagesForUpstream(payload, tt.serviceType, map[string]string{
				fingerprint: "稳定的图片描述",
			})
			if err != nil {
				t.Fatalf("transformImagesForUpstream() error = %v", err)
			}
			images, err := collectImagesForUpstream(transformed, tt.serviceType)
			if err != nil {
				t.Fatalf("collectImagesForUpstream() error = %v", err)
			}
			if len(images) != 0 {
				t.Fatalf("transformed payload still contains %d image blocks", len(images))
			}
			if text := extractUserText(transformed); !strings.Contains(text, "稳定的图片描述") {
				t.Fatalf("transformed payload text = %q", text)
			}
		})
	}
}

func TestParseVisionBatchResponseMatchesImagesByID(t *testing.T) {
	images := []visionImage{
		{id: "image_1"},
		{id: "image_2"},
	}
	raw := "```json\n{\"images\":[{\"id\":\"image_2\",\"description\":\"第二张\"},{\"id\":\"image_1\",\"description\":\"第一张\"}]}\n```"

	result, err := parseVisionBatchResponse(raw, images)
	if err != nil {
		t.Fatalf("parseVisionBatchResponse() error = %v", err)
	}
	if result["image_1"] != "第一张" || result["image_2"] != "第二张" {
		t.Fatalf("parseVisionBatchResponse() = %#v", result)
	}
}

func TestParseVisionBatchResponseAcceptsSingleImageMarkdown(t *testing.T) {
	images := []visionImage{{id: "image_1"}}
	raw := "```markdown\n这是一张终端截图，能看到一行错误信息。\n```"

	result, err := parseVisionBatchResponse(raw, images)
	if err != nil {
		t.Fatalf("parseVisionBatchResponse() error = %v", err)
	}
	if result["image_1"] != "这是一张终端截图，能看到一行错误信息。" {
		t.Fatalf("parseVisionBatchResponse() = %#v", result)
	}
}

func TestParseVisionBatchResponseAcceptsNamedMarkdownSections(t *testing.T) {
	images := []visionImage{{id: "image_1"}, {id: "image_2"}}
	raw := "```markdown\n[image_1]\n第一张图片的结果\n\n## 图片 2：第二张图片的结果\n```"

	result, err := parseVisionBatchResponse(raw, images)
	if err != nil {
		t.Fatalf("parseVisionBatchResponse() error = %v", err)
	}
	if result["image_1"] != "第一张图片的结果" || result["image_2"] != "第二张图片的结果" {
		t.Fatalf("parseVisionBatchResponse() = %#v", result)
	}
}

func TestParseVisionBatchResponseRejectsUnlabeledMultipleImages(t *testing.T) {
	images := []visionImage{{id: "image_1"}, {id: "image_2"}}
	if _, err := parseVisionBatchResponse("两张图片看起来都像终端截图。", images); err == nil {
		t.Fatal("parseVisionBatchResponse() error = nil")
	}
}

func TestParseVisionBatchResponseRejectsInvalidResults(t *testing.T) {
	images := []visionImage{
		{id: "image_1"},
		{id: "image_2"},
	}
	tests := []struct {
		name string
		raw  string
	}{
		{name: "缺少编号", raw: `{"images":[{"id":"image_1","description":"第一张"}]}`},
		{name: "重复编号", raw: `{"images":[{"id":"image_1","description":"第一张"},{"id":"image_1","description":"仍是第一张"}]}`},
		{name: "未知编号", raw: `{"images":[{"id":"image_1","description":"第一张"},{"id":"image_3","description":"第三张"}]}`},
		{name: "空描述", raw: `{"images":[{"id":"image_1","description":"第一张"},{"id":"image_2","description":""}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseVisionBatchResponse(tt.raw, images); err == nil {
				t.Fatal("parseVisionBatchResponse() error = nil")
			}
		})
	}
}

func TestParseVisionBatchResponse_RejectsMalformedJSONWithoutFallback(t *testing.T) {
	for _, raw := range []string{
		`{"images":[`,
		`{"images":[{"id":"image_1","description":"部分结果"}]`,
		`{"images" [{"id":"image_1","description":"不能抢救内部数组"}]}`,
		`[{"id":"image_1","description":"部分结果"}`,
		`["未结束的字符串`,
		"```json\n这不是 JSON\n```",
		`{"images":[{"id":"image_1","description":"结果"}]} trailing`,
	} {
		t.Run(raw, func(t *testing.T) {
			result, err := parseVisionBatchResponse(raw, []visionImage{{id: "image_1"}})
			require.ErrorContains(t, err, "JSON 无效")
			require.Nil(t, result)
		})
	}
}

func TestParseVisionBatchResponse_PreservesSingleImageText(t *testing.T) {
	for _, raw := range []string{
		"终端显示 {invalid JSON}，这是图片中的错误原文。",
		"[错误说明](https://example.com)\n截图显示连接失败。",
		"[终端截图]\n能看到一行错误。",
		"1. 可见事实：终端有报错。\n2. 可见原文：connection failed。\n3. 不确定项：原因未知。",
		"7. 截图中的第七条内容。\n8. 截图中的第八条内容。",
		"1. 左侧列表。\n1. 右侧列表。",
	} {
		result, err := parseVisionBatchResponse(raw, []visionImage{{id: "image_3"}})
		require.NoError(t, err)
		require.Equal(t, raw, result["image_3"])
	}
	text := "1. 可见事实\n2. 可见原文"
	result, err := parseVisionBatchResponse("```markdown\n"+text+"\n```", []visionImage{{id: "image_11"}})
	require.NoError(t, err)
	require.Equal(t, text, result["image_11"])
}

func TestParseVisionBatchResponse_SingleImageStillValidatesExplicitIDs(t *testing.T) {
	for _, raw := range []string{
		`{"images":[{"id":"image_1","description":"编号错误"}]}`,
		"[image_1]\n编号错误",
		"[image_3]\n第一次\n[image_3]\n重复",
		"[image_3]\n第一张\n[image_4]\n多余图片",
	} {
		t.Run(raw, func(t *testing.T) {
			result, err := parseVisionBatchResponse(raw, []visionImage{{id: "image_3"}})
			require.Error(t, err)
			require.Nil(t, result)
		})
	}
}

func TestBuildVisionRequest_ExamplesMatchActualBatch(t *testing.T) {
	for _, ids := range [][]string{{"image_3"}, {"image_11", "image_12"}, {"image_2", "image_4"}} {
		t.Run(strings.Join(ids, "+"), func(t *testing.T) {
			images := make([]visionImage, 0, len(ids))
			for _, id := range ids {
				images = append(images, visionImage{id: id, block: map[string]interface{}{
					"type":   "image",
					"source": map[string]interface{}{"type": "base64", "media_type": "image/png", "data": "aGVsbG8="},
				}})
			}
			body, err := buildVisionRequest("test", resolveVisionAnalysisProfile("比较图片"), images)
			require.NoError(t, err)
			var request struct {
				Messages []struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"messages"`
			}
			require.NoError(t, json.Unmarshal(body, &request))
			require.Len(t, request.Messages, 1)
			content := request.Messages[0].Content
			require.Len(t, content, 1+2*len(images))
			_, exampleAndRest, found := strings.Cut(content[0].Text, "格式为：")
			require.True(t, found)
			example, _, found := strings.Cut(exampleAndRest, "。如果")
			require.True(t, found)
			parsed, err := parseVisionBatchResponse(example, images)
			require.NoError(t, err, "提示中的 JSON 示例必须通过当前批次的结果校验")
			require.Len(t, parsed, len(images))
			for index, id := range ids {
				require.Contains(t, content[0].Text, "["+id+"]")
				require.Equal(t, "图片编号："+id, content[1+2*index].Text)
				require.Equal(t, "image", content[2+2*index].Type)
			}
			require.NotContains(t, content[0].Text, `"id":"image_1"`)
			require.NotContains(t, content[0].Text, "[image_1]")
		})
	}
}

func TestParseVisionBatchResponse_NumberedSectionsUseBatchOrder(t *testing.T) {
	images := []visionImage{{id: "image_11"}, {id: "image_3"}}
	result, err := parseVisionBatchResponse("1. 第一张\n2. 第二张", images)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"image_11": "第一张", "image_3": "第二张"}, result)
	result, err = parseVisionBatchResponse(`[{"id":"image_3","description":"第二张"},{"id":"image_11","description":"第一张"}]`, images)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"image_11": "第一张", "image_3": "第二张"}, result)
	for _, raw := range []string{
		"1. 第一张\n2. 第二张\n3. 越界",
		"1. 第一张\n1. 重复\n2. 第二张",
		"2. 跳过第一张\n1. 第一张",
		"0. 无效编号\n1. 第一张\n2. 第二张",
		"1. 第一张\n999999999999999999999999999999. 溢出\n2. 第二张",
		"1. 第一张",
	} {
		t.Run(raw, func(t *testing.T) {
			result, err := parseVisionBatchResponse(raw, images)
			require.Error(t, err)
			require.Nil(t, result)
		})
	}
}

func TestVisionMemoryCacheHasHardLimit(t *testing.T) {
	ClearCache()
	for index := 0; index < 600; index++ {
		key := fmt.Sprintf("key-%d", index)
		_, _, call, _ := claimAnalysis(key)
		storeMemoryCache(key, call, "result")
		finishAnalysis(key, call, visionAnalysisResult{result: "result"})
	}
	visionCache.Lock()
	count := len(visionCache.items)
	visionCache.Unlock()
	if count != 512 {
		t.Fatalf("memory cache size = %d, want 512", count)
	}
	ClearCache()
}

func TestClaimAnalysisDeduplicatesConcurrentImage(t *testing.T) {
	key := "conversation\x00image"
	ClearCache()

	_, cached, ownerCall, owner := claimAnalysis(key)
	if cached || !owner || ownerCall == nil {
		t.Fatalf("first claim = cached:%v owner:%v call:%v", cached, owner, ownerCall)
	}
	_, cached, waiterCall, owner := claimAnalysis(key)
	if cached || owner || waiterCall != ownerCall {
		t.Fatalf("second claim = cached:%v owner:%v sameCall:%v", cached, owner, waiterCall == ownerCall)
	}

	storeMemoryCache(key, ownerCall, "shared result")
	finishAnalysis(key, ownerCall, visionAnalysisResult{result: "shared result"})
	<-waiterCall.done
	if waiterCall.err != nil || waiterCall.result != "shared result" {
		t.Fatalf("waiter result = (%q, %v)", waiterCall.result, waiterCall.err)
	}
	result, cached, _, owner := claimAnalysis(key)
	if !cached || owner || result != "shared result" {
		t.Fatalf("completed claim = (%q, cached:%v, owner:%v)", result, cached, owner)
	}
	ClearCache()
}

func TestToClaudeImageBlockSupportsGeminiFileData(t *testing.T) {
	block, ok := toClaudeImageBlock(map[string]interface{}{
		"fileData": map[string]interface{}{
			"mimeType": "image/png",
			"fileUri":  "https://example.com/image.png",
		},
	})
	if !ok {
		t.Fatal("toClaudeImageBlock() ok = false")
	}
	source := block["source"].(map[string]interface{})
	if source["type"] != "url" || source["url"] != "https://example.com/image.png" {
		t.Fatalf("source = %#v", source)
	}
}

// TestShouldRetryVisionAttemptSkipsNonRetryableBody 锁定图片理解层的重试判定
// 与主链路 failover 一致：状态码可重试不代表值得重试，响应体里的内容审核 /
// 请求内容非法错误码必须一票否决。收敛前这里只看状态码，审核类 403 会被判为
// 可重试，于是轮询所有 Key 空转，并把每个无辜 Key 标记为失败。
func TestShouldRetryVisionAttemptSkipsNonRetryableBody(t *testing.T) {
	const contentPolicyBody = `{"error":{"message":"sensitive words detected","code":"sensitive_words_detected"}}`
	const invalidRequestBody = `{"error":{"code":"invalid_request"}}`
	const quotaBody = `{"error":{"message":"insufficient quota","code":"insufficient_quota"}}`

	tests := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{"审核拦截的 403 不重试", 403, contentPolicyBody, false},
		{"审核拦截的 500 不重试", 500, contentPolicyBody, false},
		{"请求内容非法不重试", 400, invalidRequestBody, false},
		{"额度不足的 429 仍重试", 429, quotaBody, true},
		{"鉴权失败的 401 仍重试", 401, `{"error":{"code":"authentication_error"}}`, true},
		{"上游 502 仍重试", 502, `upstream exploded`, true},
		{"响应体为空时退回状态码判断", 403, "", true},
		{"404 不重试", 404, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRetryVisionAttempt(tt.statusCode, []byte(tt.body)); got != tt.want {
				t.Errorf("shouldRetryVisionAttempt(%d, %q) = %v, want %v", tt.statusCode, tt.body, got, tt.want)
			}
		})
	}
}

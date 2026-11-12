package utils

import (
	"bytes"
	"mime/multipart"
	"strings"
	"testing"
)

func TestMultipartBoundary(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        string
		wantOK      bool
	}{
		{name: "标准表单", contentType: `multipart/form-data; boundary=abc123`, want: "abc123", wantOK: true},
		{name: "带引号 boundary", contentType: `multipart/form-data; boundary="a b c"`, want: "a b c", wantOK: true},
		{name: "大写媒体类型", contentType: `MULTIPART/FORM-DATA; boundary=X`, want: "X", wantOK: true},
		{name: "缺少 boundary", contentType: `multipart/form-data`, want: "", wantOK: false},
		{name: "非 multipart", contentType: `application/json`, want: "", wantOK: false},
		{name: "无法解析", contentType: `multipart/form-data; boundary`, want: "", wantOK: false},
		{name: "空值", contentType: "", want: "", wantOK: false},
	}
	for _, test := range tests {
		got, ok := MultipartBoundary(test.contentType)
		if got != test.want || ok != test.wantOK {
			t.Errorf("%s: MultipartBoundary(%q) = (%q, %v)，期望 (%q, %v)",
				test.name, test.contentType, got, ok, test.want, test.wantOK)
		}
	}
}

// buildTestForm 构造一张含文本字段、重复字段与文件部件的表单。
func buildTestForm(t *testing.T) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("model", "gpt-image-1"); err != nil {
		t.Fatalf("写入 model 字段失败: %v", err)
	}
	if err := writer.WriteField("prompt", "一只在屋顶的猫"); err != nil {
		t.Fatalf("写入 prompt 字段失败: %v", err)
	}
	if err := writer.WriteField("prompt", "第二段提示"); err != nil {
		t.Fatalf("写入重复 prompt 字段失败: %v", err)
	}
	filePart, err := writer.CreateFormFile("image", "cat.png")
	if err != nil {
		t.Fatalf("创建文件部件失败: %v", err)
	}
	if _, err := filePart.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0xFF}); err != nil {
		t.Fatalf("写入文件内容失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭表单失败: %v", err)
	}
	return buffer.Bytes(), writer.FormDataContentType()
}

func TestParseMultipartPartsPreservesOrderFileNameAndContent(t *testing.T) {
	body, contentType := buildTestForm(t)
	boundary, ok := MultipartBoundary(contentType)
	if !ok {
		t.Fatalf("测试表单 Content-Type 无 boundary: %s", contentType)
	}

	parts, err := ParseMultipartParts(body, boundary)
	if err != nil {
		t.Fatalf("解析表单失败: %v", err)
	}
	if len(parts) != 4 {
		t.Fatalf("部件数量 = %d，期望 4", len(parts))
	}
	wantNames := []string{"model", "prompt", "prompt", "image"}
	for index, want := range wantNames {
		if parts[index].FormName != want {
			t.Errorf("第 %d 个部件字段名 = %q，期望 %q", index, parts[index].FormName, want)
		}
	}
	if parts[3].FileName != "cat.png" {
		t.Errorf("文件部件 FileName = %q，期望 cat.png", parts[3].FileName)
	}
	for index := 0; index < 3; index++ {
		if parts[index].FileName != "" {
			t.Errorf("第 %d 个部件不应被识别为文件部件: %q", index, parts[index].FileName)
		}
	}
	if string(parts[1].Content) != "一只在屋顶的猫" || string(parts[2].Content) != "第二段提示" {
		t.Errorf("重复字段内容错误: %q / %q", parts[1].Content, parts[2].Content)
	}
	if !bytes.Equal(parts[3].Content, []byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0xFF}) {
		t.Errorf("文件部件内容被改写: %x", parts[3].Content)
	}
}

func TestEncodeMultipartPartsRoundTripsWithNewBoundary(t *testing.T) {
	body, contentType := buildTestForm(t)
	boundary, _ := MultipartBoundary(contentType)
	parts, err := ParseMultipartParts(body, boundary)
	if err != nil {
		t.Fatalf("解析表单失败: %v", err)
	}

	parts[0].Content = []byte("dall-e-3")
	encoded, encodedContentType, err := EncodeMultipartParts(parts)
	if err != nil {
		t.Fatalf("重新编码表单失败: %v", err)
	}

	newBoundary, ok := MultipartBoundary(encodedContentType)
	if !ok {
		t.Fatalf("重新编码后的 Content-Type 无 boundary: %s", encodedContentType)
	}
	if newBoundary == boundary {
		t.Fatal("重新编码应生成新 boundary，否则调用方无法察觉必须同步请求头")
	}
	if bytes.Contains(encoded, []byte(boundary)) {
		t.Fatal("重新编码后的表单仍含旧 boundary")
	}

	reparsed, err := ParseMultipartParts(encoded, newBoundary)
	if err != nil {
		t.Fatalf("解析重新编码后的表单失败: %v", err)
	}
	if len(reparsed) != len(parts) {
		t.Fatalf("往返后部件数量 = %d，期望 %d", len(reparsed), len(parts))
	}
	for index := range parts {
		if reparsed[index].FormName != parts[index].FormName ||
			reparsed[index].FileName != parts[index].FileName ||
			!bytes.Equal(reparsed[index].Content, parts[index].Content) {
			t.Errorf("第 %d 个部件往返不一致: %+v vs %+v", index, reparsed[index], parts[index])
		}
	}
	if string(reparsed[0].Content) != "dall-e-3" {
		t.Errorf("model 字段改写未生效: %q", reparsed[0].Content)
	}
}

func TestReadMultipartTextFieldsSkipsFilesAndKeepsFirstOccurrence(t *testing.T) {
	body, contentType := buildTestForm(t)
	boundary, _ := MultipartBoundary(contentType)

	values, err := ReadMultipartTextFields(body, boundary, []string{"prompt", "image", "size"})
	if err != nil {
		t.Fatalf("读取表单字段失败: %v", err)
	}
	if values["prompt"] != "一只在屋顶的猫" {
		t.Errorf("prompt 应取首个同名字段，实际 = %q", values["prompt"])
	}
	if _, exists := values["image"]; exists {
		t.Error("文件部件不应被读入")
	}
	if _, exists := values["size"]; exists {
		t.Error("缺失字段不应出现在返回值中")
	}
}

func TestMultipartHelpersRejectMissingBoundary(t *testing.T) {
	body, contentType := buildTestForm(t)
	boundary, _ := MultipartBoundary(contentType)

	if _, err := ParseMultipartParts(body, ""); err == nil {
		t.Error("ParseMultipartParts 缺少 boundary 时必须显式报错")
	}
	if _, err := ReadMultipartTextFields(body, "", []string{"prompt"}); err == nil {
		t.Error("ReadMultipartTextFields 缺少 boundary 时必须显式报错")
	}
	if _, err := ParseMultipartParts([]byte("not a form"), boundary); err == nil {
		t.Error("表单畸形时必须显式报错")
	} else if !strings.Contains(err.Error(), "multipart") {
		t.Errorf("错误信息应指明 multipart 解析失败: %v", err)
	}
}

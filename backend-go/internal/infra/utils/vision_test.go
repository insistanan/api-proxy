package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestExtractImageFingerprints(t *testing.T) {
	body := []byte(`{
		"messages":[{"content":[
			{"type":"image","source":{"type":"base64","data":"aGVsbG8="}},
			{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}},
			{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}
		]}]
	}`)

	fingerprints := ExtractImageFingerprints(body)
	helloSum := sha256.Sum256([]byte("hello"))
	urlSum := sha256.Sum256([]byte("https://example.com/image.png"))
	wantImage := "sha256:" + hex.EncodeToString(helloSum[:])
	wantURL := "url-sha256:" + hex.EncodeToString(urlSum[:])

	if len(fingerprints) != 2 {
		t.Fatalf("fingerprint count = %d, want 2: %#v", len(fingerprints), fingerprints)
	}
	if fingerprints[0] != wantImage || fingerprints[1] != wantURL {
		t.Fatalf("fingerprints = %#v, want [%q %q]", fingerprints, wantImage, wantURL)
	}
}

// TestParseImageDataURL 固定 data URL 解析的行为契约：
// 收敛前 utils/providers/converters 三处实现的判定规则在此逐条锁定。
func TestParseImageDataURL(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		wantMIME  string
		wantData  string
		wantParse bool
	}{
		{"标准 base64 data URL", "data:image/jpeg;base64,aGVsbG8=", "image/jpeg", "aGVsbG8=", true},
		{"头部无 mediaType 时填默认值", "data:;base64,aGVsbG8=", DefaultImageMediaType, "aGVsbG8=", true},
		{"头部完全为空时填默认值", "data:,aGVsbG8=", DefaultImageMediaType, "aGVsbG8=", true},
		{"带附加参数时剥离 base64 之后内容", "data:image/webp;charset=utf-8;base64,aGVsbG8=", "image/webp;charset=utf-8", "aGVsbG8=", true},
		{"非 base64 编码仍按逗号切分", "data:image/svg+xml,%3Csvg%2F%3E", "image/svg+xml", "%3Csvg%2F%3E", true},
		{"非 data 前缀不解析", "https://example.com/a.png", "", "", false},
		{"缺少逗号不解析", "data:image/png;base64", "", "", false},
		{"载荷为空不解析", "data:image/png;base64,", "", "", false},
		{"空串不解析", "", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mime, data, ok := ParseImageDataURL(tc.url)
			if ok != tc.wantParse {
				t.Fatalf("ok = %v, want %v", ok, tc.wantParse)
			}
			if mime != tc.wantMIME {
				t.Fatalf("mediaType = %q, want %q", mime, tc.wantMIME)
			}
			if data != tc.wantData {
				t.Fatalf("data = %q, want %q", data, tc.wantData)
			}
		})
	}
}

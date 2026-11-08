package proxycore

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestExtractRequestedChannelIndexPreservesValidation(t *testing.T) {
	tests := []struct {
		body    string
		want    int64
		present bool
		wantErr bool
	}{
		{`{}`, 0, false, false},
		{`{"metadata":null}`, 0, false, false},
		{`{"metadata":{"channel_index":null}}`, 0, false, false},
		{`{"metadata":{"channel_index":0}}`, 0, true, false},
		{`{"metadata":{"channel_index":9007199254740993}}`, 9007199254740993, true, false},
		{`{"metadata":{"channel_index":1,"channel_index":2}}`, 2, true, false},
		{`{"metadata":{"channel_index":1},"metadata":{"channel_index":3}}`, 3, true, false},
		{`{"Metadata":{"channel_index":4}}`, 0, false, false},
		{`{"metadata":{"CHANNEL_INDEX":4}}`, 0, false, false},
		{`{"metadata":{"channel_index":1,"CHANNEL_INDEX":4},"Metadata":{"channel_index":5}}`, 1, true, false},
		{`{"metadata":{"channel_index":1},"metadata":null}`, 0, false, false},
		{`{"metadata":null,"metadata":{"channel_index":3}}`, 3, true, false},
		{`{"meta\u0064ata":{"channel_\u0069ndex":2}}`, 2, true, false},
		{`{"metadata":{"channel_index":1},"meta\u0064ata":{"channel_index":3}}`, 3, true, false},
		{`{"metadata":"invalid"}`, 0, false, true},
		{`{"metadata":[]}`, 0, false, true},
		{`{"metadata":{"channel_index":"1"}}`, 0, true, true},
		{`{"metadata":{"channel_index":true}}`, 0, true, true},
		{`{"metadata":{"channel_index":1.0}}`, 0, true, true},
		{`{"metadata":{"channel_index":1e2}}`, 0, true, true},
		{`{"metadata":{"channel_index":-1}}`, 0, true, true},
		{`{"metadata":{"channel_index":9223372036854775808}}`, 0, true, true},
	}
	for _, test := range tests {
		got, present, err := ExtractRequestedChannelIndex([]byte(test.body))
		if int64(got) != test.want || present != test.present || (err != nil) != test.wantErr {
			t.Errorf("请求 %s: got=(%d,%v,%v)，预期=(%d,%v,错误=%v)", test.body, got, present, err, test.want, test.present, test.wantErr)
		}
	}
}

func TestIsQuickTestRequestPreservesMetadataAndHeaderSemantics(t *testing.T) {
	for _, test := range []struct {
		body string
		want bool
	}{
		{`{"metadata":{"purpose":" QUICK_TEST "}}`, true},
		{`{"metadata":{"purpose":"quick_test","purpose":"other"}}`, false},
		{`{"Metadata":{"purpose":"quick_test"}}`, false},
		{`{"metadata":{"PURPOSE":"quick_test"}}`, false},
		{`{"metadata":{"purpose":"quick_test","PURPOSE":"other"},"Metadata":null}`, true},
		{`{"metadata":{"purpose":"quick_test"},"metadata":{"purpose":"other"}}`, false},
		{`{"metadata":{"purpose":"other"},"meta\u0064ata":{"purpose":"quick_test"}}`, true},
		{`{"metadata":{"purpose":true}}`, false},
		{`{"metadata":"invalid"}`, false},
		{`{"text":"` + strings.Repeat("A", 2*1024*1024) + `","metadata":{"purpose":"quick_test"}}`, true},
	} {
		if got := IsQuickTestRequest(nil, []byte(test.body)); got != test.want {
			t.Errorf("快捷测试判断 = %v，预期 %v", got, test.want)
		}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	c.Request.Header.Set("X-Proxy-Purpose", "quick_test")
	if !IsQuickTestRequest(c, []byte(`invalid JSON`)) {
		t.Fatal("请求头标记应优先于正文解析")
	}
}

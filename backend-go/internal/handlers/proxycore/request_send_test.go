package proxycore

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/insistanan/api-proxy/internal/infra/config"
	"github.com/insistanan/api-proxy/internal/storage/logger"
)

func TestLogRequestDetailsPreservesBodyAndClose(t *testing.T) {
	for _, test := range []struct {
		name      string
		size      int
		knownSize bool
		getBody   bool
		wantReads int
	}{
		{"小正文使用独立 reader", 128, true, true, 0},
		{"小正文回放前缀", 128, true, false, 128},
		{"已知长度大正文不读取", logger.MaxBodyBytes + 64, true, false, 0},
		{"未知长度大正文有界读取", logger.MaxBodyBytes + 64, false, false, logger.MaxBodyBytes + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := strings.Repeat("A", test.size)
			req, err := http.NewRequest("POST", "http://example.test", strings.NewReader(original))
			if err != nil {
				t.Fatal(err)
			}
			reader := &countedResponseBody{Reader: req.Body}
			req.Body = reader
			if !test.getBody {
				req.GetBody = nil
			}
			if !test.knownSize {
				req.ContentLength = -1
			}
			if err := logRequestDetails(req, &config.EnvConfig{LogLevel: "error"}, "messages", false); err != nil {
				t.Fatalf("记录请求失败: %v", err)
			}
			if reader.readBytes != test.wantReads || reader.closed {
				t.Fatalf("日志过量读取或提前关闭正文: readBytes=%d, closed=%v", reader.readBytes, reader.closed)
			}
			forwarded, err := io.ReadAll(req.Body)
			if err != nil || string(forwarded) != original {
				t.Fatalf("日志影响实际转发正文: len=%d, err=%v", len(forwarded), err)
			}
			if err := req.Body.Close(); err != nil || !reader.closed {
				t.Fatalf("原始正文的关闭责任丢失: closed=%v, err=%v", reader.closed, err)
			}
		})
	}
}

func TestLogRequestDetailsReportsGetBodyFailureAndClosesBody(t *testing.T) {
	req, err := http.NewRequest("POST", "http://example.test", strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	reader := &countedResponseBody{Reader: req.Body}
	req.Body = reader
	wantErr := errors.New("无法重放正文")
	req.GetBody = func() (io.ReadCloser, error) { return nil, wantErr }
	err = logRequestDetails(req, &config.EnvConfig{LogLevel: "error"}, "messages", false)
	if !errors.Is(err, wantErr) || !reader.closed {
		t.Fatalf("日志正文失败被吞掉或未关闭 reader: err=%v, closed=%v", err, reader.closed)
	}
}

func TestLogRequestDetailsRejectsNilGetBodyReader(t *testing.T) {
	req, err := http.NewRequest("POST", "http://example.test", strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	reader := &countedResponseBody{Reader: req.Body}
	req.Body = reader
	req.GetBody = func() (io.ReadCloser, error) { return nil, nil }
	err = logRequestDetails(req, &config.EnvConfig{LogLevel: "error"}, "messages", false)
	if err == nil || !strings.Contains(err.Error(), "GetBody 返回空 reader") || !reader.closed {
		t.Fatalf("空日志 reader 应显式失败并关闭原正文: err=%v, closed=%v", err, reader.closed)
	}
}

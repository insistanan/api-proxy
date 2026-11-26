package modelfingerprint

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
	"github.com/klauspost/compress/zstd"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/infra/httpclient"
)

const maxReferenceBytes = 256 << 20

func readReferenceFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readReferenceBytes(file, maxReferenceBytes)
}

func readReferenceBytes(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("指纹参考库超过资源容量上限")
	}
	return data, nil
}

func downloadReference(ctx context.Context, cfg *config.ConfigManager) ([]byte, error) {
	client, err := httpclient.DownloadClient(cfg, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	var failures []error
	for _, source := range lmfpd.DefaultDataURLs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := downloadReferenceSource(ctx, client, source)
		if err == nil {
			return data, nil
		}
		failures = append(failures, fmt.Errorf("参考源 %s: %w", source, err))
	}
	return nil, errors.Join(failures...)
}

func downloadReferenceSource(ctx context.Context, client *http.Client, source string) ([]byte, error) {
	get := func(path string, limit int64) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source+path, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
		}
		return readReferenceBytes(resp.Body, limit)
	}
	manifest, err := get("bundle.json", 4<<20)
	if err != nil {
		return nil, err
	}
	var bundle struct {
		BuiltAt        string `json:"built_at"`
		Schema         string `json:"schema"`
		DetectorSchema string `json:"detector_schema"`
		Files          map[string]struct {
			Parts []string `json:"parts"`
		} `json:"files"`
	}
	if err := json.Unmarshal(manifest, &bundle); err != nil {
		return nil, err
	}
	if bundle.Schema != "lmfpd-data-v1" || bundle.DetectorSchema != "shared-detector-v2" {
		return nil, fmt.Errorf("指纹参考库格式不兼容，请升级检测 SDK")
	}
	parts := bundle.Files["shared_detector.bin"].Parts
	if len(parts) == 0 || len(parts) > 256 {
		return nil, fmt.Errorf("指纹参考库分片清单无效")
	}
	var compressed []byte
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "/\\:?%#") {
			return nil, fmt.Errorf("指纹参考库分片名称无效")
		}
		chunk, err := get("chunks/"+part, int64(maxReferenceBytes-len(compressed)))
		if err != nil {
			return nil, err
		}
		compressed = append(compressed, chunk...)
	}
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxReferenceBytes))
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	detector, err := decoder.DecodeAll(compressed, nil)
	if err != nil {
		return nil, err
	}
	if len(detector)+len(manifest)+4 > maxReferenceBytes {
		return nil, fmt.Errorf("指纹参考库解压后超过容量上限")
	}
	data := make([]byte, 4, 4+len(manifest)+len(detector))
	binary.LittleEndian.PutUint32(data, uint32(len(manifest)))
	data = append(append(data, manifest...), detector...)
	if _, err := referenceDigest(data, bundle.BuiltAt); err != nil {
		return nil, err
	}
	return data, ctx.Err()
}

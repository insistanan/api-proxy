package modelfingerprint

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"insistanan/api-proxy/internal/infra/config"
	"insistanan/api-proxy/internal/storage/atomicfile"
	"os"
	"path/filepath"
	"strings"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
)

type cachedReference struct {
	*lmfpd.Detector
	digest string
}

func (d *cachedReference) DataSHA256() string { return d.digest }

func loadReference(ctx context.Context, cfg *config.ConfigManager) (analyzer, error) {
	cachePath, err := filepath.Abs(filepath.Join(DataDirectory, "data.bin"))
	if err != nil {
		return nil, fmt.Errorf("解析指纹参考库路径失败：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0700); err != nil {
		return nil, fmt.Errorf("创建指纹参考库目录失败：%w", err)
	}
	data, err := readReferenceFile(cachePath)
	downloaded := errors.Is(err, os.ErrNotExist)
	if downloaded {
		data, err = downloadReference(ctx, cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("读取指纹参考库失败：%w", err)
	}
	detector, digest, err := decodeReference(ctx, data)
	if err != nil {
		return nil, err
	}
	if downloaded {
		if err := atomicfile.Write(cachePath, data, atomicfile.Options{Mode: 0600}); err != nil {
			return nil, fmt.Errorf("保存指纹参考库失败: %w", err)
		}
	}
	return &cachedReference{Detector: detector, digest: digest}, nil
}

// SDK 未开放下载客户端注入，只使用其纯本地加载入口，禁止隐式网络与后台刷新。
func decodeReference(ctx context.Context, data []byte) (detector *lmfpd.Detector, digest string, err error) {
	if len(data) < 4 {
		return nil, "", fmt.Errorf("指纹参考库缓存头不完整")
	}
	length := uint64(binary.LittleEndian.Uint32(data[:4]))
	if length == 0 || 4+length >= uint64(len(data)) {
		return nil, "", fmt.Errorf("指纹参考库缓存长度无效")
	}
	var version struct {
		BuiltAt string `json:"built_at"`
	}
	if err := json.Unmarshal(data[4:4+length], &version); err != nil {
		return nil, "", err
	}
	digest, err = referenceDigest(data, version.BuiltAt)
	if err != nil {
		return nil, "", err
	}
	dir, err := os.MkdirTemp("", "api-proxy-reference-")
	if err != nil {
		return nil, "", err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	if err = os.WriteFile(filepath.Join(dir, "unified_bank.json"), data[4:4+length], 0600); err != nil {
		return nil, "", err
	}
	if err = os.WriteFile(filepath.Join(dir, "shared_detector.bin"), data[4+length:], 0600); err != nil {
		return nil, "", err
	}
	if err = ctx.Err(); err != nil {
		return nil, "", err
	}
	detector, err = lmfpd.Load(ctx, lmfpd.LoadOptions{Sources: []string{dir}})
	return detector, digest, err
}

// referenceDigest 解析 SDK 缓存：4 字节小端长度 + manifest JSON + 评分数据，校验 shared_detector.bin 的 SHA-256 后返回摘要。
func referenceDigest(data []byte, builtAt string) (string, error) {
	if len(data) < 4 {
		return "", fmt.Errorf("指纹参考库缓存头不完整")
	}
	length := uint64(binary.LittleEndian.Uint32(data[:4]))
	if length == 0 || 4+length >= uint64(len(data)) {
		return "", fmt.Errorf("指纹参考库缓存长度无效")
	}
	var manifest struct {
		BuiltAt        string `json:"built_at"`
		Schema         string `json:"schema"`
		DetectorSchema string `json:"detector_schema"`
		Files          map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data[4:4+length], &manifest); err != nil {
		return "", fmt.Errorf("解析指纹参考库版本信息失败：%w", err)
	}
	if manifest.Schema != "lmfpd-data-v1" || manifest.DetectorSchema != "shared-detector-v2" {
		return "", fmt.Errorf("指纹参考库格式不兼容，请升级检测 SDK")
	}
	if manifest.BuiltAt == "" || manifest.BuiltAt != builtAt {
		return "", fmt.Errorf("指纹参考库缓存与已加载版本不一致")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data[4+length:]))
	if !strings.EqualFold(manifest.Files["shared_detector.bin"].SHA256, digest) {
		return "", fmt.Errorf("指纹参考库 SHA-256 校验失败，缓存数据不完整或已变化")
	}
	return digest, nil
}

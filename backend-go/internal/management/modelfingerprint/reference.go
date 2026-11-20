package modelfingerprint

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
)

type cachedReference struct {
	*lmfpd.Detector
	digest string
}

func (d *cachedReference) DataSHA256() string { return d.digest }

func loadReference(ctx context.Context) (analyzer, error) {
	cachePath, err := filepath.Abs(filepath.Join(DataDirectory, "data.bin"))
	if err != nil {
		return nil, fmt.Errorf("解析指纹参考库路径失败：%w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0700); err != nil {
		return nil, fmt.Errorf("创建指纹参考库目录失败：%w", err)
	}
	// 固定本进程的数据版本，禁止 SDK 在后台静默替换基准。
	detector, err := lmfpd.Load(ctx, lmfpd.LoadOptions{Cache: cachePath, TTL: 100 * 365 * 24 * time.Hour})
	if err != nil {
		return nil, err
	}
	// SDK 的落盘是尽力而为且未校验摘要，显式读取并核验后才允许采样。
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, fmt.Errorf("读取已缓存的指纹参考库失败（%s）：%w", cachePath, err)
	}
	digest, err := referenceDigest(data, detector.BuiltAt())
	if err != nil {
		return nil, err
	}
	return &cachedReference{Detector: detector, digest: digest}, nil
}

// referenceDigest 对应 SDK v0.2.0 的缓存布局：长度、bundle.json、解压后的评分数据。
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

package utils

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// ErrDecompressedBodyTooLarge 表示解压缩输出超过了最大允许字节数（防御压缩炸弹）。
var ErrDecompressedBodyTooLarge = errors.New("decompressed body exceeds maximum allowed size")

// ErrUnsupportedContentEncoding 表示遇到了不受支持的压缩算法。
var ErrUnsupportedContentEncoding = errors.New("unsupported content-encoding")

// SplitContentCodings 将 Content-Encoding 头部值按逗号拆分为小写且去除 identity 和空值的 coding 列表。
func SplitContentCodings(contentEncoding string) []string {
	trimmedEncoding := strings.TrimSpace(contentEncoding)
	if trimmedEncoding == "" {
		return nil
	}

	rawSegments := strings.Split(trimmedEncoding, ",")
	parsedCodings := make([]string, 0, len(rawSegments))
	for _, segment := range rawSegments {
		cleanedCoding := strings.ToLower(strings.TrimSpace(segment))
		if cleanedCoding != "" && cleanedCoding != "identity" {
			parsedCodings = append(parsedCodings, cleanedCoding)
		}
	}
	return parsedCodings
}

// IsSingleSupportedCoding 检查单个 coding 是否受解压缩支持。
func IsSingleSupportedCoding(coding string) bool {
	switch strings.ToLower(strings.TrimSpace(coding)) {
	case "gzip", "x-gzip", "deflate", "br", "zstd", "zst":
		return true
	default:
		return false
	}
}

// IsSupportedContentEncoding 检查 Content-Encoding 是否全部可以被解压。
// 若包含未支持的编码或为空则返回 false。
func IsSupportedContentEncoding(contentEncoding string) bool {
	codings := SplitContentCodings(contentEncoding)
	if len(codings) == 0 {
		return false
	}
	for _, coding := range codings {
		if !IsSingleSupportedCoding(coding) {
			return false
		}
	}
	return true
}

// GetContentEncoding 从 HTTP Header 提取并规范化 Content-Encoding。
// 支持多个 Content-Encoding 头部值拼接；忽略 identity 和空值。
func GetContentEncoding(headers http.Header) string {
	if headers == nil {
		return ""
	}
	var rawValues []string
	for _, headerValue := range headers.Values("Content-Encoding") {
		trimmedValue := strings.TrimSpace(headerValue)
		if trimmedValue != "" {
			rawValues = append(rawValues, trimmedValue)
		}
	}
	if len(rawValues) == 0 {
		return ""
	}
	combinedValues := strings.Join(rawValues, ", ")
	codings := SplitContentCodings(combinedValues)
	if len(codings) == 0 {
		return ""
	}
	return strings.Join(codings, ", ")
}

// ReadWithOutputLimit 从 reader 中读取解压内容，最多读取 maximumBytes 字节。
// 一旦解压输出超过预算立即截停并返回 ErrDecompressedBodyTooLarge，
// 防御压缩炸弹在内存中完全展开导致 OOM。
func ReadWithOutputLimit(reader io.Reader, maximumBytes int64) ([]byte, error) {
	if maximumBytes <= 0 {
		return nil, ErrDecompressedBodyTooLarge
	}
	limitedReader := io.LimitReader(reader, maximumBytes+1)
	decompressedBytes, readErr := io.ReadAll(limitedReader)
	if readErr != nil {
		return nil, readErr
	}
	if int64(len(decompressedBytes)) > maximumBytes {
		return nil, ErrDecompressedBodyTooLarge
	}
	return decompressedBytes, nil
}

// DecompressSingle 解压单个 content-coding，输出受 maximumBytes 限制。
func DecompressSingle(coding string, bodyBytes []byte, maximumBytes int64) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(coding)) {
	case "gzip", "x-gzip":
		gzipReader, err := gzip.NewReader(bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("创建 gzip 解码器失败: %w", err)
		}
		defer gzipReader.Close()
		return ReadWithOutputLimit(gzipReader, maximumBytes)

	case "deflate":
		// RFC 9110: deflate 指 zlib 包裹格式；但部分上游/客户端违规发送 raw deflate 流。
		// 先按规范尝试 zlib，失败再回退 raw deflate。
		zlibReader, zlibErr := zlib.NewReader(bytes.NewReader(bodyBytes))
		if zlibErr == nil {
			decompressedBytes, readErr := ReadWithOutputLimit(zlibReader, maximumBytes)
			_ = zlibReader.Close()
			if readErr == nil {
				return decompressedBytes, nil
			}
			if errors.Is(readErr, ErrDecompressedBodyTooLarge) {
				return nil, readErr
			}
		}

		// 回退到 raw deflate
		flateReader := flate.NewReader(bytes.NewReader(bodyBytes))
		defer flateReader.Close()
		decompressedBytes, flateErr := ReadWithOutputLimit(flateReader, maximumBytes)
		if flateErr != nil {
			if zlibErr != nil {
				return nil, fmt.Errorf("deflate 解压失败: zlib 错误 (%v), raw deflate 错误 (%w)", zlibErr, flateErr)
			}
			return nil, flateErr
		}
		return decompressedBytes, nil

	case "br":
		brotliReader := brotli.NewReader(bytes.NewReader(bodyBytes))
		return ReadWithOutputLimit(brotliReader, maximumBytes)

	case "zstd", "zst":
		if maximumBytes <= 0 {
			return nil, ErrDecompressedBodyTooLarge
		}
		// 输出限额本身不能限制解码器的窗口分配；单独约束窗口并关闭并行解码。
		// 保留至少 8 MiB，兼容输出很小但声明常规窗口的流式压缩帧。
		decoderMemory := uint64(maximumBytes)
		if decoderMemory < 8<<20 {
			decoderMemory = 8 << 20
		}
		zstdDecoder, err := zstd.NewReader(bytes.NewReader(bodyBytes), zstd.WithDecoderMaxMemory(decoderMemory), zstd.WithDecoderConcurrency(1))
		if err != nil {
			return nil, fmt.Errorf("创建 zstd 解码器失败: %w", err)
		}
		defer zstdDecoder.Close()
		return ReadWithOutputLimit(zstdDecoder, maximumBytes)

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedContentEncoding, coding)
	}
}

// DecompressBodyWithLimit 根据 content-encoding 解压 body 字节。
// 支持堆叠编码（如 "gzip, zstd"），按 RFC 9110 §8.4 反向解码（最后应用的先解）。
// 每个 coding 的解压输出都受 maximumBytes 限制，超限立即中止并返回 ErrDecompressedBodyTooLarge。
// 返回: (decompressedBytes, wasDecompressed, error)
func DecompressBodyWithLimit(contentEncoding string, bodyBytes []byte, maximumBytes int64) ([]byte, bool, error) {
	codings := SplitContentCodings(contentEncoding)
	if len(codings) == 0 {
		return bodyBytes, false, nil
	}

	for _, coding := range codings {
		if !IsSingleSupportedCoding(coding) {
			return bodyBytes, false, fmt.Errorf("%w: %s", ErrUnsupportedContentEncoding, coding)
		}
	}

	currentBytes := bodyBytes
	// 反向解码：列表末尾是最后应用的编码，须最先解
	for reverseIndex := len(codings) - 1; reverseIndex >= 0; reverseIndex-- {
		targetCoding := codings[reverseIndex]
		decompressedResult, err := DecompressSingle(targetCoding, currentBytes, maximumBytes)
		if err != nil {
			return nil, false, err
		}
		currentBytes = decompressedResult
	}

	return currentBytes, true, nil
}

// DecompressResponseBodyIfNeeded 检测并解压缩 HTTP 响应体。
// 当响应包含受支持的 Content-Encoding 时进行安全有界解压；
// 若解压成功，返回 (decompressedBytes, true, nil)；
// 若无压缩或编码为 identity，返回 (bodyBytes, false, nil)；
// 若超限，返回 (nil, false, ErrDecompressedBodyTooLarge)；
// 若遇到不支持的编码或解码失败，记录警告并返回 (bodyBytes, false, err)，由调用方决定是透传还是报错。
func DecompressResponseBodyIfNeeded(response *http.Response, bodyBytes []byte, maximumBytes int64) ([]byte, bool, error) {
	if response == nil || len(bodyBytes) == 0 {
		return bodyBytes, false, nil
	}

	encoding := GetContentEncoding(response.Header)
	if encoding == "" {
		return bodyBytes, false, nil
	}

	if !IsSupportedContentEncoding(encoding) {
		log.Printf("[Gzip-Warning] 不支持的上游响应 content-encoding: %s，保留原始数据透传", encoding)
		return bodyBytes, false, nil
	}

	decompressedResult, decodedSuccessfully, decompressErr := DecompressBodyWithLimit(encoding, bodyBytes, maximumBytes)
	if decompressErr != nil {
		if errors.Is(decompressErr, ErrDecompressedBodyTooLarge) {
			log.Printf("[Gzip-Limit] 上游响应体解压输出超过限制 (%d 字节): %v", maximumBytes, decompressErr)
			return nil, false, decompressErr
		}
		log.Printf("[Gzip-Warning] 解压上游响应体失败 (%s): %v，使用原始数据", encoding, decompressErr)
		return bodyBytes, false, decompressErr
	}

	return decompressedResult, decodedSuccessfully, nil
}

// DecompressGzipIfNeeded 兼容历史调用的解压缩包装函数。
// 现已升级支持 gzip、deflate、br、zstd 等多算法有界解压（默认上限 64MB）。
func DecompressGzipIfNeeded(resp *http.Response, bodyBytes []byte) []byte {
	decompressedResult, decodedSuccessfully, decompressErr := DecompressResponseBodyIfNeeded(resp, bodyBytes, 64*1024*1024)
	if decompressErr != nil || !decodedSuccessfully {
		return bodyBytes
	}
	return decompressedResult
}

type decompressReadCloser struct {
	reader       io.Reader
	sourceCloser io.Closer
	readerCloser io.Closer
	customClose  func()
}

func (wrapper *decompressReadCloser) Read(p []byte) (int, error) {
	return wrapper.reader.Read(p)
}

func (wrapper *decompressReadCloser) Close() error {
	var firstError error
	if wrapper.customClose != nil {
		wrapper.customClose()
	}
	if wrapper.readerCloser != nil {
		if err := wrapper.readerCloser.Close(); err != nil && firstError == nil {
			firstError = err
		}
	}
	if wrapper.sourceCloser != nil {
		if err := wrapper.sourceCloser.Close(); err != nil && firstError == nil {
			firstError = err
		}
	}
	return firstError
}

// WrapStreamReaderIfNeeded 检查响应头是否含有 Content-Encoding。
// 如果包含受支持的单层压缩格式（如 gzip、br、zstd、deflate），则包装流式解码 Reader 以便事件流逐块解压消费。
// 若无压缩，返回 (response.Body, false, nil)。
func WrapStreamReaderIfNeeded(response *http.Response) (io.ReadCloser, bool, error) {
	if response == nil || response.Body == nil {
		return nil, false, nil
	}
	encoding := GetContentEncoding(response.Header)
	if encoding == "" {
		return response.Body, false, nil
	}

	codings := SplitContentCodings(encoding)
	if len(codings) != 1 {
		return response.Body, false, fmt.Errorf("不支持多层堆叠流式编码: %s", encoding)
	}

	singleCoding := codings[0]
	switch singleCoding {
	case "gzip", "x-gzip":
		gzipReader, err := gzip.NewReader(response.Body)
		if err != nil {
			return response.Body, false, fmt.Errorf("创建流式 gzip reader 失败: %w", err)
		}
		return &decompressReadCloser{
			reader:       gzipReader,
			sourceCloser: response.Body,
			readerCloser: gzipReader,
		}, true, nil
	case "deflate":
		flateReader := flate.NewReader(response.Body)
		return &decompressReadCloser{
			reader:       flateReader,
			sourceCloser: response.Body,
			readerCloser: flateReader,
		}, true, nil
	case "br":
		brotliReader := brotli.NewReader(response.Body)
		return &decompressReadCloser{
			reader:       brotliReader,
			sourceCloser: response.Body,
		}, true, nil
	case "zstd", "zst":
		zstdDecoder, err := zstd.NewReader(response.Body)
		if err != nil {
			return response.Body, false, fmt.Errorf("创建流式 zstd reader 失败: %w", err)
		}
		return &decompressReadCloser{
			reader:       zstdDecoder,
			sourceCloser: response.Body,
			customClose: func() {
				zstdDecoder.Close()
			},
		}, true, nil
	default:
		return response.Body, false, fmt.Errorf("%w: %s", ErrUnsupportedContentEncoding, singleCoding)
	}
}

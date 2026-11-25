package images

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/tidwall/gjson"
	"insistanan/api-proxy/internal/api/streams"
	"insistanan/api-proxy/internal/protocol/codec"
	"insistanan/api-proxy/internal/protocol/tokenusage"
	"insistanan/api-proxy/internal/protocol/types"
)

const nativeMetadataLimit = 64 << 10
const nativeStringLimit = 1024

// 旁路读取用量，保留原生图片响应的正文、编码和逐块写回行为。
type nativeUsageCapture struct {
	buffer    []byte
	modeKnown bool
	sse       bool
	overflow  bool
	limited   bool
	event     []byte
	skipEvent bool
	usage     *types.Usage
	inString  bool
	escaped   bool
	discarded bool
	stringAt  int
}

func captureNativeUsage(resp *http.Response, maxDecodedBytes int64) func() *types.Usage {
	sse := streams.IsEventStreamResponse(resp)
	capture := &nativeUsageCapture{modeKnown: sse, sse: sse}
	var tap io.Writer = capture
	finish := capture.result
	if codings := codec.SplitContentCodings(codec.GetContentEncoding(resp.Header)); len(codings) > 0 {
		reader, writer := io.Pipe()
		done := make(chan *types.Usage, 1)
		go func() {
			if err := capture.decode(reader, codings, maxDecodedBytes); err != nil {
				log.Printf("[Images-Usage] 读取压缩响应中的用量失败: %v", err)
			}
			done <- capture.result()
		}()
		tap = nativeUsagePipe{writer}
		finish = func() *types.Usage {
			_ = writer.Close()
			return <-done
		}
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.TeeReader(resp.Body, tap), resp.Body}
	return finish
}

// 解码失败只终止旁路。客户端仍收到原始响应体。
type nativeUsagePipe struct{ *io.PipeWriter }

func (p nativeUsagePipe) Write(data []byte) (int, error) {
	_, _ = p.PipeWriter.Write(data)
	return len(data), nil
}

func (c *nativeUsageCapture) decode(reader io.ReadCloser, codings []string, maxDecodedBytes int64) (err error) {
	defer func() { err = errors.Join(err, reader.Close()) }()
	if maxDecodedBytes <= 0 {
		return codec.ErrDecompressedBodyTooLarge
	}
	for i := len(codings) - 1; i >= 0; i-- {
		if codings[i] == "deflate" {
			buffered := bufio.NewReader(reader)
			header, err := buffered.Peek(2)
			if err != nil {
				return err
			}
			if header[0]&15 == 8 && (int(header[0])*256+int(header[1]))%31 == 0 {
				decoded, err := zlib.NewReader(buffered)
				if err != nil {
					return err
				}
				reader = nativeDecodedBody{ReadCloser: decoded, source: reader}
				continue
			}
			reader = struct {
				io.Reader
				io.Closer
			}{buffered, reader}
		}
		response := &http.Response{Body: reader, Header: http.Header{"Content-Encoding": {codings[i]}}}
		decoded, _, err := codec.WrapStreamReaderIfNeeded(response)
		if err != nil {
			return err
		}
		reader = decoded
	}
	n, err := io.Copy(c, io.LimitReader(reader, maxDecodedBytes+1))
	if n > maxDecodedBytes {
		return codec.ErrDecompressedBodyTooLarge
	}
	return err
}

type nativeDecodedBody struct {
	io.ReadCloser
	source io.Closer
}

func (r nativeDecodedBody) Close() error { return errors.Join(r.ReadCloser.Close(), r.source.Close()) }

func (c *nativeUsageCapture) Write(chunk []byte) (int, error) {
	n := len(chunk)
	if !c.modeKnown {
		chunk = bytes.TrimLeft(chunk, " \t\r\n")
		if len(chunk) == 0 {
			return n, nil
		}
		c.modeKnown = true
		c.sse = chunk[0] != '{' && chunk[0] != '['
	}
	if !c.sse {
		c.append(chunk)
		return n, nil
	}
	for len(chunk) > 0 {
		line, rest, complete := bytes.Cut(chunk, []byte{'\n'})
		c.append(line)
		if complete {
			c.readLine()
		}
		chunk = rest
	}
	return n, nil
}

func (c *nativeUsageCapture) append(data []byte) {
	// 图片 Base64 和长文本替换成空 JSON 字符串，保留结构及短字段。
	// 这样一个很大的图片字段也不会挤掉同一响应末尾的 usage。
	for len(data) > 0 && !c.overflow {
		if !c.inString {
			quote := bytes.IndexByte(data, '"')
			if quote < 0 {
				c.keep(data)
				return
			}
			c.keep(data[:quote+1])
			c.inString, c.discarded, c.stringAt = true, false, len(c.buffer)
			data = data[quote+1:]
			continue
		}
		if c.escaped {
			c.keepString(data[:1])
			c.escaped = false
			data = data[1:]
			continue
		}
		next := bytes.IndexAny(data, `"\`)
		if next < 0 {
			c.keepString(data)
			return
		}
		c.keepString(data[:next])
		if data[next] == '"' {
			c.keep(data[next : next+1])
			c.inString = false
		} else {
			c.keepString(data[next : next+1])
			c.escaped = true
		}
		data = data[next+1:]
	}
}

func (c *nativeUsageCapture) keepString(data []byte) {
	if c.overflow || c.discarded {
		return
	}
	if len(c.buffer)-c.stringAt+len(data) > nativeStringLimit {
		c.buffer = c.buffer[:c.stringAt]
		c.discarded = true
		return
	}
	c.keep(data)
}

func (c *nativeUsageCapture) keep(data []byte) {
	if c.overflow {
		return
	}
	if len(c.buffer)+len(data) > nativeMetadataLimit {
		c.overflow, c.limited = true, true
		c.buffer = nil
		return
	}
	c.buffer = append(c.buffer, data...)
}

func (c *nativeUsageCapture) readLine() {
	switch {
	case c.overflow:
		c.event, c.skipEvent = nil, true
	case len(bytes.TrimSpace(c.buffer)) == 0:
		c.finishEvent()
	case !c.skipEvent:
		if payload, ok := codec.ParseSSEDataLineBytes(c.buffer); ok {
			switch {
			case len(c.event) == 0:
				c.event, c.buffer = payload, nil
			case len(c.event)+len(payload)+1 > nativeMetadataLimit:
				c.event, c.skipEvent, c.limited = nil, true, true
			default:
				c.event = append(c.event, '\n')
				c.event = append(c.event, payload...)
			}
		}
	}
	c.buffer = c.buffer[:0]
	c.overflow, c.inString, c.escaped = false, false, false
}

func (c *nativeUsageCapture) finishEvent() {
	if !c.skipEvent && len(c.event) > 0 {
		c.readUsage(c.event)
	}
	c.event, c.skipEvent = nil, false
}

func (c *nativeUsageCapture) readUsage(body []byte) {
	field := gjson.GetBytes(body, "usage")
	if !field.Exists() || !json.Valid(body) {
		return
	}
	usage, err := tokenusage.ParseJSONUsage([]byte(field.Raw))
	if err != nil {
		log.Printf("[Images-Usage] 原生图片用量字段无效: %v", err)
		return
	}
	if usage != nil {
		c.usage = usage
	}
}

func (c *nativeUsageCapture) result() *types.Usage {
	if c.sse {
		c.readLine()
		c.finishEvent()
	} else if !c.overflow {
		c.readUsage(c.buffer)
	}
	if c.usage == nil && c.limited {
		log.Printf("[Images-Usage] 响应超过用量提取大小限制，已记为用量缺失")
	}
	return c.usage
}

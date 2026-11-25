package images

import (
	"github.com/insistanan/api-proxy/internal/protocol/codec"
	"github.com/insistanan/api-proxy/internal/protocol/contract"
	"github.com/tidwall/gjson"
)

func validateImagesStreamFormat(contentType string, body []byte) error {
	format := gjson.GetBytes(body, "response_format").String()
	if boundary, ok := codec.MultipartBoundary(contentType); ok {
		fields, err := codec.ReadMultipartTextFields(body, boundary, []string{"response_format"})
		if err != nil {
			return err
		}
		format = fields["response_format"]
	}
	if format == "url" {
		return contract.Unsupported("强制非流式的 Images SSE 适配只能交付 b64_json，不能交付 URL 图片")
	}
	return nil
}

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseImagesExtraParamsRejectsInvalidValues(t *testing.T) {
	_, err := ParseImagesExtraParams(map[string]string{"model": "x"})
	require.ErrorContains(t, err, "保留字段")
	_, err = ParseImagesExtraParams(map[string]string{"stream": "false"})
	require.ErrorContains(t, err, "保留字段")
	_, err = ParseImagesExtraParams(map[string]string{" ": "1"})
	require.ErrorContains(t, err, "不能为空")
	_, err = ParseImagesExtraParams(map[string]string{"a": "1", " a ": "2"})
	require.ErrorContains(t, err, "重复")
	_, err = ParseImagesExtraParams(map[string]string{"meta": "{"})
	require.ErrorContains(t, err, "不是合法 JSON")
}

func TestImagesExtraParamsUpdateNilKeepsEmptyClearsAndCloneIsolates(t *testing.T) {
	cm := writeTestConfig(t, `{"imagesUpstream":[{"name":"images","baseUrl":"https://api.example.com","apiKeys":["test-key"],"serviceType":"openai"}]}`)

	_, err := cm.UpdateChannel("images", 0, UpstreamUpdate{ImagesExtraParams: map[string]string{" watermark ": " false "}})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"watermark": "false"}, cm.GetConfig().ImagesUpstream[0].ImagesExtraParams)
	require.Equal(t, map[string]string{"watermark": "false"}, ChannelToDTO(&cm.GetConfig().ImagesUpstream[0], 0)["imagesExtraParams"])

	name := "renamed"
	_, err = cm.UpdateChannel("images", 0, UpstreamUpdate{Name: &name})
	require.NoError(t, err)
	require.Equal(t, "renamed", cm.GetConfig().ImagesUpstream[0].Name)
	require.Equal(t, map[string]string{"watermark": "false"}, cm.GetConfig().ImagesUpstream[0].ImagesExtraParams)

	_, err = cm.UpdateChannel("images", 0, UpstreamUpdate{ImagesExtraParams: map[string]string{"meta": "{"}})
	require.ErrorContains(t, err, "不是合法 JSON")
	require.Equal(t, map[string]string{"watermark": "false"}, cm.GetConfig().ImagesUpstream[0].ImagesExtraParams)

	_, err = cm.UpdateChannel("images", 0, UpstreamUpdate{ImagesExtraParams: map[string]string{}})
	require.NoError(t, err)
	require.Nil(t, cm.GetConfig().ImagesUpstream[0].ImagesExtraParams)

	src := &UpstreamConfig{ImagesExtraParams: map[string]string{"watermark": "false"}}
	cloned := src.Clone()
	cloned.ImagesExtraParams["watermark"] = "true"
	cloned.ImagesExtraParams["n"] = "1"
	require.Equal(t, map[string]string{"watermark": "false"}, src.ImagesExtraParams)
}

func TestImagesForceNonStreamUpdateNilKeepsAndFalseClears(t *testing.T) {
	cm := writeTestConfig(t, `{"imagesUpstream":[{"name":"images","baseUrl":"https://api.example.com","apiKeys":["test-key"],"serviceType":"openai"}]}`)
	require.False(t, cm.GetConfig().ImagesUpstream[0].ImagesForceNonStream)

	enabled := true
	_, err := cm.UpdateChannel("images", 0, UpstreamUpdate{ImagesForceNonStream: &enabled})
	require.NoError(t, err)
	require.True(t, cm.GetConfig().ImagesUpstream[0].ImagesForceNonStream)
	require.Equal(t, true, ChannelToDTO(&cm.GetConfig().ImagesUpstream[0], 0)["imagesForceNonStream"])

	name := "renamed"
	_, err = cm.UpdateChannel("images", 0, UpstreamUpdate{Name: &name})
	require.NoError(t, err)
	require.True(t, cm.GetConfig().ImagesUpstream[0].ImagesForceNonStream)

	disabled := false
	_, err = cm.UpdateChannel("images", 0, UpstreamUpdate{ImagesForceNonStream: &disabled})
	require.NoError(t, err)
	require.False(t, cm.GetConfig().ImagesUpstream[0].ImagesForceNonStream)
	require.Equal(t, false, ChannelToDTO(&cm.GetConfig().ImagesUpstream[0], 0)["imagesForceNonStream"])
}

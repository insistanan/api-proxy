package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImagesChannelProtocolValidationAndAtomicUpdates(t *testing.T) {
	cm := writeTestConfig(t, `{"imagesUpstream":[{"name":"images","baseUrl":"https://api.example.com","apiKeys":["test-key"],"serviceType":"openai"}]}`)
	responses := "responses"
	_, err := cm.UpdateImagesUpstream(0, UpstreamUpdate{ServiceType: &responses})
	require.ErrorContains(t, err, "Responses 主模型")
	require.Equal(t, "openai", cm.GetConfig().ImagesUpstream[0].ServiceType)

	mainModel := "  gpt-4.1  "
	_, err = cm.UpdateImagesUpstream(0, UpstreamUpdate{ServiceType: &responses, ResponsesModel: &mainModel})
	require.NoError(t, err)
	upstream := cm.GetConfig().ImagesUpstream[0]
	require.Equal(t, "responses", upstream.ServiceType)
	require.Equal(t, "gpt-4.1", upstream.ResponsesModel)
	require.Equal(t, "gpt-4.1", ChannelToDTO(&upstream, 0)["responsesModel"])

	empty := " "
	_, err = cm.UpdateImagesUpstream(0, UpstreamUpdate{ResponsesModel: &empty})
	require.Error(t, err)
	require.Equal(t, "gpt-4.1", cm.GetConfig().ImagesUpstream[0].ResponsesModel)

	_, err = cm.AddImagesUpstreamWithResult(UpstreamConfig{ServiceType: "claude"})
	require.Error(t, err)
	require.Len(t, cm.GetConfig().ImagesUpstream, 1)
}

func TestImagesOutputConfigPersistsKeepsAndClears(t *testing.T) {
	cm := writeTestConfig(t, `{"imagesUpstream":[{"name":"images","baseUrl":"https://api.example.com","apiKeys":["fixture"],"serviceType":"responses","responsesModel":"gpt-main"}]}`)
	format, count, duration := "gif", 9, 200
	_, err := cm.UpdateImagesUpstream(0, UpstreamUpdate{ImagesOutputFormat: &format, ImagesFrameCount: &count, ImagesFrameDurationMS: &duration})
	require.NoError(t, err)
	name := "GIF 渠道"
	_, err = cm.UpdateImagesUpstream(0, UpstreamUpdate{Name: &name})
	require.NoError(t, err)
	up := cm.GetConfig().ImagesUpstream[0]
	require.Equal(t, "gif", up.ImagesOutputFormat)
	require.Equal(t, 9, up.ImagesFrameCount)
	require.Equal(t, 200, up.ImagesFrameDurationMS)
	dto := ChannelToDTO(&up, 0)
	require.Equal(t, "gif", dto["imagesOutputFormat"])
	require.Equal(t, 9, dto["imagesFrameCount"])
	require.Equal(t, 200, dto["imagesFrameDurationMs"])
	path := cm.configFile
	require.NoError(t, cm.Close())
	reloaded, err := NewConfigManager(path)
	require.NoError(t, err)
	t.Cleanup(func() { reloaded.Close() })
	require.Equal(t, up, reloaded.GetConfig().ImagesUpstream[0])
	empty, zero := "", 0
	_, err = reloaded.UpdateImagesUpstream(0, UpstreamUpdate{ImagesOutputFormat: &empty, ImagesFrameCount: &zero, ImagesFrameDurationMS: &zero})
	require.NoError(t, err)
	cleared := reloaded.GetConfig().ImagesUpstream[0]
	require.Empty(t, cleared.ImagesOutputFormat)
	require.Zero(t, cleared.ImagesFrameCount)
	require.Zero(t, cleared.ImagesFrameDurationMS)
}

func TestImagesOutputConfigRejectsInvalidAtomicUpdates(t *testing.T) {
	cm := writeTestConfig(t, `{"imagesUpstream":[{"name":"images","baseUrl":"https://api.example.com","apiKeys":["fixture"],"serviceType":"responses","responsesModel":"gpt-main"}]}`)
	before := cm.GetConfig().ImagesUpstream[0]
	gif, png, bad := "gif", "png", "invalid"
	count, duration := 17, 2001
	for _, updates := range []UpstreamUpdate{
		{ImagesOutputFormat: &bad},
		{ImagesOutputFormat: &gif, ImagesFrameCount: &count},
		{ImagesOutputFormat: &gif, ImagesFrameDurationMS: &duration},
		{ImagesOutputFormat: &png, ImagesFrameCount: &count},
	} {
		_, err := cm.UpdateImagesUpstream(0, updates)
		require.Error(t, err)
		require.Equal(t, before, cm.GetConfig().ImagesUpstream[0])
	}
}

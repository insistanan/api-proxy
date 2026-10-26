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

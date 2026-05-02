package fullmodel

import (
	"context"
	"testing"

	"fullmodel/processmessage"
	"fullmodel/utils/fileop"

	"github.com/stretchr/testify/require"
)

func TestOpenWithConfigs(t *testing.T) {
	client, err := Open(WithConfigs(&fileop.BrainConfigs{
		Defaults: fileop.ModelConfig{
			Provider: "qwen",
			Region:   "cn-beijing",
			APIKey:   "test",
		},
	}))
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotEmpty(t, client.Capabilities())
}

func TestRunNilClient(t *testing.T) {
	var client *Client
	_, err := client.Run(context.Background(), processmessage.TextMessage{Text: "hello"})
	require.Error(t, err)
}

package processmessage

import (
	"testing"

	"github.com/xumi30/fullmodel/agent/brain"

	"github.com/stretchr/testify/require"
)

func TestBuildInputTextMessage(t *testing.T) {
	processor := NewProcessor(nil)

	input, err := processor.BuildInput(TextMessage{
		System: "be concise",
		Text:   "hello",
	}, Options{Model: "qwen-plus", Stream: true})

	require.NoError(t, err)
	require.Equal(t, brain.BrainModeText, input.Mode)
	require.Equal(t, "hello", input.Prompt)
	require.True(t, input.Options.Stream)
	require.Equal(t, "qwen-plus", input.Options.Model)
	require.Len(t, input.Messages, 2)
	require.Equal(t, "system", input.Messages[0].Role)
	require.Equal(t, "user", input.Messages[1].Role)
}

func TestBuildInputImageMessage(t *testing.T) {
	processor := NewProcessor(nil)

	input, err := processor.BuildInput(ImageMessage{
		Prompt: "describe it",
		Image:  brain.MediaResource{URL: "https://example.com/a.png"},
	}, Options{})

	require.NoError(t, err)
	require.Equal(t, brain.BrainModeImageUnderstand, input.Mode)
	require.Equal(t, "describe it", input.Prompt)
	require.Equal(t, "https://example.com/a.png", input.Media.Image.URL)
}

func TestBuildInputSpeechToTextRequiresAudioData(t *testing.T) {
	processor := NewProcessor(nil)

	_, err := processor.BuildInput(SpeechToTextMessage{
		Audio: brain.MediaResource{URL: "https://example.com/a.wav"},
	}, Options{})

	require.Error(t, err)
	require.Contains(t, err.Error(), "audio data")
}

func TestBuildInputImageEditAddsExtraImagesAsParts(t *testing.T) {
	processor := NewProcessor(nil)

	input, err := processor.BuildInput(ImageEditMessage{
		Prompt: "make it brighter",
		Images: []brain.MediaResource{
			{URL: "https://example.com/first.png"},
			{URL: "https://example.com/second.png"},
		},
	}, Options{})

	require.NoError(t, err)
	require.Equal(t, brain.BrainIMageGenerate, input.Mode)
	require.Equal(t, "https://example.com/first.png", input.Media.Image.URL)
	require.Len(t, input.Media.Parts, 1)
	require.NotNil(t, input.Media.Parts[0].ImageURL)
	require.Equal(t, "https://example.com/second.png", input.Media.Parts[0].ImageURL.URL)
}

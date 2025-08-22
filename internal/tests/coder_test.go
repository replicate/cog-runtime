package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
)

func TestPredictionDataclassCoderSucceeded(t *testing.T) {
	t.Parallel()
	if *legacyCog {
		t.Skip("legacy Cog does not support custom coder")
	}

	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "dataclass",
		predictorClass:   "Predictor",
	})
	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	input := map[string]any{
		"account": map[string]any{
			"id":          0,
			"name":        "John",
			"address":     map[string]any{"street": "Smith", "zip": 12345},
			"credentials": map[string]any{"password": "foo", "pubkey": b64encode("bar")},
		},
	}
	req := httpPredictionRequest(t, runtimeServer, server.PredictionRequest{Input: input})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var predictionResponse server.PredictionResponse
	err = json.Unmarshal(body, &predictionResponse)
	require.NoError(t, err)

	expectedOutput := map[string]any{
		"account": map[string]any{
			"id":          100.0,
			"name":        "JOHN",
			"address":     map[string]any{"street": "SMITH", "zip": 22345.0},
			"credentials": map[string]any{"password": "**********", "pubkey": b64encode("*bar*")},
		},
	}
	assert.Equal(t, expectedOutput, predictionResponse.Output)
	assert.Equal(t, server.PredictionSucceeded, predictionResponse.Status)
}

func TestPredictionChatCoderSucceeded(t *testing.T) {
	t.Parallel()
	if *legacyCog {
		t.Skip("legacy Cog does not support custom coder")
	}

	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "chat",
		predictorClass:   "Predictor",
	})
	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	input := map[string]any{"msg": map[string]any{"role": "assistant", "content": "bar"}}
	req := httpPredictionRequest(t, runtimeServer, server.PredictionRequest{Input: input})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var predictionResponse server.PredictionResponse
	err = json.Unmarshal(body, &predictionResponse)
	require.NoError(t, err)
	expectedOutput := map[string]any{"role": "assistant", "content": "*bar*"}
	assert.Equal(t, expectedOutput, predictionResponse.Output)
	assert.Equal(t, server.PredictionSucceeded, predictionResponse.Status)
}

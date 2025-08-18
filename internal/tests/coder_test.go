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
	// FIXME: stop using a global for determining "legacy"
	if *legacyCog {
		t.Skip("legacy Cog does not support custom coder")
	}

	runtimeServer := setupCogRuntimeServer(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "dataclass",
		predictorClass:   "Predictor",
	})
	hc := waitForSetupComplete(t, runtimeServer)
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	input := map[string]any{
		"account": map[string]any{
			"id":          0,
			"name":        "John",
			"address":     map[string]any{"street": "Smith", "zip": 12345},
			"credentials": map[string]any{"password": "foo", "pubkey": b64encode("bar")},
		},
	}
	req := httpPredictionRequest(t, runtimeServer, nil, server.PredictionRequest{Input: input})
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
	// FIXME: stop using a global for determining "legacy"
	if *legacyCog {
		t.Skip("legacy Cog does not support custom coder")
	}

	runtimeServer := setupCogRuntimeServer(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "chat",
		predictorClass:   "Predictor",
	})
	hc := waitForSetupComplete(t, runtimeServer)
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	input := map[string]any{"msg": map[string]any{"role": "assistant", "content": "bar"}}
	req := httpPredictionRequest(t, runtimeServer, nil, server.PredictionRequest{Input: input})
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

	// if *legacyCog {
	// 	// Compat: legacy Cog does not support custom coder
	// 	t.SkipNow()
	// }
	// ct := NewCogTest(t, "chat")
	// assert.NoError(t, ct.Start())

	// hc := ct.WaitForSetup()
	// assert.Equal(t, server.StatusReady.String(), hc.Status)
	// assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	// resp := ct.Prediction(map[string]any{"msg": map[string]any{"role": "assistant", "content": "bar"}})
	// output := map[string]any{"role": "assistant", "content": "*bar*"}
	// ct.AssertResponse(resp, server.PredictionSucceeded, output, "")

	// ct.Shutdown()
	// assert.NoError(t, ct.Cleanup())
}

package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/runner"
	"github.com/replicate/cog-runtime/internal/server"
)

func TestPredictionOutputSucceeded(t *testing.T) {
	t.Parallel()
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "output",
		predictorClass:   "Predictor",
	})
	waitForSetupComplete(t, runtimeServer, runner.StatusReady, runner.SetupSucceeded)

	input := map[string]any{"p": b64encode("bar")}
	req := httpPredictionRequest(t, runtimeServer, runner.PredictionRequest{Input: input})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var predictionResponse server.PredictionResponse
	err = json.Unmarshal(body, &predictionResponse)
	require.NoError(t, err)

	assert.Equal(t, runner.PredictionSucceeded, predictionResponse.Status)
	assert.Contains(t, predictionResponse.Logs, "reading input file\nwriting output file\n")
	var b64 string
	if *legacyCog {
		// Compat: different MIME type detection logic
		b64 = b64encodeLegacy("*bar*")
	} else {
		b64 = b64encode("*bar*")
	}
	expectedOutput := map[string]any{
		"path": b64,
		"text": "*bar*",
	}
	assert.Equal(t, expectedOutput, predictionResponse.Output)
}

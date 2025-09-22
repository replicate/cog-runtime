package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/runner"
)

func TestInputDefaults(t *testing.T) {
	t.Parallel()
	if *legacyCog {
		t.Skip("Mutable default validation is coglet specific.")
	}

	t.Run("mutable default fails", func(t *testing.T) {
		runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
			procedureMode:    false,
			explicitShutdown: false,
			uploadURL:        "",
			module:           "input_bad_mutable_default",
			predictorClass:   "Predictor",
		})

		// Wait for setup to complete, expecting it to fail due to mutable default
		hc := waitForSetupComplete(t, runtimeServer, runner.StatusSetupFailed, runner.SetupFailed)

		// Check that the setup logs contain the expected error message about mutable defaults
		assert.Contains(t, hc.Setup.Logs, "Mutable default [1, 2, 3] passed to Input()")
		assert.Contains(t, hc.Setup.Logs, "Use Input(default_factory=lambda: [1, 2, 3]) instead")
	})

	t.Run("immutable default succeeds", func(t *testing.T) {
		t.Parallel()

		runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
			procedureMode:    false,
			explicitShutdown: false,
			uploadURL:        "",
			module:           "input_immutable_default",
			predictorClass:   "Predictor",
		})

		// Wait for setup to complete successfully
		waitForSetupComplete(t, runtimeServer, runner.StatusReady, runner.SetupSucceeded)

		// Verify that the predictor actually works
		input := map[string]any{} // Use default
		req := httpPredictionRequest(t, runtimeServer, runner.PredictionRequest{Input: input})

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var prediction testHarnessResponse
		err = json.Unmarshal(body, &prediction)
		require.NoError(t, err)

		assert.Equal(t, runner.PredictionSucceeded, prediction.Status)
		assert.Equal(t, "message: hello world", prediction.Output)
	})

	t.Run("immutable default with overrided value succeeds", func(t *testing.T) {
		t.Parallel()

		runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
			procedureMode:    false,
			explicitShutdown: false,
			uploadURL:        "",
			module:           "input_immutable_default",
			predictorClass:   "Predictor",
		})

		waitForSetupComplete(t, runtimeServer, runner.StatusReady, runner.SetupSucceeded)

		// Test with custom input
		input := map[string]any{"message": "custom message"}
		req := httpPredictionRequest(t, runtimeServer, runner.PredictionRequest{Input: input})

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var prediction testHarnessResponse
		err = json.Unmarshal(body, &prediction)
		require.NoError(t, err)

		assert.Equal(t, runner.PredictionSucceeded, prediction.Status)
		assert.Equal(t, "message: custom message", prediction.Output)
	})
}

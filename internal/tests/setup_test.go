package tests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
)

func TestSetupSucceeded(t *testing.T) {
	t.Parallel()
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "sleep",
		predictorClass:   "SetupSleepingPredictor",
	})
	hc := waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)
	assert.Equal(t, "starting setup\nsetup in progress 1/1\ncompleted setup\n", hc.Setup.Logs)

	resp, err := http.DefaultClient.Get(runtimeServer.URL + "/openapi.json")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestSetupFailure(t *testing.T) {
	t.Parallel()
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "sleep",
		predictorClass:   "SetupFailingPredictor",
	})
	hc := waitForSetupComplete(t, runtimeServer, server.StatusSetupFailed, server.SetupFailed)
	// FIXME: stop using a global for determining "legacy"
	if *legacyCog {
		// Compat: legacy Cog includes worker stacktrace
		assert.Contains(t, hc.Setup.Logs, "Predictor errored during setup: setup failed\n")
	} else {
		assert.Contains(t, hc.Setup.Logs, "starting setup\nsetup failed\nTraceback")
	}

}

func TestSetupCrash(t *testing.T) {
	t.Parallel()
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "sleep",
		predictorClass:   "SetupCrashingPredictor",
	})
	hc := waitForSetupComplete(t, runtimeServer, server.StatusSetupFailed, server.SetupFailed)
	// FIXME: stop using a global for determining "legacy"
	if *legacyCog {
		// Compat: legacy Cog includes worker stacktrace
		assert.Contains(t, hc.Setup.Logs, "Predictor errored during setup: 1\n")
	} else {
		assert.Equal(t, "starting setup\nsetup crashed\n", hc.Setup.Logs)
	}

}

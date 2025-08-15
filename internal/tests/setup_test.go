package tests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
)

func TestSetupSucceeded(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.AppendEnvs("SETUP_SLEEP=1")
	assert.NoError(t, ct.Start())
	assert.Equal(t, server.StatusStarting.String(), ct.HealthCheck().Status)

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)
	assert.Equal(t, "starting setup\nsetup in progress 1/1\ncompleted setup\n", hc.Setup.Logs)
	resp, err := http.DefaultClient.Get(ct.Url("/openapi.json"))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestSetupFailure(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.AppendArgs("--await-explicit-shutdown=true")
	ct.AppendEnvs("SETUP_FAILURE=1")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusSetupFailed.String(), hc.Status)
	assert.Equal(t, server.SetupFailed, hc.Setup.Status)
	if *legacyCog {
		// Compat: legacy Cog includes worker stacktrace
		assert.Contains(t, hc.Setup.Logs, "Predictor errored during setup: setup failed\n")
	} else {
		assert.Contains(t, hc.Setup.Logs, "starting setup\nsetup failed\nTraceback")
	}

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestSetupCrash(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.AppendArgs("--await-explicit-shutdown=true")
	ct.AppendEnvs("SETUP_CRASH=1")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusSetupFailed.String(), hc.Status)
	assert.Equal(t, server.SetupFailed, hc.Setup.Status)
	if *legacyCog {
		// Compat: legacy Cog includes worker stacktrace
		// Compat: "SystemExit: 1" parsing error?
		assert.Contains(t, hc.Setup.Logs, "Predictor errored during setup: 1\n")
	} else {
		assert.Equal(t, "starting setup\nsetup crashed\n", hc.Setup.Logs)
	}

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

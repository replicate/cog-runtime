package tests

import (
	"net/http"
	"testing"

	"github.com/replicate/go/must"

	"github.com/stretchr/testify/assert"
)

func TestSetupSucceeded(t *testing.T) {
	e := NewCogTest(t, "sleep")
	e.AppendEnvs("SETUP_SLEEP=1")
	assert.NoError(t, e.Start())
	assert.Equal(t, "STARTING", e.HealthCheck().Status)

	hc := e.WaitForSetup()
	assert.Equal(t, "READY", hc.Status)
	assert.Equal(t, "succeeded", hc.Setup.Status)
	assert.Equal(t, "starting setup\nsetup in progress 1/1\ncompleted setup\n", hc.Setup.Logs)
	assert.Equal(t, http.StatusOK, must.Get(http.DefaultClient.Get(e.Url("/openapi.json"))).StatusCode)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

func TestSetupFailure(t *testing.T) {
	e := NewCogTest(t, "sleep")
	e.AppendArgs("--await-explicit-shutdown=true")
	e.AppendEnvs("SETUP_FAILURE=1")
	assert.NoError(t, e.Start())

	hc := e.WaitForSetup()
	assert.Equal(t, "SETUP_FAILED", hc.Status)
	assert.Equal(t, "failed", hc.Setup.Status)
	assert.Equal(t, "starting setup\nsetup failed\n", hc.Setup.Logs)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

func TestSetupCrash(t *testing.T) {
	e := NewCogTest(t, "sleep")
	e.AppendArgs("--await-explicit-shutdown=true")
	e.AppendEnvs("SETUP_CRASH=1")
	assert.NoError(t, e.Start())

	hc := e.WaitForSetup()
	assert.Equal(t, "SETUP_FAILED", hc.Status)
	assert.Equal(t, "failed", hc.Setup.Status)
	assert.Equal(t, "starting setup\nsetup crashed\n", hc.Setup.Logs)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

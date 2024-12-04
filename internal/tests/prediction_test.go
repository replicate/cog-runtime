package tests

import (
	"testing"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestPredictionSucceeded(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{"i": 1, "s": "bar"})
	assert.Equal(t, server.PredictionSucceeded, resp.Status)
	assert.Equal(t, "*bar*", resp.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", resp.Logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionWithIdSucceeded(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.PredictionWithId("p01", map[string]any{"i": 1, "s": "bar"})
	assert.Equal(t, server.PredictionSucceeded, resp.Status)
	assert.Equal(t, "*bar*", resp.Output)
	assert.Equal(t, "p01", resp.Id)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", resp.Logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionFailure(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.AppendEnvs("PREDICTION_FAILURE=1")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{"i": 1, "s": "bar"})
	assert.Equal(t, server.PredictionFailed, resp.Status)
	assert.Equal(t, nil, resp.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\nprediction failed\n", resp.Logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionCrash(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.AppendArgs("--await-explicit-shutdown=true")
	ct.AppendEnvs("PREDICTION_CRASH=1")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{"i": 1, "s": "bar"})
	assert.Equal(t, server.PredictionFailed, resp.Status)
	assert.Equal(t, nil, resp.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\nprediction crashed\n", resp.Logs)
	assert.Equal(t, "DEFUNCT", ct.HealthCheck().Status)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPredictionSucceeded(t *testing.T) {
	e := NewCogTest(t, "sleep")
	assert.NoError(t, e.Start())

	hc := e.WaitForSetup()
	assert.Equal(t, "READY", hc.Status)
	assert.Equal(t, "succeeded", hc.Setup.Status)

	resp := e.Prediction(map[string]any{"i": 1, "s": "bar"})
	assert.Equal(t, "succeeded", resp.Status)
	assert.Equal(t, "*bar*", resp.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", resp.Logs)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

func TestPredictionWithIdSucceeded(t *testing.T) {
	e := NewCogTest(t, "sleep")
	assert.NoError(t, e.Start())

	hc := e.WaitForSetup()
	assert.Equal(t, "READY", hc.Status)
	assert.Equal(t, "succeeded", hc.Setup.Status)

	resp := e.PredictionWithId("p01", map[string]any{"i": 1, "s": "bar"})
	assert.Equal(t, "succeeded", resp.Status)
	assert.Equal(t, "*bar*", resp.Output)
	assert.Equal(t, "p01", resp.Id)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", resp.Logs)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

func TestPredictionFailure(t *testing.T) {
	e := NewCogTest(t, "sleep")
	e.AppendEnvs("PREDICTION_FAILURE=1")
	assert.NoError(t, e.Start())

	hc := e.WaitForSetup()
	assert.Equal(t, "READY", hc.Status)
	assert.Equal(t, "succeeded", hc.Setup.Status)

	resp := e.Prediction(map[string]any{"i": 1, "s": "bar"})
	assert.Equal(t, "failed", resp.Status)
	assert.Equal(t, nil, resp.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\nprediction failed\n", resp.Logs)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

func TestPredictionCrash(t *testing.T) {
	e := NewCogTest(t, "sleep")
	e.AppendArgs("--await-explicit-shutdown=true")
	e.AppendEnvs("PREDICTION_CRASH=1")
	assert.NoError(t, e.Start())

	hc := e.WaitForSetup()
	assert.Equal(t, "READY", hc.Status)
	assert.Equal(t, "succeeded", hc.Setup.Status)

	resp := e.Prediction(map[string]any{"i": 1, "s": "bar"})
	assert.Equal(t, "failed", resp.Status)
	assert.Equal(t, nil, resp.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\nprediction crashed\n", resp.Logs)
	assert.Equal(t, "DEFUNCT", e.HealthCheck().Status)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

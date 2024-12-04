package tests

import (
	"testing"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func assertResponse(
	t *testing.T,
	response server.PredictionResponse,
	status server.PredictionStatus,
	output any,
	logs string) {
	assert.Equal(t, status, response.Status)
	assert.Equal(t, output, response.Output)
	assert.Equal(t, logs, response.Logs)
}

func TestAsyncPredictionSucceeded(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	assert.NoError(t, ct.Start())
	ct.StartWebhook()

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	wr := ct.WaitForWebhookResponses()
	logs := "starting prediction\nprediction in progress 1/1\n"
	assertResponse(t, wr[0], server.PredictionStarting, nil, logs)
	logs += "completed prediction\n"
	assertResponse(t, wr[1], server.PredictionProcessing, nil, logs)
	assertResponse(t, wr[2], server.PredictionSucceeded, "*bar*", logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestAsyncPredictionWithIdSucceeded(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	assert.NoError(t, ct.Start())
	ct.StartWebhook()

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPredictionWithId("p01", map[string]any{"i": 1, "s": "bar"})
	wr := ct.WaitForWebhookResponses()
	logs := "starting prediction\nprediction in progress 1/1\n"
	assertResponse(t, wr[0], server.PredictionStarting, nil, logs)
	logs += "completed prediction\n"
	assertResponse(t, wr[1], server.PredictionProcessing, nil, logs)
	assertResponse(t, wr[2], server.PredictionSucceeded, "*bar*", logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestAsyncPredictionFailure(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.AppendEnvs("PREDICTION_FAILURE=1")
	assert.NoError(t, ct.Start())
	ct.StartWebhook()

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	wr := ct.WaitForWebhookResponses()
	logs := "starting prediction\nprediction in progress 1/1\n"
	assertResponse(t, wr[0], server.PredictionStarting, nil, logs)
	logs += "prediction failed\n"
	assertResponse(t, wr[1], server.PredictionProcessing, nil, logs)
	assertResponse(t, wr[2], server.PredictionFailed, nil, logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestAsyncPredictionCrash(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.AppendArgs("--await-explicit-shutdown=true")
	ct.AppendEnvs("PREDICTION_CRASH=1")
	assert.NoError(t, ct.Start())
	ct.StartWebhook()

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	wr := ct.WaitForWebhookResponses()
	logs := "starting prediction\nprediction in progress 1/1\n"
	assertResponse(t, wr[0], server.PredictionStarting, nil, logs)
	logs += "prediction crashed\n"
	assertResponse(t, wr[1], server.PredictionProcessing, nil, logs)
	assertResponse(t, wr[2], server.PredictionFailed, nil, logs)

	assert.Equal(t, "DEFUNCT", ct.HealthCheck().Status)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

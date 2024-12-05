package tests

import (
	"testing"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestAsyncPredictionSucceeded(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	wr := ct.WaitForWebhookResponses()
	logs := ""
	ct.AssertResponse(wr[0], server.PredictionStarting, nil, logs)
	logs += "starting prediction\n"
	ct.AssertResponse(wr[1], server.PredictionProcessing, nil, logs)
	logs += "prediction in progress 1/1\n"
	ct.AssertResponse(wr[2], server.PredictionProcessing, nil, logs)
	logs += "completed prediction\n"
	ct.AssertResponse(wr[3], server.PredictionProcessing, nil, logs)
	ct.AssertResponse(wr[4], server.PredictionSucceeded, "*bar*", logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestAsyncPredictionWithIdSucceeded(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPredictionWithId("p01", map[string]any{"i": 1, "s": "bar"})
	wr := ct.WaitForWebhookResponses()
	logs := ""
	ct.AssertResponse(wr[0], server.PredictionStarting, nil, logs)
	logs += "starting prediction\n"
	ct.AssertResponse(wr[1], server.PredictionProcessing, nil, logs)
	logs += "prediction in progress 1/1\n"
	ct.AssertResponse(wr[2], server.PredictionProcessing, nil, logs)
	logs += "completed prediction\n"
	ct.AssertResponse(wr[3], server.PredictionProcessing, nil, logs)
	ct.AssertResponse(wr[4], server.PredictionSucceeded, "*bar*", logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestAsyncPredictionFailure(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.StartWebhook()
	ct.AppendEnvs("PREDICTION_FAILURE=1")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	wr := ct.WaitForWebhookResponses()
	logs := ""
	ct.AssertResponse(wr[0], server.PredictionStarting, nil, logs)
	logs += "starting prediction\n"
	ct.AssertResponse(wr[1], server.PredictionProcessing, nil, logs)
	logs += "prediction in progress 1/1\n"
	ct.AssertResponse(wr[2], server.PredictionProcessing, nil, logs)
	logs += "prediction failed\n"
	ct.AssertResponse(wr[3], server.PredictionProcessing, nil, logs)
	ct.AssertResponse(wr[4], server.PredictionFailed, nil, logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestAsyncPredictionCrash(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.StartWebhook()
	ct.AppendArgs("--await-explicit-shutdown=true")
	ct.AppendEnvs("PREDICTION_CRASH=1")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	wr := ct.WaitForWebhookResponses()
	logs := ""
	ct.AssertResponse(wr[0], server.PredictionStarting, nil, logs)
	logs += "starting prediction\n"
	ct.AssertResponse(wr[1], server.PredictionProcessing, nil, logs)
	logs += "prediction in progress 1/1\n"
	ct.AssertResponse(wr[2], server.PredictionProcessing, nil, logs)
	logs += "prediction crashed\n"
	ct.AssertResponse(wr[3], server.PredictionProcessing, nil, logs)
	ct.AssertResponse(wr[4], server.PredictionFailed, nil, logs)

	assert.Equal(t, "DEFUNCT", ct.HealthCheck().Status)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

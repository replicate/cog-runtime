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
	if *legacyCog {
		assert.Len(t, wr, 3)
		logs := ""
		// Compat: legacy Cog sends no "starting" event
		ct.AssertResponse(wr[0], server.PredictionProcessing, nil, logs)
		// Compat: legacy Cog buffers logging?
		logs += "starting prediction\n"
		ct.AssertResponse(wr[1], server.PredictionProcessing, "*bar*", logs)
		logs += "prediction in progress 1/1\n"
		logs += "completed prediction\n"
		ct.AssertResponse(wr[2], server.PredictionSucceeded, "*bar*", logs)
	} else {
		assert.Len(t, wr, 5)
		logs := ""
		ct.AssertResponse(wr[0], server.PredictionStarting, nil, logs)
		logs += "starting prediction\n"
		ct.AssertResponse(wr[1], server.PredictionProcessing, nil, logs)
		logs += "prediction in progress 1/1\n"
		ct.AssertResponse(wr[2], server.PredictionProcessing, nil, logs)
		logs += "completed prediction\n"
		ct.AssertResponse(wr[3], server.PredictionProcessing, nil, logs)
		ct.AssertResponse(wr[4], server.PredictionSucceeded, "*bar*", logs)
	}

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
	if *legacyCog {
		assert.Len(t, wr, 3)
		logs := ""
		// Compat: legacy Cog sends no "starting" event
		ct.AssertResponse(wr[0], server.PredictionProcessing, nil, logs)
		// Compat: legacy Cog buffers logging?
		logs += "starting prediction\n"
		ct.AssertResponse(wr[1], server.PredictionProcessing, "*bar*", logs)
		logs += "prediction in progress 1/1\n"
		logs += "completed prediction\n"
		ct.AssertResponse(wr[2], server.PredictionSucceeded, "*bar*", logs)
	} else {
		assert.Len(t, wr, 5)
		logs := ""
		ct.AssertResponse(wr[0], server.PredictionStarting, nil, logs)
		logs += "starting prediction\n"
		ct.AssertResponse(wr[1], server.PredictionProcessing, nil, logs)
		logs += "prediction in progress 1/1\n"
		ct.AssertResponse(wr[2], server.PredictionProcessing, nil, logs)
		logs += "completed prediction\n"
		ct.AssertResponse(wr[3], server.PredictionProcessing, nil, logs)
		ct.AssertResponse(wr[4], server.PredictionSucceeded, "*bar*", logs)
	}

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
	if *legacyCog {
		assert.Len(t, wr, 3)
		logs := ""
		// Compat: legacy Cog sends no "starting" event
		ct.AssertResponse(wr[0], server.PredictionProcessing, nil, logs)
		assert.Equal(t, server.PredictionProcessing, wr[1].Status)
		assert.Equal(t, nil, wr[1].Output)
		// Compat: legacy Cog includes worker stacktrace
		assert.Contains(t, wr[1].Logs, "Traceback")
		// Compat: legacy Cog buffers logging?
		logs += "starting prediction\n"
		logs += "prediction in progress 1/1\n"
		logs += "prediction failed\n"
		assert.Equal(t, server.PredictionFailed, wr[2].Status)
		assert.Equal(t, nil, wr[2].Output)
		// Compat: legacy Cog includes worker stacktrace
		assert.Contains(t, wr[2].Logs, "Traceback")
		assert.Contains(t, wr[2].Logs, logs)
		assert.Equal(t, "prediction failed", wr[2].Error)
	} else {
		assert.Len(t, wr, 5)
		logs := ""
		ct.AssertResponse(wr[0], server.PredictionStarting, nil, logs)
		logs += "starting prediction\n"
		ct.AssertResponse(wr[1], server.PredictionProcessing, nil, logs)
		logs += "prediction in progress 1/1\n"
		ct.AssertResponse(wr[2], server.PredictionProcessing, nil, logs)
		logs += "prediction failed\n"
		ct.AssertResponse(wr[3], server.PredictionProcessing, nil, logs)
		ct.AssertResponse(wr[4], server.PredictionFailed, nil, logs)
		assert.Equal(t, "prediction failed", wr[4].Error)
	}

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
	if *legacyCog {
		assert.Len(t, wr, 3)
		logs := ""
		// Compat: legacy Cog sends no "starting" event
		ct.AssertResponse(wr[0], server.PredictionProcessing, nil, logs)
		assert.Equal(t, server.PredictionProcessing, wr[1].Status)
		assert.Equal(t, nil, wr[1].Output)
		// Compat: legacy Cog includes worker stacktrace
		assert.Contains(t, wr[1].Logs, "Traceback")
		// Compat: legacy Cog buffers logging?
		logs += "starting prediction\n"
		logs += "prediction in progress 1/1\n"
		logs += "prediction crashed\n"
		assert.Equal(t, server.PredictionFailed, wr[2].Status)
		assert.Equal(t, nil, wr[2].Output)
		// Compat: legacy Cog includes worker stacktrace
		assert.Contains(t, wr[2].Logs, "Traceback")
		assert.Contains(t, wr[2].Logs, logs)
		// Compat: legacy Cog cannot handle worker crash
		errMsg := "Prediction failed for an unknown reason. It might have run out of memory? (exitcode 1)"
		assert.Equal(t, errMsg, wr[2].Error)
		assert.Equal(t, "DEFUNCT", ct.HealthCheck().Status)
	} else {
		assert.Len(t, wr, 5)
		logs := ""
		ct.AssertResponse(wr[0], server.PredictionStarting, nil, logs)
		logs += "starting prediction\n"
		ct.AssertResponse(wr[1], server.PredictionProcessing, nil, logs)
		logs += "prediction in progress 1/1\n"
		ct.AssertResponse(wr[2], server.PredictionProcessing, nil, logs)
		logs += "prediction crashed\n"
		ct.AssertResponse(wr[3], server.PredictionProcessing, nil, logs)
		assert.Equal(t, server.PredictionFailed, wr[4].Status)
		assert.Equal(t, nil, wr[4].Output)
		assert.Contains(t, wr[4].Logs, logs)
		assert.Contains(t, wr[4].Logs, "SystemExit: 1\n")
		assert.Equal(t, "prediction failed", wr[4].Error)
		assert.Equal(t, "DEFUNCT", ct.HealthCheck().Status)
	}

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

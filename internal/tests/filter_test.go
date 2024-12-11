package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/replicate/cog-runtime/internal/server"
)

func TestPredictionFilterAll(t *testing.T) {
	ct := NewCogTest(t, "iterator")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPredictionWithFilter(map[string]any{"i": 2, "s": "bar"}, []server.WebhookEvent{
		server.WebhookStart,
		server.WebhookOutput,
		server.WebhookLogs,
		server.WebhookCompleted,
	})
	wr := ct.WaitForWebhookCompletion()
	if *legacyCog {
		assert.Len(t, wr, 5)
		logs := ""
		// Compat: legacy Cog sends no "starting" event
		ct.AssertResponse(wr[0], server.PredictionProcessing, nil, logs)
		ct.AssertResponse(wr[1], server.PredictionProcessing, []any{"*bar-0*"}, logs)
		ct.AssertResponse(wr[2], server.PredictionProcessing, []any{"*bar-0*", "*bar-1*"}, logs)
		// Compat: legacy Cog buffers logging?
		logs += "starting prediction\n"
		ct.AssertResponse(wr[3], server.PredictionProcessing, []any{"*bar-0*", "*bar-1*"}, logs)
		logs += "prediction in progress 1/2\n"
		logs += "prediction in progress 2/2\n"
		logs += "completed prediction\n"
		ct.AssertResponse(wr[4], server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)
	} else {
		assert.Len(t, wr, 8)
		logs := ""
		ct.AssertResponse(wr[0], server.PredictionStarting, nil, logs)
		logs += "starting prediction\n"
		ct.AssertResponse(wr[1], server.PredictionProcessing, nil, logs)
		logs += "prediction in progress 1/2\n"
		ct.AssertResponse(wr[2], server.PredictionProcessing, nil, logs)
		ct.AssertResponse(wr[3], server.PredictionProcessing, []any{"*bar-0*"}, logs)
		logs += "prediction in progress 2/2\n"
		ct.AssertResponse(wr[4], server.PredictionProcessing, []any{"*bar-0*"}, logs)
		ct.AssertResponse(wr[5], server.PredictionProcessing, []any{"*bar-0*", "*bar-1*"}, logs)
		logs += "completed prediction\n"
		ct.AssertResponse(wr[6], server.PredictionProcessing, []any{"*bar-0*", "*bar-1*"}, logs)
		ct.AssertResponse(wr[7], server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)
	}

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionFilterCompleted(t *testing.T) {
	ct := NewCogTest(t, "iterator")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPredictionWithFilter(map[string]any{"i": 2, "s": "bar"}, []server.WebhookEvent{
		server.WebhookCompleted,
	})
	wr := ct.WaitForWebhookCompletion()
	assert.Len(t, wr, 1)
	logs := ""
	logs += "starting prediction\n"
	logs += "prediction in progress 1/2\n"
	logs += "prediction in progress 2/2\n"
	logs += "completed prediction\n"
	ct.AssertResponse(wr[0], server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionFilterStartedCompleted(t *testing.T) {
	ct := NewCogTest(t, "iterator")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPredictionWithFilter(map[string]any{"i": 2, "s": "bar"}, []server.WebhookEvent{
		server.WebhookStart,
		server.WebhookCompleted,
	})
	wr := ct.WaitForWebhookCompletion()
	assert.Len(t, wr, 2)
	logs := ""
	if *legacyCog {
		// Compat: legacy Cog sends no "starting" event
		ct.AssertResponse(wr[0], server.PredictionProcessing, nil, logs)
	} else {
		ct.AssertResponse(wr[0], server.PredictionStarting, nil, logs)
	}
	logs += "starting prediction\n"
	logs += "prediction in progress 1/2\n"
	logs += "prediction in progress 2/2\n"
	logs += "completed prediction\n"
	ct.AssertResponse(wr[1], server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionFilterOutput(t *testing.T) {
	ct := NewCogTest(t, "iterator")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPredictionWithFilter(map[string]any{"i": 2, "s": "bar"}, []server.WebhookEvent{
		server.WebhookOutput,
		server.WebhookCompleted,
	})
	wr := ct.WaitForWebhookCompletion()
	if *legacyCog {
		assert.Len(t, wr, 3)
		logs := ""
		// Compat: legacy Cog sends no "starting" event
		ct.AssertResponse(wr[0], server.PredictionProcessing, []any{"*bar-0*"}, logs)
		ct.AssertResponse(wr[1], server.PredictionProcessing, []any{"*bar-0*", "*bar-1*"}, logs)
		// Compat: legacy Cog buffers logging?
		logs += "starting prediction\n"
		logs += "prediction in progress 1/2\n"
		logs += "prediction in progress 2/2\n"
		logs += "completed prediction\n"
		ct.AssertResponse(wr[2], server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)
	} else {
		assert.Len(t, wr, 3)
		logs := ""
		logs += "starting prediction\n"
		logs += "prediction in progress 1/2\n"
		ct.AssertResponse(wr[0], server.PredictionProcessing, []any{"*bar-0*"}, logs)
		logs += "prediction in progress 2/2\n"
		ct.AssertResponse(wr[1], server.PredictionProcessing, []any{"*bar-0*", "*bar-1*"}, logs)
		logs += "completed prediction\n"
		ct.AssertResponse(wr[2], server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)
	}

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionFilterLogs(t *testing.T) {
	ct := NewCogTest(t, "iterator")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPredictionWithFilter(map[string]any{"i": 2, "s": "bar"}, []server.WebhookEvent{
		server.WebhookLogs,
		server.WebhookCompleted,
	})
	wr := ct.WaitForWebhookCompletion()
	if *legacyCog {
		assert.Len(t, wr, 2)
		logs := ""
		logs += "starting prediction\n"
		ct.AssertResponse(wr[0], server.PredictionProcessing, []any{"*bar-0*", "*bar-1*"}, logs)
		logs += "prediction in progress 1/2\n"
		logs += "prediction in progress 2/2\n"
		logs += "completed prediction\n"
		ct.AssertResponse(wr[1], server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)

	} else {
		assert.Len(t, wr, 5)
		logs := ""
		logs += "starting prediction\n"
		ct.AssertResponse(wr[0], server.PredictionProcessing, nil, logs)
		logs += "prediction in progress 1/2\n"
		ct.AssertResponse(wr[1], server.PredictionProcessing, nil, logs)
		logs += "prediction in progress 2/2\n"
		ct.AssertResponse(wr[2], server.PredictionProcessing, []any{"*bar-0*"}, logs)
		logs += "completed prediction\n"
		ct.AssertResponse(wr[3], server.PredictionProcessing, []any{"*bar-0*", "*bar-1*"}, logs)
		ct.AssertResponse(wr[4], server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)
	}

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

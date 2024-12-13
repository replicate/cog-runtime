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
		// Compat: legacy Cog buffers logging?
		assert.Len(t, wr, 7)
	} else {
		assert.Len(t, wr, 8)
	}
	logs := "starting prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted prediction\n"
	ct.AssertResponses(wr, server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)

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
	logs := "starting prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted prediction\n"
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
	ct.AssertResponse(wr[0], server.PredictionProcessing, nil, "")
	logs := "starting prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted prediction\n"
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

	assert.Len(t, wr, 3)
	logs := "starting prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted prediction\n"
	ct.AssertResponses(wr, server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)

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
	assert.Len(t, wr, 5)
	logs := "starting prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted prediction\n"
	ct.AssertResponses(wr, server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

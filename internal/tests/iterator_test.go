package tests

import (
	"testing"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestPredictionIteratorSucceeded(t *testing.T) {
	ct := NewCogTest(t, "iterator")
	assert.NoError(t, ct.Start())
	ct.StartWebhook()

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 2, "s": "bar"})
	wr := ct.WaitForWebhookResponses()
	logs := "starting prediction\nprediction in progress 1/2\n"
	assertResponse(t, wr[0], server.PredictionStarting, nil, logs)
	logs += "prediction in progress 2/2\n"
	assertResponse(t, wr[1], server.PredictionProcessing, nil, logs)
	logs += "completed prediction\n"
	assertResponse(t, wr[2], server.PredictionProcessing, []any{"*bar-0*"}, logs)
	assertResponse(t, wr[3], server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionConcatenateIteratorSucceeded(t *testing.T) {
	ct := NewCogTest(t, "concat_iterator")
	assert.NoError(t, ct.Start())
	ct.StartWebhook()

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 2, "s": "bar"})
	wr := ct.WaitForWebhookResponses()
	logs := "starting prediction\nprediction in progress 1/2\n"
	assertResponse(t, wr[0], server.PredictionStarting, nil, logs)
	logs += "prediction in progress 2/2\n"
	assertResponse(t, wr[1], server.PredictionProcessing, nil, logs)
	logs += "completed prediction\n"
	assertResponse(t, wr[2], server.PredictionProcessing, []any{"*bar-0*"}, logs)
	assertResponse(t, wr[3], server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

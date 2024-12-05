package tests

import (
	"testing"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestPredictionIteratorSucceeded(t *testing.T) {
	ct := NewCogTest(t, "iterator")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 2, "s": "bar"})
	wr := ct.WaitForWebhookResponses()
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

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionConcatenateIteratorSucceeded(t *testing.T) {
	ct := NewCogTest(t, "concat_iterator")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 2, "s": "bar"})
	wr := ct.WaitForWebhookResponses()
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

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

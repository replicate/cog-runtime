package tests

import (
	"testing"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestAsyncPredictorSucceeded(t *testing.T) {
	if *legacyCog {
		t.SkipNow()
	}
	ct := NewCogTest(t, "async_sleep")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	barId := ct.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	bazId := ct.AsyncPrediction(map[string]any{"i": 2, "s": "baz"})
	wr := ct.WaitForWebhookResponses()
	var barR []server.PredictionResponse
	var bazR []server.PredictionResponse
	for _, r := range wr {
		if r.Id == barId {
			barR = append(barR, r)
		} else if r.Id == bazId {
			bazR = append(bazR, r)
		}
	}
	assert.Len(t, barR, 5)
	assert.Len(t, bazR, 6)

	barLogs := ""
	ct.AssertResponse(barR[0], server.PredictionStarting, nil, barLogs)
	barLogs += "starting async prediction\n"
	ct.AssertResponse(barR[1], server.PredictionProcessing, nil, barLogs)
	barLogs += "prediction in progress 1/1\n"
	ct.AssertResponse(barR[2], server.PredictionProcessing, nil, barLogs)
	barLogs += "completed async prediction\n"
	ct.AssertResponse(barR[3], server.PredictionProcessing, nil, barLogs)
	ct.AssertResponse(barR[4], server.PredictionSucceeded, "*bar*", barLogs)

	bazLogs := ""
	ct.AssertResponse(bazR[0], server.PredictionStarting, nil, bazLogs)
	bazLogs += "starting async prediction\n"
	ct.AssertResponse(bazR[1], server.PredictionProcessing, nil, bazLogs)
	bazLogs += "prediction in progress 1/2\n"
	ct.AssertResponse(bazR[2], server.PredictionProcessing, nil, bazLogs)
	bazLogs += "prediction in progress 2/2\n"
	ct.AssertResponse(bazR[3], server.PredictionProcessing, nil, bazLogs)
	bazLogs += "completed async prediction\n"
	ct.AssertResponse(bazR[4], server.PredictionProcessing, nil, bazLogs)
	ct.AssertResponse(bazR[5], server.PredictionSucceeded, "*baz*", bazLogs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

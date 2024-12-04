package tests

import (
	"testing"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestAsyncPredictorSucceeded(t *testing.T) {
	ct := NewCogTest(t, "async_sleep")
	assert.NoError(t, ct.Start())
	ct.StartWebhook()

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
	assert.Equal(t, 3, len(barR))
	assert.Equal(t, 4, len(bazR))

	barLogs := "starting async prediction\nprediction in progress 1/1\n"
	assertResponse(t, barR[0], server.PredictionStarting, nil, barLogs)
	barLogs += "completed async prediction\n"
	assertResponse(t, barR[1], server.PredictionProcessing, nil, barLogs)
	assertResponse(t, barR[2], server.PredictionSucceeded, "*bar*", barLogs)

	bazLogs := "starting async prediction\nprediction in progress 1/2\n"
	assertResponse(t, bazR[0], server.PredictionStarting, nil, bazLogs)
	bazLogs += "prediction in progress 2/2\n"
	assertResponse(t, bazR[1], server.PredictionProcessing, nil, bazLogs)
	bazLogs += "completed async prediction\n"
	assertResponse(t, bazR[2], server.PredictionProcessing, nil, bazLogs)
	assertResponse(t, bazR[3], server.PredictionSucceeded, "*baz*", bazLogs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

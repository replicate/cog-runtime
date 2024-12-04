package tests

import (
	"net/http"
	"testing"
	"time"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestAsyncPredictorSucceeded(t *testing.T) {
	e := NewCogTest(t, "async_sleep")
	assert.NoError(t, e.Start())
	e.StartWebhook()

	hc := e.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	barId := e.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	bazId := e.AsyncPrediction(map[string]any{"i": 2, "s": "baz"})
	barDone, bazDone := false, false
	for {
		for _, wr := range e.WebhookRequests() {
			if wr.Response.Status == "succeeded" {
				if wr.Response.Id == barId {
					barDone = true
				} else if wr.Response.Id == bazId {
					bazDone = true
				}
			}
		}
		if barDone && bazDone {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	wr := e.WebhookRequests()
	var barR []server.PredictionResponse
	var bazR []server.PredictionResponse
	for _, r := range wr {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/webhook", r.Path)
		if r.Response.Id == barId {
			barR = append(barR, r.Response)
		} else if r.Response.Id == bazId {
			bazR = append(bazR, r.Response)
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

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

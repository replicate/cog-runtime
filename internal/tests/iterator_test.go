package tests

import (
	"net/http"
	"testing"
	"time"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestPredictionIteratorSucceeded(t *testing.T) {
	e := NewCogTest(t, "iterator")
	assert.NoError(t, e.Start())
	e.StartWebhook()

	hc := e.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	e.AsyncPrediction(map[string]any{"i": 2, "s": "bar"})
	for {
		if len(e.WebhookRequests()) == 4 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	wr := e.WebhookRequests()
	for _, r := range wr {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/webhook", r.Path)
	}
	logs := "starting prediction\nprediction in progress 1/2\n"
	assertResponse(t, wr[0].Response, server.PredictionStarting, nil, logs)
	logs += "prediction in progress 2/2\n"
	assertResponse(t, wr[1].Response, server.PredictionProcessing, nil, logs)
	logs += "completed prediction\n"
	assertResponse(t, wr[2].Response, server.PredictionProcessing, []any{"*bar-0*"}, logs)
	assertResponse(t, wr[3].Response, server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

func TestPredictionConcatenateIteratorSucceeded(t *testing.T) {
	e := NewCogTest(t, "concat_iterator")
	assert.NoError(t, e.Start())
	e.StartWebhook()

	hc := e.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	e.AsyncPrediction(map[string]any{"i": 2, "s": "bar"})
	for {
		if len(e.WebhookRequests()) == 4 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	wr := e.WebhookRequests()
	for _, r := range wr {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/webhook", r.Path)
	}
	logs := "starting prediction\nprediction in progress 1/2\n"
	assertResponse(t, wr[0].Response, server.PredictionStarting, nil, logs)
	logs += "prediction in progress 2/2\n"
	assertResponse(t, wr[1].Response, server.PredictionProcessing, nil, logs)
	logs += "completed prediction\n"
	assertResponse(t, wr[2].Response, server.PredictionProcessing, []any{"*bar-0*"}, logs)
	assertResponse(t, wr[3].Response, server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

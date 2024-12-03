package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func assertResponse(
	t *testing.T,
	response server.PredictionResponse,
	status server.PredictionStatus,
	output any,
	logs string) {
	assert.Equal(t, status, response.Status)
	assert.Equal(t, output, response.Output)
	assert.Equal(t, logs, response.Logs)
}

func TestAsyncPredictionSucceeded(t *testing.T) {
	e := NewCogTest(t, "sleep")
	assert.NoError(t, e.Start())
	e.StartWebhook()

	hc := e.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	e.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
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
	logs := "starting prediction\nprediction in progress 1/1\n"
	assertResponse(t, wr[0].Response, server.PredictionStarting, nil, logs)
	assertResponse(t, wr[1].Response, server.PredictionProcessing, nil, logs)
	assertResponse(t, wr[2].Response, server.PredictionProcessing, nil, logs)
	assertResponse(t, wr[3].Response, server.PredictionSucceeded, "*bar*", fmt.Sprintf("%scompleted prediction\n", logs))

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

func TestAsyncPredictionWithIdSucceeded(t *testing.T) {
	e := NewCogTest(t, "sleep")
	assert.NoError(t, e.Start())
	e.StartWebhook()

	hc := e.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	e.AsyncPredictionWithId("p01", map[string]any{"i": 1, "s": "bar"})
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
	logs := "starting prediction\nprediction in progress 1/1\n"
	assertResponse(t, wr[0].Response, server.PredictionStarting, nil, logs)
	assertResponse(t, wr[1].Response, server.PredictionProcessing, nil, logs)
	assertResponse(t, wr[2].Response, server.PredictionProcessing, nil, logs)
	assertResponse(t, wr[3].Response, server.PredictionSucceeded, "*bar*", fmt.Sprintf("%scompleted prediction\n", logs))

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

func TestAsyncPredictionFailure(t *testing.T) {
	e := NewCogTest(t, "sleep")
	e.AppendEnvs("PREDICTION_FAILURE=1")
	assert.NoError(t, e.Start())
	e.StartWebhook()

	hc := e.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	e.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
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
	logs := "starting prediction\nprediction in progress 1/1\n"
	assertResponse(t, wr[0].Response, server.PredictionStarting, nil, logs)
	assertResponse(t, wr[1].Response, server.PredictionProcessing, nil, logs)
	assertResponse(t, wr[2].Response, server.PredictionProcessing, nil, logs)
	assertResponse(t, wr[3].Response, server.PredictionFailed, nil, fmt.Sprintf("%sprediction failed\n", logs))

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

func TestAsyncPredictionCrash(t *testing.T) {
	e := NewCogTest(t, "sleep")
	e.AppendArgs("--await-explicit-shutdown=true")
	e.AppendEnvs("PREDICTION_CRASH=1")
	assert.NoError(t, e.Start())
	e.StartWebhook()

	hc := e.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	e.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	for {
		if len(e.WebhookRequests()) == 4 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	wr := e.WebhookRequests()
	for i, r := range wr {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/webhook", r.Path)
		if i == 0 {

		}
	}
	logs := "starting prediction\nprediction in progress 1/1\n"
	assertResponse(t, wr[0].Response, server.PredictionStarting, nil, logs)
	assertResponse(t, wr[1].Response, server.PredictionProcessing, nil, logs)
	assertResponse(t, wr[2].Response, server.PredictionProcessing, nil, logs)
	assertResponse(t, wr[3].Response, server.PredictionFailed, nil, fmt.Sprintf("%sprediction crashed\n", logs))

	assert.Equal(t, "DEFUNCT", e.HealthCheck().Status)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

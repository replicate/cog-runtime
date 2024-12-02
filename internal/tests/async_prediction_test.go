package tests

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAsyncPredictionSucceeded(t *testing.T) {
	e := NewCogTest(t, "sleep")
	assert.NoError(t, e.Start())
	e.StartWebhook()

	hc := e.WaitForSetup()
	assert.Equal(t, "READY", hc.Status)
	assert.Equal(t, "succeeded", hc.Setup.Status)

	e.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	for {
		if len(e.WebhookRequests()) == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	wr := e.WebhookRequests()
	for _, r := range wr {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/webhook", r.Path)
	}
	assert.Equal(t, "starting", wr[0].Response.Status)
	assert.Equal(t, nil, wr[0].Response.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\n", wr[0].Response.Logs)

	assert.Equal(t, "succeeded", wr[1].Response.Status)
	assert.Equal(t, "*bar*", wr[1].Response.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", wr[1].Response.Logs)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

func TestAsyncPredictionWithIdSucceeded(t *testing.T) {
	e := NewCogTest(t, "sleep")
	assert.NoError(t, e.Start())
	e.StartWebhook()

	hc := e.WaitForSetup()
	assert.Equal(t, "READY", hc.Status)
	assert.Equal(t, "succeeded", hc.Setup.Status)

	e.AsyncPredictionWithId("p01", map[string]any{"i": 1, "s": "bar"})
	for {
		if len(e.WebhookRequests()) == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	wr := e.WebhookRequests()
	for _, r := range wr {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/webhook", r.Path)
	}

	assert.Equal(t, "starting", wr[0].Response.Status)
	assert.Equal(t, nil, wr[0].Response.Output)
	assert.Equal(t, "p01", wr[0].Response.Id)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\n", wr[0].Response.Logs)

	assert.Equal(t, "succeeded", wr[1].Response.Status)
	assert.Equal(t, "*bar*", wr[1].Response.Output)
	assert.Equal(t, "p01", wr[1].Response.Id)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", wr[1].Response.Logs)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

func TestAsyncPredictionFailure(t *testing.T) {
	e := NewCogTest(t, "sleep")
	e.AppendEnvs("PREDICTION_FAILURE=1")
	assert.NoError(t, e.Start())
	e.StartWebhook()

	hc := e.WaitForSetup()
	assert.Equal(t, "READY", hc.Status)
	assert.Equal(t, "succeeded", hc.Setup.Status)

	e.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	for {
		if len(e.WebhookRequests()) == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	wr := e.WebhookRequests()
	for _, r := range wr {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/webhook", r.Path)
	}

	assert.Equal(t, "starting", wr[0].Response.Status)
	assert.Equal(t, nil, wr[0].Response.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\n", wr[0].Response.Logs)

	assert.Equal(t, "failed", wr[1].Response.Status)
	assert.Equal(t, nil, wr[1].Response.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\nprediction failed\n", wr[1].Response.Logs)

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
	assert.Equal(t, "READY", hc.Status)
	assert.Equal(t, "succeeded", hc.Setup.Status)

	e.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	for {
		if len(e.WebhookRequests()) == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	wr := e.WebhookRequests()
	for _, r := range wr {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/webhook", r.Path)
	}

	assert.Equal(t, "starting", wr[0].Response.Status)
	assert.Equal(t, nil, wr[0].Response.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\n", wr[0].Response.Logs)

	assert.Equal(t, "failed", wr[1].Response.Status)
	assert.Equal(t, nil, wr[1].Response.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\nprediction crashed\n", wr[1].Response.Logs)

	assert.Equal(t, "DEFUNCT", e.HealthCheck().Status)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

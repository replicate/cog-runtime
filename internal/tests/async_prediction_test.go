package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/replicate/cog-runtime/internal/util"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestAsyncPredictionSucceeded(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	wr := ct.WaitForWebhookCompletion()
	logs := "starting prediction\nprediction in progress 1/1\ncompleted prediction\n"
	ct.AssertResponses(wr, server.PredictionSucceeded, "*bar*", logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestAsyncPredictionWithIdSucceeded(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPredictionWithId("p01", map[string]any{"i": 1, "s": "bar"})
	wr := ct.WaitForWebhookCompletion()
	logs := "starting prediction\nprediction in progress 1/1\ncompleted prediction\n"
	ct.AssertResponses(wr, server.PredictionSucceeded, "*bar*", logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestAsyncPredictionFailure(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.StartWebhook()
	ct.AppendEnvs("PREDICTION_FAILURE=1")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	wr := ct.WaitForWebhookCompletion()
	logs := "starting prediction\nprediction in progress 1/1\nprediction failed\n"
	ct.AssertResponses(wr, server.PredictionFailed, nil, logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestAsyncPredictionCrash(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.StartWebhook()
	ct.AppendArgs("--await-explicit-shutdown=true")
	ct.AppendEnvs("PREDICTION_CRASH=1")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	wr := ct.WaitForWebhookCompletion()
	logs := "starting prediction\nprediction in progress 1/1\nprediction crashed\n"
	ct.AssertResponses(wr, server.PredictionFailed, nil, logs)
	if *legacyCog {
		assert.Equal(t, "Prediction failed for an unknown reason. It might have run out of memory? (exitcode 1)", wr[len(wr)-1].Error)
	} else {
		assert.Equal(t, "prediction failed", wr[len(wr)-1].Error)
	}
	assert.Equal(t, "DEFUNCT", ct.HealthCheck().Status)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestAsyncPredictionCanceled(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.StartWebhook()
	ct.AppendArgs("--await-explicit-shutdown=true")
	ct.AppendEnvs("PREDICTION_CRASH=1")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	pid := "p01"
	ct.AsyncPredictionWithId(pid, map[string]any{"i": 60, "s": "bar"})
	if *legacyCog {
		// Compat: legacy Cog does not send output webhook
		time.Sleep(time.Second)
	} else {
		ct.WaitForWebhook(func(response server.PredictionResponse) bool {
			return strings.Contains(response.Logs, "prediction in progress 1/60\n")
		})
	}
	ct.Cancel(pid)
	wr := ct.WaitForWebhookCompletion()
	logs := "starting prediction\nprediction in progress 1/60\nprediction canceled\n"
	ct.AssertResponses(wr, server.PredictionCanceled, nil, logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestAsyncPredictionConcurrency(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)
	if !*legacyCog {
		// Compat: not implemented in legacy Cog
		assert.Equal(t, 1, hc.Concurrency.Max)
		assert.Equal(t, 0, hc.Concurrency.Current)
	}

	ct.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	if !*legacyCog {
		// Compat: not implemented in legacy Cog
		hc = ct.HealthCheck()
		assert.Equal(t, 1, hc.Concurrency.Max)
		assert.Equal(t, 1, hc.Concurrency.Current)
	}

	// Fail prediction requests when one is in progress
	req := server.PredictionRequest{
		CreatedAt: util.NowIso(),
		Input:     map[string]any{"i": 1, "s": "baz"},
		Webhook:   fmt.Sprintf("http://localhost:%d/webhook", ct.webhookPort),
	}
	resp := ct.PredictionReq(http.MethodPost, "/predictions", req)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	wr := ct.WaitForWebhookCompletion()
	logs := "starting prediction\nprediction in progress 1/1\ncompleted prediction\n"
	ct.AssertResponses(wr, server.PredictionSucceeded, "*bar*", logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

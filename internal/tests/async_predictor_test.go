package tests

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
)

func TestAsyncPredictorConcurrency(t *testing.T) {
	ct := NewCogTest(t, "async_sleep")
	ct.AppendEnvs("TEST_COG_MAX_CONCURRENCY=2")
	ct.StartWebhook()
	require.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	barID := ct.AsyncPredictionWithID("p01", map[string]any{"i": 1, "s": "bar"})
	bazID := ct.AsyncPredictionWithID("p02", map[string]any{"i": 2, "s": "baz"})
	wr := ct.WaitForWebhookCompletion()
	var barR []server.PredictionResponse
	var bazR []server.PredictionResponse
	for _, r := range wr {
		switch r.ID {
		case barID:
			barR = append(barR, r)
		case bazID:
			bazR = append(bazR, r)
		}
	}
	barLogs := "starting async prediction\nprediction in progress 1/1\ncompleted async prediction\n"
	ct.AssertResponses(barR, server.PredictionSucceeded, "*bar*", barLogs)
	bazLogs := "starting async prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted async prediction\n"
	ct.AssertResponses(bazR, server.PredictionSucceeded, "*baz*", bazLogs)

	ct.Shutdown()
	require.NoError(t, ct.Cleanup())
}

func TestAsyncPredictorCanceled(t *testing.T) {
	if *legacyCog {
		// Cancellation bug as of 0.14.1
		// https://github.com/replicate/cog/issues/2212
		t.SkipNow()
	}
	ct := NewCogTest(t, "async_sleep")
	ct.StartWebhook()
	require.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	pid := "p01"
	ct.AsyncPredictionWithID(pid, map[string]any{"i": 60, "s": "bar"})
	ct.WaitForWebhook(func(response server.PredictionResponse) bool {
		return strings.Contains(response.Logs, "prediction in progress 1/60\n")
	})
	ct.Cancel(pid)
	wr := ct.WaitForWebhookCompletion()
	logs := "starting async prediction\nprediction in progress 1/60\nprediction canceled\n"
	ct.AssertResponses(wr, server.PredictionCanceled, nil, logs)

	ct.Shutdown()
	require.NoError(t, ct.Cleanup())
}

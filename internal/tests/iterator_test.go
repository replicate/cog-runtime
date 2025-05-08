package tests

import (
	"testing"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestPredictionIteratorSucceeded(t *testing.T) {
	testPredictionIteratorSucceeded(t, "iterator")
}

func TestPredictionConcatenateIteratorSucceeded(t *testing.T) {
	testPredictionIteratorSucceeded(t, "concat_iterator")
}

func TestPredictionAsyncIteratorSucceeded(t *testing.T) {
	if *legacyCog {
		// Compat: legacy Cog fails due to logging buffer?
		t.SkipNow()
	}
	testPredictionIteratorSucceeded(t, "async_iterator")
}

func testPredictionIteratorSucceeded(t *testing.T, module string) {
	ct := NewCogTest(t, module)
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"i": 2, "s": "bar"})
	wr := ct.WaitForWebhookCompletion()
	logs := "starting prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted prediction\n"
	ct.AssertResponses(wr, server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionAsyncIteratorConcurrency(t *testing.T) {
	if *legacyCog {
		// Compat: legacy Cog rejects concurrent prediction requests
		t.SkipNow()
	}
	ct := NewCogTest(t, "async_iterator")
	ct.AppendEnvs("TEST_COG_MAX_CONCURRENCY=2")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	barId := ct.AsyncPrediction(map[string]any{"i": 1, "s": "bar"})
	bazId := ct.AsyncPrediction(map[string]any{"i": 2, "s": "baz"})
	wr := ct.WaitForWebhookCompletion()
	var barR []server.PredictionResponse
	var bazR []server.PredictionResponse
	for _, r := range wr {
		if r.Id == barId {
			barR = append(barR, r)
		} else if r.Id == bazId {
			bazR = append(bazR, r)
		}
	}
	barLogs := "starting prediction\nprediction in progress 1/1\ncompleted prediction\n"
	ct.AssertResponses(barR, server.PredictionSucceeded, []any{"*bar-0*"}, barLogs)
	bazLogs := "starting prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted prediction\n"
	ct.AssertResponses(bazR, server.PredictionSucceeded, []any{"*baz-0*", "*baz-1*"}, bazLogs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

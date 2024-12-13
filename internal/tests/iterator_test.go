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
	if *legacyCog {
		assert.Len(t, wr, 5)
		logs := ""
		// Compat: legacy Cog sends no "starting" event
		ct.AssertResponse(wr[0], server.PredictionProcessing, nil, logs)
		ct.AssertResponse(wr[1], server.PredictionProcessing, []any{"*bar-0*"}, logs)
		ct.AssertResponse(wr[2], server.PredictionProcessing, []any{"*bar-0*", "*bar-1*"}, logs)
		// Compat: legacy Cog buffers logging?
		logs += "starting prediction\n"
		ct.AssertResponse(wr[3], server.PredictionProcessing, []any{"*bar-0*", "*bar-1*"}, logs)
		logs += "prediction in progress 1/2\n"
		logs += "prediction in progress 2/2\n"
		logs += "completed prediction\n"
		ct.AssertResponse(wr[4], server.PredictionSucceeded, []any{"*bar-0*", "*bar-1*"}, logs)
	} else {
		assert.Len(t, wr, 8)
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
	}

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionAsyncIteratorConcurrency(t *testing.T) {
	if *legacyCog {
		// Compat: legacy Cog rejects concurrent prediction requests
		t.SkipNow()
	}
	ct := NewCogTest(t, "async_iterator")
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
	assert.Len(t, barR, 6)
	barLogs := ""
	ct.AssertResponse(barR[0], server.PredictionStarting, nil, barLogs)
	barLogs += "starting prediction\n"
	ct.AssertResponse(barR[1], server.PredictionProcessing, nil, barLogs)
	barLogs += "prediction in progress 1/1\n"
	ct.AssertResponse(barR[2], server.PredictionProcessing, nil, barLogs)
	ct.AssertResponse(barR[3], server.PredictionProcessing, []any{"*bar-0*"}, barLogs)
	barLogs += "completed prediction\n"
	ct.AssertResponse(barR[4], server.PredictionProcessing, []any{"*bar-0*"}, barLogs)
	ct.AssertResponse(barR[5], server.PredictionSucceeded, []any{"*bar-0*"}, barLogs)
	assert.Len(t, bazR, 8)
	bazLogs := ""
	ct.AssertResponse(bazR[0], server.PredictionStarting, nil, bazLogs)
	bazLogs += "starting prediction\n"
	ct.AssertResponse(bazR[1], server.PredictionProcessing, nil, bazLogs)
	bazLogs += "prediction in progress 1/2\n"
	ct.AssertResponse(bazR[2], server.PredictionProcessing, nil, bazLogs)
	ct.AssertResponse(bazR[3], server.PredictionProcessing, []any{"*baz-0*"}, bazLogs)
	bazLogs += "prediction in progress 2/2\n"
	ct.AssertResponse(bazR[4], server.PredictionProcessing, []any{"*baz-0*"}, bazLogs)
	ct.AssertResponse(bazR[5], server.PredictionProcessing, []any{"*baz-0*", "*baz-1*"}, bazLogs)
	bazLogs += "completed prediction\n"
	ct.AssertResponse(bazR[6], server.PredictionProcessing, []any{"*baz-0*", "*baz-1*"}, bazLogs)
	ct.AssertResponse(bazR[7], server.PredictionSucceeded, []any{"*baz-0*", "*baz-1*"}, bazLogs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

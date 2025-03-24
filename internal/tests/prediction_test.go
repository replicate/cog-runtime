package tests

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/replicate/go/must"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestPredictionSucceeded(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{"i": 1, "s": "bar"})
	assert.Equal(t, server.PredictionSucceeded, resp.Status)
	assert.Equal(t, "*bar*", resp.Output)
	assert.Contains(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", resp.Logs)
	assert.Equal(t, 1.0, resp.Metrics["i"])
	assert.Equal(t, 3.0, resp.Metrics["s_len"])

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionWithIdSucceeded(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.PredictionWithId("p01", map[string]any{"i": 1, "s": "bar"})
	assert.Equal(t, server.PredictionSucceeded, resp.Status)
	assert.Equal(t, "*bar*", resp.Output)
	assert.Equal(t, "p01", resp.Id)
	assert.Contains(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", resp.Logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionFailure(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.AppendEnvs("PREDICTION_FAILURE=1")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{"i": 1, "s": "bar"})
	assert.Equal(t, server.PredictionFailed, resp.Status)
	assert.Equal(t, nil, resp.Output)
	logs := "starting prediction\nprediction in progress 1/1\nprediction failed\n"
	// Compat: legacy Cog also includes Traceback
	assert.Contains(t, resp.Logs, logs)
	assert.Equal(t, "prediction failed", resp.Error)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionCrash(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	ct.AppendArgs("--await-explicit-shutdown=true")
	ct.AppendEnvs("PREDICTION_CRASH=1")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	if *legacyCog {
		req := server.PredictionRequest{
			Input: map[string]any{"i": 1, "s": "bar"},
		}
		resp := ct.PredictionReq(http.MethodPost, "/predictions", req)
		// Compat: legacy Cog returns HTTP 500 and "Internal Server Error"
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		body := string(must.Get(io.ReadAll(resp.Body)))
		assert.Equal(t, "Internal Server Error", body)
		// Compat: flaky server?
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, "DEFUNCT", ct.HealthCheck().Status)
	} else {
		resp := ct.Prediction(map[string]any{"i": 1, "s": "bar"})
		assert.Equal(t, server.PredictionFailed, resp.Status)
		assert.Equal(t, nil, resp.Output)
		assert.Contains(t, resp.Logs, "starting prediction\nprediction in progress 1/1\nprediction crashed\n")
		assert.Contains(t, resp.Logs, "SystemExit: 1\n")
		assert.Equal(t, "prediction failed", resp.Error)
		assert.Equal(t, "DEFUNCT", ct.HealthCheck().Status)
	}

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionConcurrency(t *testing.T) {
	ct := NewCogTest(t, "sleep")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	var resp1 server.PredictionResponse
	done1 := make(chan bool, 1)
	done2 := make(chan bool, 1)

	go func() {
		resp1 = ct.Prediction(map[string]any{"i": 1, "s": "bar"})
		done1 <- true
	}()

	time.Sleep(100 * time.Millisecond)
	// Fail prediction requests when one is in progress
	go func() {
		req := server.PredictionRequest{Input: map[string]any{"i": 1, "s": "baz"}}
		resp := ct.PredictionReq("POST", "/predictions", req)
		assert.Equal(t, http.StatusConflict, resp.StatusCode)
		done2 <- true
	}()

	<-done1
	assert.Equal(t, server.PredictionSucceeded, resp1.Status)
	assert.Equal(t, "*bar*", resp1.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", resp1.Logs)

	<-done2

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

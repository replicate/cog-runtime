package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/replicate/go/must"

	"github.com/replicate/cog-runtime/internal/util"

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
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", resp.Logs)

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
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", resp.Logs)

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
	if *legacyCog {
		assert.Contains(t, resp.Logs, fmt.Sprintf("Exception: prediction failed\n%s", logs))
	} else {
		assert.Equal(t, logs, resp.Logs)
	}
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
		req := server.PredictionRequest{Input: map[string]any{"i": 1, "s": "bar"}}
		req.CreatedAt = util.NowIso()
		data := bytes.NewReader(must.Get(json.Marshal(req)))
		r := must.Get(http.NewRequest(http.MethodPost, ct.Url("/predictions"), data))
		r.Header.Set("Content-Type", "application/json")
		resp := must.Get(http.DefaultClient.Do(r))
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
	var resp2 server.PredictionResponse
	done1 := make(chan bool, 1)
	done2 := make(chan bool, 1)

	go func() {
		resp1 = ct.Prediction(map[string]any{"i": 1, "s": "bar"})
		done1 <- true
	}()

	time.Sleep(100 * time.Millisecond)
	// Block prediction requests when one is in progress
	go func() {
		resp2 = ct.Prediction(map[string]any{"i": 1, "s": "baz"})
		done2 <- true
	}()

	<-done1
	assert.Equal(t, server.PredictionSucceeded, resp1.Status)
	assert.Equal(t, "*bar*", resp1.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", resp1.Logs)

	<-done2
	assert.Equal(t, server.PredictionSucceeded, resp2.Status)
	assert.Equal(t, "*baz*", resp2.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", resp2.Logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

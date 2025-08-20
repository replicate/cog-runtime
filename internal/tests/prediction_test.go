package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
	"github.com/replicate/cog-runtime/internal/util"
)

func TestPredictionSucceeded(t *testing.T) {
	t.Parallel()
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: false,
		uploadURL:        "",
		module:           "sleep",
		predictorClass:   "Predictor",
	})

	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	input := map[string]any{"i": 1, "s": "bar"}
	req := httpPredictionRequest(t, runtimeServer, nil, server.PredictionRequest{Input: input})

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var prediction server.PredictionResponse
	err = json.Unmarshal(body, &prediction)
	require.NoError(t, err)

	assert.Equal(t, server.PredictionSucceeded, prediction.Status)
	assert.Equal(t, "*bar*", prediction.Output)
	assert.Contains(t, prediction.Logs, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n")
	assert.Equal(t, 1.0, prediction.Metrics["i"])
	assert.Equal(t, 3.0, prediction.Metrics["s_len"])
}

func TestPredictionWithIdSucceeded(t *testing.T) {
	t.Parallel()
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: false,
		uploadURL:        "",
		module:           "sleep",
		predictorClass:   "Predictor",
	})
	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	input := map[string]any{"i": 1, "s": "bar"}
	predictionId, err := util.PredictionId()
	require.NoError(t, err)
	predictionReq := server.PredictionRequest{
		Id:    predictionId,
		Input: input,
	}
	req := httpPredictionRequestWithId(t, runtimeServer, nil, predictionReq)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var predictionResponse server.PredictionResponse
	err = json.Unmarshal(body, &predictionResponse)
	require.NoError(t, err)

	assert.Equal(t, server.PredictionSucceeded, predictionResponse.Status)
	assert.Equal(t, "*bar*", predictionResponse.Output)
	assert.Equal(t, predictionId, predictionResponse.Id)
	assert.Contains(t, predictionResponse.Logs, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n")

}

func TestPredictionFailure(t *testing.T) {
	t.Parallel()
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: false,
		uploadURL:        "",
		module:           "sleep",
		predictorClass:   "PredictionFailingPredictor",
	})
	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	input := map[string]any{"i": 1, "s": "bar"}
	req := httpPredictionRequest(t, runtimeServer, nil, server.PredictionRequest{Input: input})

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var predictionResponse server.PredictionResponse
	err = json.Unmarshal(body, &predictionResponse)
	require.NoError(t, err)

	assert.Equal(t, server.PredictionFailed, predictionResponse.Status)
	assert.Equal(t, nil, predictionResponse.Output)
	assert.Contains(t, predictionResponse.Logs, "starting prediction\nprediction failed\n")
	assert.Equal(t, "prediction failed", predictionResponse.Error)
}

func TestPredictionCrash(t *testing.T) {
	t.Parallel()

	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "sleep",
		predictorClass:   "PredictionCrashingPredictor",
	})
	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	input := map[string]any{"i": 1, "s": "bar"}
	req := httpPredictionRequest(t, runtimeServer, nil, server.PredictionRequest{Input: input})

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var predictionResponse server.PredictionResponse
	err = json.Unmarshal(body, &predictionResponse)
	require.NoError(t, err)
	hc := healthCheck(t, runtimeServer)
	switch resp.StatusCode {
	case http.StatusInternalServerError:
		// This is "legacy" cog semantics

		assert.Equal(t, "Internal Server Error", predictionResponse.Error)
		assert.Equal(t, "DEFUNCT", hc.Status)
	case http.StatusOK:
		assert.Equal(t, server.PredictionFailed, predictionResponse.Status)
		assert.Equal(t, nil, predictionResponse.Output)
		assert.Contains(t, predictionResponse.Logs, "starting prediction")
		assert.Contains(t, predictionResponse.Logs, "SystemExit: 1\n")
		assert.Equal(t, "prediction failed", predictionResponse.Error)
		assert.Equal(t, "DEFUNCT", hc.Status)
	default:
		t.Fatalf("unexpected status code: %d", resp.StatusCode)
	}
}

func TestPredictionConcurrency(t *testing.T) {
	t.Parallel()

	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "sleep",
		predictorClass:   "Predictor",
	})
	receiverServer := testHarnessReceiverServer(t)

	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	input := map[string]any{"i": 5, "s": "bar"}

	firstPredictionSent := make(chan bool, 1)

	wg := sync.WaitGroup{}

	wg.Go(func() {
		predictionReq := server.PredictionRequest{
			Input:               input,
			Webhook:             receiverServer.URL + "/webhook",
			WebhookEventsFilter: []server.WebhookEvent{server.WebhookCompleted},
		}
		req := httpPredictionRequest(t, runtimeServer, receiverServer, predictionReq)
		resp, err := http.DefaultClient.Do(req)
		close(firstPredictionSent)
		require.NoError(t, err)
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		assert.Equal(t, http.StatusAccepted, resp.StatusCode)
		var webhook webhookData
		select {
		case webhook = <-receiverServer.webhookReceiverChan:
		case <-time.After(10 * time.Second):
			assert.Fail(t, "timeout waiting for webhook")
		}
		assert.Equal(t, server.PredictionSucceeded, webhook.Response.Status)
		// NOTE(morgan): since we're using the webhook format, the deserialization
		// of `i` is a float64, so we need to convert it to an int, since we've already
		// shipped the input, we can change it directly
		expectedInput := input
		expectedInput["i"] = float64(5)
		assert.Equal(t, expectedInput, webhook.Response.Input)
		assert.Equal(t, "*bar*", webhook.Response.Output)
		assert.Contains(t, webhook.Response.Logs, "starting prediction\nprediction in progress 1/5\nprediction in progress 2/5\nprediction in progress 3/5\nprediction in progress 4/5\nprediction in progress 5/5\ncompleted prediction\n")
	})

	predictionReq := server.PredictionRequest{
		Input: input,
	}
	req := httpPredictionRequest(t, runtimeServer, receiverServer, predictionReq)
	t.Log("waiting for first prediction to be sent")
	select {
	case <-firstPredictionSent:
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for first prediction to be sent")
	}
	t.Log("first prediction sent, attempting second prediction send")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	// Ensure the first prediction is completed
	wg.Wait()
}

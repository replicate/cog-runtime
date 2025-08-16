package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
	"github.com/replicate/cog-runtime/internal/util"
)

func TestPredictionSucceeded(t *testing.T) {
	t.Parallel()
	runtimeServer := setupCogRuntimeServer(t, false, false, false, "", "sleep", "Predictor")

	hc := waitForSetupComplete(t, runtimeServer)
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

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
	runtimeServer := setupCogRuntimeServer(t, false, false, false, "", "sleep", "Predictor")
	hc := waitForSetupComplete(t, runtimeServer)
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	input := map[string]any{"i": 1, "s": "bar"}
	predictionId, err := util.PredictionId()
	require.NoError(t, err)
	predictionReq := server.PredictionRequest{
		Id:    predictionId,
		Input: input,
	}
	req := httpPredictionRequest(t, runtimeServer, nil, predictionReq)

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
	runtimeServer := setupCogRuntimeServer(t, false, false, false, "", "sleep", "PredictionFailingPredictor")
	hc := waitForSetupComplete(t, runtimeServer)
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

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

	runtimeServer := setupCogRuntimeServer(t, false, false, true, "", "sleep", "PredictionCrashingPredictor")
	hc := waitForSetupComplete(t, runtimeServer)
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

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
	hc = healthCheck(t, runtimeServer)
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

	runtimeServer := setupCogRuntimeServer(t, false, false, true, "", "sleep", "Predictor")
	receiverServer := testHarnessReceiverServer(t)

	hc := waitForSetupComplete(t, runtimeServer)
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	input := map[string]any{"i": 5, "s": "bar"}

	firstPredictionSent := make(chan bool, 1)

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
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
		assert.Equal(t, http.StatusAccepted, resp.StatusCode)
		<-receiverServer.webhookReceived
		webhook := receiverServer.webhookRequests[0]
		assert.Equal(t, http.MethodPost, webhook.Method)
		assert.Equal(t, "/webhook", webhook.Path)
		var predictionWebhookResp server.PredictionResponse
		err = json.Unmarshal(webhook.Body, &predictionWebhookResp)
		require.NoError(t, err)
		assert.Equal(t, server.PredictionSucceeded, predictionWebhookResp.Status)
		// NOTE(morgan): since we're using the webhook format, the deserialization
		// of `i` is a float64, so we need to convert it to an int, since we've already
		// shipped the input, we can change it directly
		expectedInput := input
		expectedInput["i"] = float64(5)
		assert.Equal(t, expectedInput, predictionWebhookResp.Input)
		assert.Equal(t, "*bar*", predictionWebhookResp.Output)
		assert.Contains(t, predictionWebhookResp.Logs, "starting prediction\nprediction in progress 1/5\nprediction in progress 2/5\nprediction in progress 3/5\nprediction in progress 4/5\nprediction in progress 5/5\ncompleted prediction\n")
		wg.Done()
	}()

	predictionReq := server.PredictionRequest{
		Input: input,
	}
	req := httpPredictionRequest(t, runtimeServer, receiverServer, predictionReq)
	t.Log("waiting for first prediction to be sent")
	<-firstPredictionSent
	t.Log("first prediction sent, attempting second prediction send")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	// Ensure the first prediction is completed
	wg.Wait()
}

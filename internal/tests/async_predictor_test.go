package tests

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
	"github.com/replicate/cog-runtime/internal/util"
)

func TestAsyncPredictorConcurrency(t *testing.T) {
	t.Parallel()
	runtimeServer := setupCogRuntimeServer(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: false,
		uploadURL:        "",
		module:           "async_sleep",
		predictorClass:   "Predictor",
		concurrencyMax:   2,
	})
	receiverServer := testHarnessReceiverServer(t)
	hc := waitForSetupComplete(t, runtimeServer)
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	barId, err := util.PredictionId()
	require.NoError(t, err)
	bazId, err := util.PredictionId()
	require.NoError(t, err)
	barReq := httpPredictionRequestWithId(t, runtimeServer, receiverServer, server.PredictionRequest{
		Input:               map[string]any{"i": 1, "s": "bar"},
		Webhook:             receiverServer.URL + "/webhook",
		WebhookEventsFilter: []server.WebhookEvent{server.WebhookCompleted},
		Id:                  barId,
	})
	bazReq := httpPredictionRequestWithId(t, runtimeServer, receiverServer, server.PredictionRequest{
		Input:               map[string]any{"i": 2, "s": "baz"},
		Webhook:             receiverServer.URL + "/webhook",
		WebhookEventsFilter: []server.WebhookEvent{server.WebhookCompleted},
		Id:                  bazId,
	})
	barResp, err := http.DefaultClient.Do(barReq)
	require.NoError(t, err)
	defer barResp.Body.Close()
	assert.Equal(t, http.StatusAccepted, barResp.StatusCode)
	_, _ = io.Copy(io.Discard, barResp.Body)
	bazResp, err := http.DefaultClient.Do(bazReq)
	require.NoError(t, err)
	defer bazResp.Body.Close()
	assert.Equal(t, http.StatusAccepted, bazResp.StatusCode)
	_, _ = io.Copy(io.Discard, bazResp.Body)

	var barRCompleted bool
	var bazRCompleted bool

	for webhook := range receiverServer.webhookReceiverChan {
		assert.Equal(t, server.PredictionSucceeded, webhook.Response.Status)
		switch webhook.Response.Id {
		case barId:
			assert.Equal(t, "*bar*", webhook.Response.Output)
			assert.True(t, strings.Contains(webhook.Response.Logs, "starting async prediction\nprediction in progress 1/1\ncompleted async prediction\n"))
			barRCompleted = true
		case bazId:
			assert.Equal(t, "*baz*", webhook.Response.Output)
			assert.Equal(t, "starting async prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted async prediction\n", webhook.Response.Logs)
			bazRCompleted = true
		}
		if barRCompleted && bazRCompleted {
			break
		}
	}
}

func TestAsyncPredictorCanceled(t *testing.T) {
	t.Parallel()
	if *legacyCog {
		// Cancellation bug as of 0.14.1
		t.Skipf("skipping due to cancellation bug: https://github.com/replicate/cog/issues/2212")
	}

	runtimeServer := setupCogRuntimeServer(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: false,
		uploadURL:        "",
		module:           "async_sleep",
		predictorClass:   "Predictor",
		concurrencyMax:   2,
	})
	receiverServer := testHarnessReceiverServer(t)
	hc := waitForSetupComplete(t, runtimeServer)
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	barId, err := util.PredictionId()
	require.NoError(t, err)
	barReq := httpPredictionRequestWithId(t, runtimeServer, receiverServer, server.PredictionRequest{
		Input:   map[string]any{"i": 60, "s": "bar"},
		Webhook: receiverServer.URL + "/webhook",
		WebhookEventsFilter: []server.WebhookEvent{
			server.WebhookStart,
			server.WebhookCompleted},
		Id: barId,
	})
	barResp, err := http.DefaultClient.Do(barReq)
	require.NoError(t, err)
	defer barResp.Body.Close()
	assert.Equal(t, http.StatusAccepted, barResp.StatusCode)
	_, _ = io.Copy(io.Discard, barResp.Body)

	cancelReq, err := http.NewRequest(http.MethodPost, runtimeServer.URL+fmt.Sprintf("/predictions/%s/cancel", barId), nil)
	require.NoError(t, err)

	// Get the start webhook, then continue.
	var webhook webhookData
	select {
	case webhook = <-receiverServer.webhookReceiverChan:
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for webhook")
	}
	assert.Equal(t, barId, webhook.Response.Id)

	// cancel the prediction now that we're sure it has "Started " (for some value of "Started")
	cancelResp, err := http.DefaultClient.Do(cancelReq)
	require.NoError(t, err)
	defer cancelResp.Body.Close()
	assert.Equal(t, http.StatusOK, cancelResp.StatusCode)
	_, _ = io.Copy(io.Discard, cancelResp.Body)

	select {
	case webhook = <-receiverServer.webhookReceiverChan:
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for webhook")
	}

	assert.Equal(t, server.PredictionCanceled, webhook.Response.Status)
	assert.Equal(t, barId, webhook.Response.Id)
	// NOTE(morgan): The logs are not deterministic, so we can only assert that `prediction canceled` is in the logs.
	// previously we asserted that the prediction was making progress. We are assured that we have a "starting" webhook, but
	// internally this test not reacts faster than the runner does.
	assert.Contains(t, webhook.Response.Logs, "prediction canceled\n")
}

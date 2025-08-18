package tests

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
	"github.com/replicate/cog-runtime/internal/util"
)

func TestIteratorTypes(t *testing.T) {
	testCases := []struct {
		module        string
		skipLegacyCog bool
	}{
		{
			module: "iterator",
		},
		{
			module:        "async_iterator",
			skipLegacyCog: true,
		},
		{
			module: "concat_iterator",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.module, func(t *testing.T) {
			t.Parallel()
			if tc.skipLegacyCog && *legacyCog {
				t.Skipf("skipping %s due to legacy Cog configuration", tc.module)
			}
			runtimeServer := setupCogRuntimeServer(t, cogRuntimeServerConfig{
				procedureMode:    false,
				explicitShutdown: false,
				uploadURL:        "",
				module:           tc.module,
				predictorClass:   "Predictor",
			})
			receiverServer := testHarnessReceiverServer(t)

			hc := waitForSetupComplete(t, runtimeServer)
			assert.Equal(t, server.StatusReady.String(), hc.Status)
			assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

			input := map[string]any{"i": 2, "s": "bar"}
			req := httpPredictionRequest(t, runtimeServer, receiverServer, server.PredictionRequest{Input: input, Webhook: receiverServer.URL + "/webhook"})
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusAccepted, resp.StatusCode)
			_, _ = io.Copy(io.Discard, resp.Body)
			require.NoError(t, err)
			var predictionResponse server.PredictionResponse
			for webhook := range receiverServer.webhookReceived {
				if webhook.Response.Status == server.PredictionSucceeded {
					predictionResponse = webhook.Response
					break
				}
			}

			expectedOutput := []any{"*bar-0*", "*bar-1*"}

			assert.Equal(t, server.PredictionSucceeded, predictionResponse.Status)
			assert.Equal(t, expectedOutput, predictionResponse.Output)
			assert.Equal(t, "starting prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted prediction\n", predictionResponse.Logs)
		})
	}
}

func TestPredictionAsyncIteratorConcurrency(t *testing.T) {
	t.Parallel()
	if *legacyCog {
		t.Skipf("skipping async iterator concurrency test due to legacy cog configuration")
	}

	runtimeServer := setupCogRuntimeServer(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: false,
		uploadURL:        "",
		module:           "async_iterator",
		predictorClass:   "Predictor",
	})
	receiverServer := testHarnessReceiverServer(t)

	hc := waitForSetupComplete(t, runtimeServer)
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	barId, err := util.PredictionId()
	require.NoError(t, err)
	bazId, err := util.PredictionId()
	require.NoError(t, err)
	barPrediction := server.PredictionRequest{
		Input:               map[string]any{"i": 1, "s": "bar"},
		Webhook:             receiverServer.URL + "/webhook",
		Id:                  barId,
		WebhookEventsFilter: []server.WebhookEvent{server.WebhookCompleted},
	}
	bazPrediction := server.PredictionRequest{
		Input:               map[string]any{"i": 2, "s": "baz"},
		Webhook:             receiverServer.URL + "/webhook",
		Id:                  bazId,
		WebhookEventsFilter: []server.WebhookEvent{server.WebhookCompleted},
	}
	barReq := httpPredictionRequestWithId(t, runtimeServer, receiverServer, barPrediction)
	bazReq := httpPredictionRequestWithId(t, runtimeServer, receiverServer, bazPrediction)
	barResp, err := http.DefaultClient.Do(barReq)
	require.NoError(t, err)
	defer barResp.Body.Close()
	_, _ = io.Copy(io.Discard, barResp.Body)
	bazResp, err := http.DefaultClient.Do(bazReq)
	require.NoError(t, err)
	defer bazResp.Body.Close()
	_, _ = io.Copy(io.Discard, bazResp.Body)
	var barR *server.PredictionResponse
	var bazR *server.PredictionResponse
	for webhook := range receiverServer.webhookReceived {
		assert.Equal(t, server.PredictionSucceeded, webhook.Response.Status)
		switch webhook.Response.Id {
		case barPrediction.Id:
			barR = &webhook.Response
		case bazPrediction.Id:
			bazR = &webhook.Response
		}
		if barR != nil && bazR != nil {
			break
		}
	}
	assert.Equal(t, server.PredictionSucceeded, barR.Status)
	assert.Equal(t, []any{"*bar-0*"}, barR.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/1\ncompleted prediction\n", barR.Logs)
	assert.Equal(t, server.PredictionSucceeded, bazR.Status)
	assert.Equal(t, []any{"*baz-0*", "*baz-1*"}, bazR.Output)
	assert.Equal(t, "starting prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted prediction\n", bazR.Logs)
}

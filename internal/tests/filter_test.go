package tests

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/runner"
	"github.com/replicate/cog-runtime/internal/util"
)

func TestPredictionWebhookFilter(t *testing.T) {
	testCases := []struct {
		name                          string
		webhookEvents                 []runner.WebhookEvent
		expectedWebhookCount          int
		legacyCogExpectedWebhookCount int
		allowedPredictionStatuses     []runner.PredictionStatus
	}{
		{
			name: "all",
			webhookEvents: []runner.WebhookEvent{
				runner.WebhookStart,
				runner.WebhookOutput,
				runner.WebhookLogs,
				runner.WebhookCompleted,
			},
			expectedWebhookCount:          8,
			legacyCogExpectedWebhookCount: 7,
			allowedPredictionStatuses: []runner.PredictionStatus{
				runner.PredictionStarting,
				runner.PredictionProcessing,
				runner.PredictionSucceeded,
				runner.PredictionFailed,
			},
		},
		{
			name: "completed",
			webhookEvents: []runner.WebhookEvent{
				runner.WebhookCompleted,
			},
			expectedWebhookCount:          1,
			legacyCogExpectedWebhookCount: 1,
			allowedPredictionStatuses: []runner.PredictionStatus{
				runner.PredictionSucceeded,
			},
		},
		{
			name: "start_completed",
			webhookEvents: []runner.WebhookEvent{
				runner.WebhookStart,
				runner.WebhookCompleted,
			},
			expectedWebhookCount:          2,
			legacyCogExpectedWebhookCount: 2,
			allowedPredictionStatuses: []runner.PredictionStatus{
				runner.PredictionStarting,
				runner.PredictionProcessing,
				runner.PredictionSucceeded,
			},
		},
		{
			allowedPredictionStatuses: []runner.PredictionStatus{
				runner.PredictionStarting,
				runner.PredictionProcessing,
				runner.PredictionSucceeded,
			},
		},
		{
			name: "output_completed",
			webhookEvents: []runner.WebhookEvent{
				runner.WebhookOutput,
				runner.WebhookCompleted,
			},
			expectedWebhookCount:          3,
			legacyCogExpectedWebhookCount: 3,
			allowedPredictionStatuses: []runner.PredictionStatus{
				runner.PredictionProcessing,
				runner.PredictionSucceeded,
			},
		},
		{
			name: "logs_completed",
			webhookEvents: []runner.WebhookEvent{
				runner.WebhookLogs,
				runner.WebhookCompleted,
			},
			expectedWebhookCount:          5,
			legacyCogExpectedWebhookCount: 5,
			allowedPredictionStatuses: []runner.PredictionStatus{
				runner.PredictionProcessing,
				runner.PredictionSucceeded,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			receiverServer := testHarnessReceiverServer(t)
			runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
				procedureMode:    false,
				explicitShutdown: true,
				uploadURL:        "",
				module:           "iterator",
				predictorClass:   "Predictor",
			})
			waitForSetupComplete(t, runtimeServer, runner.StatusReady, runner.SetupSucceeded)

			predictionID, err := util.PredictionID()
			require.NoError(t, err)
			prediction := runner.PredictionRequest{
				Input:               map[string]any{"i": 2, "s": "bar"},
				Webhook:             receiverServer.URL + "/webhook",
				WebhookEventsFilter: tc.webhookEvents,
				ID:                  predictionID,
			}
			req := httpPredictionRequestWithID(t, runtimeServer, prediction)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusAccepted, resp.StatusCode)
			_, _ = io.Copy(io.Discard, resp.Body)
			require.NoError(t, err)

			// Validate the webhook events
			timer := time.After(10 * time.Second)
			expectedWebhookCount := tc.expectedWebhookCount
			if *legacyCog {
				expectedWebhookCount = tc.legacyCogExpectedWebhookCount
			}
			for count := 0; count < expectedWebhookCount; count++ {
				select {
				case webhookEvent := <-receiverServer.webhookReceiverChan:
					assert.Contains(t, tc.allowedPredictionStatuses, webhookEvent.Response.Status)
					if webhookEvent.Response.Status == runner.PredictionSucceeded {
						assert.Equal(t, "starting prediction\nprediction in progress 1/2\nprediction in progress 2/2\ncompleted prediction\n", webhookEvent.Response.Logs)
						assert.Equal(t, []any{"*bar-0*", "*bar-1*"}, webhookEvent.Response.Output)
					}
				case <-timer:
					t.Fatalf("timeout waiting for webhook events")
				}
			}
		})
	}
}

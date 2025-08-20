package tests

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
	"github.com/replicate/cog-runtime/internal/util"
)

func TestPredictionWebhookFilter(t *testing.T) {
	testCases := []struct {
		name                          string
		webhookEvents                 []server.WebhookEvent
		expectedWebhookCount          int
		legacyCogExpectedWebhookCount int
		allowedPredictionStatuses     []server.PredictionStatus
	}{
		{
			name: "all",
			webhookEvents: []server.WebhookEvent{
				server.WebhookStart,
				server.WebhookOutput,
				server.WebhookLogs,
				server.WebhookCompleted,
			},
			expectedWebhookCount:          8,
			legacyCogExpectedWebhookCount: 7,
			allowedPredictionStatuses: []server.PredictionStatus{
				server.PredictionStarting,
				server.PredictionProcessing,
				server.PredictionSucceeded,
				server.PredictionFailed,
			},
		},
		{
			name: "completed",
			webhookEvents: []server.WebhookEvent{
				server.WebhookCompleted,
			},
			expectedWebhookCount:          1,
			legacyCogExpectedWebhookCount: 1,
			allowedPredictionStatuses: []server.PredictionStatus{
				server.PredictionSucceeded,
			},
		},
		{
			name: "start_completed",
			webhookEvents: []server.WebhookEvent{
				server.WebhookStart,
				server.WebhookCompleted,
			},
			expectedWebhookCount:          2,
			legacyCogExpectedWebhookCount: 2,
			allowedPredictionStatuses: []server.PredictionStatus{
				server.PredictionStarting,
				server.PredictionProcessing,
				server.PredictionSucceeded,
			},
		},
		{
			name: "output_completed",
			webhookEvents: []server.WebhookEvent{
				server.WebhookOutput,
				server.WebhookCompleted,
			},
			expectedWebhookCount:          3,
			legacyCogExpectedWebhookCount: 3,
			allowedPredictionStatuses: []server.PredictionStatus{
				server.PredictionProcessing,
				server.PredictionSucceeded,
			},
		},
		{
			name: "logs_completed",
			webhookEvents: []server.WebhookEvent{
				server.WebhookLogs,
				server.WebhookCompleted,
			},
			expectedWebhookCount:          5,
			legacyCogExpectedWebhookCount: 5,
			allowedPredictionStatuses: []server.PredictionStatus{
				server.PredictionProcessing,
				server.PredictionSucceeded,
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
			waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

			predictionId, err := util.PredictionId()
			require.NoError(t, err)
			prediction := server.PredictionRequest{
				Input:               map[string]any{"i": 2, "s": "bar"},
				Webhook:             receiverServer.URL + "/webhook",
				WebhookEventsFilter: tc.webhookEvents,
				Id:                  predictionId,
			}
			req := httpPredictionRequestWithId(t, runtimeServer, prediction)
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
					if webhookEvent.Response.Status == server.PredictionSucceeded {
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

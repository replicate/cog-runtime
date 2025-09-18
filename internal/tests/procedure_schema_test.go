package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/runner"
	"github.com/replicate/cog-runtime/internal/webhook"
)

// TestProcedureSchemaLoadingSequential tests that schema loading works correctly
// for sequential predictions in procedure mode where runners may be cleaned up
// between predictions. This is a specific regression test for the issue where
// schema loading timing caused problems after runner recreation.
func TestProcedureSchemaLoadingSequential(t *testing.T) {
	t.Parallel()
	if *legacyCog {
		t.Skip("procedure endpoint has diverged from legacy Cog")
	}

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("fake-jpeg-data-for-testing"))
	}))
	t.Cleanup(testServer.Close)

	receiverServer := testHarnessReceiverServer(t)

	runtimeServer, _, _ := setupCogRuntimeServer(t, cogRuntimeServerConfig{
		procedureMode:    true,
		explicitShutdown: true,
		uploadURL:        "",
		maxRunners:       1, // Force runner recreation between predictions
	})

	waitForSetupComplete(t, runtimeServer, runner.StatusReady, runner.SetupSucceeded)
	procedureURL := fmt.Sprintf("file://%s/python/tests/procedures/path_test", basePath)

	wg := sync.WaitGroup{}

	// Run 3 sequential predictions to test schema loading robustness
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(predIndex int) {
			defer wg.Done()

			prediction := runner.PredictionRequest{
				Input: map[string]any{
					"img": fmt.Sprintf("%s/image-%d.jpg", testServer.URL, predIndex),
				},
				Context: map[string]any{
					"procedure_source_url": procedureURL,
					"replicate_api_token":  "test-token",
				},
				Webhook:             receiverServer.URL + "/webhook",
				WebhookEventsFilter: []webhook.Event{webhook.EventCompleted},
			}

			_, statusCode := runProcedure(t, runtimeServer, prediction)
			require.Equal(t, http.StatusAccepted, statusCode)

			var wh webhookData
			select {
			case wh = <-receiverServer.webhookReceiverChan:
			case <-time.After(10 * time.Second):
				t.Errorf("timeout waiting for webhook for prediction %d", predIndex)
				return
			}

			assert.Equal(t, runner.PredictionSucceeded, wh.Response.Status, "prediction %d should succeed", predIndex)

			// Verify URL processing worked - this is the key regression test
			output, ok := wh.Response.Output.(string)
			require.True(t, ok, "output should be a string for prediction %d", predIndex)
			assert.True(t, strings.HasPrefix(output, "data:"),
				"prediction %d: HTTPS URL should be downloaded and converted to base64, got: %s", predIndex, output)
		}(i)

		// Wait for this prediction to complete before starting the next
		// This ensures sequential execution and potential runner cleanup between predictions
		wg.Wait()
	}
}

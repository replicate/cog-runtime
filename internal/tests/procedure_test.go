package tests

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/replicate/cog-runtime/internal/server"
)

func TestProcedure(t *testing.T) {
	if *legacyCog {
		// Compat: procedure endpoint has diverged from legacy Cog
		t.SkipNow()
	}

	ct := NewCogProcedureTest(t)
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	prediction := func(procedure, token string, input map[string]any) server.PredictionResponse {
		url := fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, procedure)
		req := server.PredictionRequest{
			Context: map[string]any{
				"procedure_source_url": url,
				"replicate_api_token":  token,
			},
			Input: input,
		}
		return ct.prediction(http.MethodPost, "/procedures", req)
	}

	var wg sync.WaitGroup

	assert.Equal(t, server.StatusReady.String(), ct.HealthCheck().Status)
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp1 := prediction("foo", "bar", map[string]any{"s": "foobar"})
		assert.Equal(t, server.PredictionSucceeded, resp1.Status)
		assert.Equal(t, "s=foobar, token=bar", resp1.Output)
		assert.Contains(t, resp1.Logs, "predicting foo\n")
	}()
	time.Sleep(500 * time.Millisecond) // Wait for runner startup
	//assert.Equal(t, server.StatusBusy.String(), ct.HealthCheck().Status)
	wg.Wait()

	// Wait for status reset to ready
	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, server.StatusReady.String(), ct.HealthCheck().Status)
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp2 := prediction("bar", "baz", map[string]any{"i": 123456})
		assert.Equal(t, server.PredictionSucceeded, resp2.Status)
		assert.Equal(t, "i=123456, token=baz", resp2.Output)
		assert.Contains(t, resp2.Logs, "predicting bar\n")
	}()
	time.Sleep(500 * time.Millisecond) // Wait for runner startup
	assert.Equal(t, server.StatusBusy.String(), ct.HealthCheck().Status)
	wg.Wait()

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

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
	// Force 2 runner slots
	ct.AppendEnvs("GOMAXPROCS=2")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	rawPrediction := func(url, token string, input map[string]any) *http.Response {
		req := server.PredictionRequest{
			Context: map[string]any{
				"procedure_source_url": url,
				"replicate_api_token":  token,
			},
			Input: input,
		}
		return ct.PredictionReq(http.MethodPost, "/procedures", req)
	}

	prediction := func(url, token string, input map[string]any) server.PredictionResponse {
		req := server.PredictionRequest{
			Context: map[string]any{
				"procedure_source_url": url,
				"replicate_api_token":  token,
			},
			Input: input,
		}
		return ct.prediction(http.MethodPost, "/procedures", req)
	}

	assert.Equal(t, server.StatusReady.String(), ct.HealthCheck().Status)

	// Occupy slot 1
	var wg1 sync.WaitGroup
	wg1.Add(1)
	fooURL := fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, "foo")
	go func() {
		defer wg1.Done()
		resp1 := prediction(fooURL, "footok", map[string]any{"i": 3, "s": "foostr"})
		assert.Equal(t, server.PredictionSucceeded, resp1.Status)
		assert.Equal(t, "i=3, s=foostr, token=footok", resp1.Output)
		assert.Contains(t, resp1.Logs, "predicting foo\n")
	}()

	time.Sleep(200 * time.Millisecond) // Wait for runner startup
	// Only 1 out of 2 runner slots occupied, ready for 1 more
	assert.Equal(t, server.StatusReady.String(), ct.HealthCheck().Status)
	assert.Equal(t, []string{fooURL}, ct.Runners())

	// Occupy slot 2
	var wg2 sync.WaitGroup
	wg2.Add(1)
	barURL := fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, "bar")
	go func() {
		defer wg2.Done()
		resp2 := prediction(barURL, "bartok", map[string]any{"i": 2, "s": "barstr"})
		assert.Equal(t, server.PredictionSucceeded, resp2.Status)
		assert.Equal(t, "i=2, s=barstr, token=bartok", resp2.Output)
		assert.Contains(t, resp2.Logs, "predicting bar\n")
	}()

	time.Sleep(200 * time.Millisecond) // Wait for runner startup
	// 2 out of 2 runner slots occupied, cannot evict and busy
	assert.Equal(t, server.StatusBusy.String(), ct.HealthCheck().Status)
	assert.Equal(t, []string{barURL, fooURL}, ct.Runners())

	bazURL := fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, "baz")
	badResp1 := rawPrediction(bazURL, "baztok", map[string]any{"i": 1, "s": "bazstr"})
	assert.Equal(t, http.StatusConflict, badResp1.StatusCode)
	assert.Equal(t, []string{barURL, fooURL}, ct.Runners())

	// Wait for 1 slot to free up
	wg2.Wait()
	assert.Equal(t, server.StatusReady.String(), ct.HealthCheck().Status)
	assert.Equal(t, []string{barURL, fooURL}, ct.Runners())

	badURL := fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, "bad")
	badResp2 := prediction(badURL, "badtok", map[string]any{"i": 1, "s": "badstr"})
	assert.Equal(t, server.PredictionFailed, badResp2.Status)
	assert.Contains(t, badResp2.Logs, "unsupported Cog type")
	// New procedure evicts an idle slot but vacates itself after setup failure
	assert.Equal(t, []string{fooURL}, ct.Runners())

	// Evict one of the 2 slots
	var wg3 sync.WaitGroup
	wg3.Add(1)
	go func() {
		defer wg3.Done()
		resp2 := prediction(bazURL, "baztok", map[string]any{"i": 2, "s": "bazstr"})
		assert.Equal(t, server.PredictionSucceeded, resp2.Status)
		assert.Equal(t, "i=2, s=bazstr, token=baztok", resp2.Output)
		assert.Contains(t, resp2.Logs, "predicting baz\n")
	}()

	time.Sleep(200 * time.Millisecond) // Wait for runner startup
	// 2 out of 2 runner slots occupied, cannot evict and busy
	assert.Equal(t, server.StatusBusy.String(), ct.HealthCheck().Status)
	assert.Equal(t, []string{bazURL, fooURL}, ct.Runners())

	wg1.Wait()
	wg3.Wait()
	assert.Equal(t, server.StatusReady.String(), ct.HealthCheck().Status)
	assert.Equal(t, []string{bazURL, fooURL}, ct.Runners())

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

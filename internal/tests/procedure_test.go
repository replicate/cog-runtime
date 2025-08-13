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

func procPredictionHTTP(ct *CogTest, url, token string, input map[string]any) *http.Response {
	req := server.PredictionRequest{
		Context: map[string]any{
			"procedure_source_url": url,
			"replicate_api_token":  token,
		},
		Input: input,
	}
	return ct.PredictionReq(http.MethodPost, "/procedures", req)
}

func procPrediction(ct *CogTest, url, token string, input map[string]any) server.PredictionResponse {
	req := server.PredictionRequest{
		Context: map[string]any{
			"procedure_source_url": url,
			"replicate_api_token":  token,
		},
		Input: input,
	}
	return ct.prediction(http.MethodPost, "/procedures", req)
}

func TestProcedure(t *testing.T) {
	if *legacyCog {
		// Compat: procedure endpoint has diverged from legacy Cog
		t.SkipNow()
	}

	ct := NewCogProcedureTest(t)
	// Force max runners and max concurrency to 2
	ct.AppendEnvs("GOMAXPROCS=1")
	ct.AppendEnvs("COG_PROCEDURE_CONCURRENCY_PER_CPU=2")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)
	assert.Equal(t, 2, hc.Concurrency.Max)
	assert.Equal(t, 0, hc.Concurrency.Current)

	assert.Equal(t, server.StatusReady.String(), ct.HealthCheck().Status)

	// Occupy slot 1
	var wg1 sync.WaitGroup
	wg1.Add(1)
	fooURL := fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, "foo")
	go func() {
		defer wg1.Done()
		resp1 := procPrediction(ct, fooURL, "footok", map[string]any{"i": 3, "s": "foostr"})
		assert.Equal(t, server.PredictionSucceeded, resp1.Status)
		assert.Equal(t, "i=3, s=foostr, token=footok", resp1.Output)
		assert.Contains(t, resp1.Logs, "predicting foo\n")
	}()

	time.Sleep(200 * time.Millisecond) // Wait for runner startup
	// Only 1 out of 2 runner slots occupied, ready for 1 more
	hc = ct.HealthCheck()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, 2, hc.Concurrency.Max)
	assert.Equal(t, 1, hc.Concurrency.Current)
	assert.Equal(t, []string{"00:" + fooURL}, ct.Runners())

	// Occupy slot 2
	var wg2 sync.WaitGroup
	wg2.Add(1)
	barURL := fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, "bar")
	go func() {
		defer wg2.Done()
		resp2 := procPrediction(ct, barURL, "bartok", map[string]any{"i": 2, "s": "barstr"})
		assert.Equal(t, server.PredictionSucceeded, resp2.Status)
		assert.Equal(t, "i=2, s=barstr, token=bartok", resp2.Output)
		assert.Contains(t, resp2.Logs, "predicting bar\n")
	}()

	time.Sleep(200 * time.Millisecond) // Wait for runner startup
	// 2 out of 2 runner slots occupied, cannot evict and busy
	hc = ct.HealthCheck()
	assert.Equal(t, server.StatusBusy.String(), hc.Status)
	assert.Equal(t, 2, hc.Concurrency.Max)
	assert.Equal(t, 2, hc.Concurrency.Current)
	assert.Equal(t, []string{"00:" + fooURL, "01:" + barURL}, ct.Runners())

	bazURL := fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, "baz")
	badResp1 := procPredictionHTTP(ct, bazURL, "baztok", map[string]any{"i": 1, "s": "bazstr"})
	assert.Equal(t, http.StatusConflict, badResp1.StatusCode)
	assert.Equal(t, []string{"00:" + fooURL, "01:" + barURL}, ct.Runners())

	// Wait for 1 slot to free up
	wg2.Wait()
	hc = ct.HealthCheck()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, 2, hc.Concurrency.Max)
	assert.Equal(t, 1, hc.Concurrency.Current)
	assert.Equal(t, []string{"00:" + fooURL, "01:" + barURL}, ct.Runners())

	badURL := fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, "bad")
	badResp2 := procPrediction(ct, badURL, "badtok", map[string]any{"i": 1, "s": "badstr"})
	assert.Equal(t, server.PredictionFailed, badResp2.Status)
	assert.Contains(t, badResp2.Logs, "unsupported Cog type")
	assert.Equal(t, "setup failed", badResp2.Error)
	// A new procedure evicts an idle slot but vacates itself after setup failure
	assert.Equal(t, []string{"00:" + fooURL}, ct.Runners())

	// Evict one of the 2 slots
	var wg3 sync.WaitGroup
	wg3.Add(1)
	go func() {
		defer wg3.Done()
		resp2 := procPrediction(ct, bazURL, "baztok", map[string]any{"i": 2, "s": "bazstr"})
		assert.Equal(t, server.PredictionSucceeded, resp2.Status)
		assert.Equal(t, "i=2, s=bazstr, token=baztok", resp2.Output)
		assert.Contains(t, resp2.Logs, "predicting baz\n")
	}()

	time.Sleep(200 * time.Millisecond) // Wait for runner startup
	// 2 out of 2 runner slots occupied, cannot evict and busy
	hc = ct.HealthCheck()
	assert.Equal(t, server.StatusBusy.String(), hc.Status)
	assert.Equal(t, 2, hc.Concurrency.Max)
	assert.Equal(t, 2, hc.Concurrency.Current)
	assert.Equal(t, []string{"00:" + fooURL, "01:" + bazURL}, ct.Runners())

	wg1.Wait()
	wg3.Wait()
	hc = ct.HealthCheck()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, 2, hc.Concurrency.Max)
	assert.Equal(t, 0, hc.Concurrency.Current)
	assert.Equal(t, []string{"00:" + fooURL, "01:" + bazURL}, ct.Runners())

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestProcedureAsyncConcurrency(t *testing.T) {
	if *legacyCog {
		// Compat: procedure endpoint has diverged from legacy Cog
		t.SkipNow()
	}

	ct := NewCogProcedureTest(t)
	// Force max runners and max concurrency to 4
	ct.AppendEnvs("GOMAXPROCS=1")
	ct.AppendEnvs("COG_PROCEDURE_CONCURRENCY_PER_CPU=4")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)
	assert.Equal(t, 4, hc.Concurrency.Max)
	assert.Equal(t, 0, hc.Concurrency.Current)

	var wg sync.WaitGroup

	fooURL := fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, "foo")
	predict := func(i int, s string) {
		defer wg.Done()
		resp := procPrediction(ct, fooURL, "footok", map[string]any{"i": i, "s": s})
		assert.Equal(t, server.PredictionSucceeded, resp.Status)
		assert.Equal(t, fmt.Sprintf("i=%d, s=%s, token=footok", i, s), resp.Output)
		assert.Contains(t, resp.Logs, "predicting foo\n")
	}

	// foo has max concurrency of 2
	wg.Add(2)
	go predict(3, "foo")
	go predict(3, "bar")

	time.Sleep(200 * time.Millisecond) // Wait for runner startup
	// Only 1 out of 4 runner slots occupied with 2 pending
	hc = ct.HealthCheck()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, 4, hc.Concurrency.Max)
	assert.Equal(t, 2, hc.Concurrency.Current)
	assert.Equal(t, []string{"00:" + fooURL}, ct.Runners())

	wg.Add(2)
	go predict(2, "foz")
	go predict(2, "baz")

	time.Sleep(200 * time.Millisecond) // Wait for runner startup
	// 2 out of 4 runner slots occupied with 4 pending
	// Max concurrency reached
	hc = ct.HealthCheck()
	assert.Equal(t, server.StatusBusy.String(), hc.Status)
	assert.Equal(t, 4, hc.Concurrency.Max)
	assert.Equal(t, 4, hc.Concurrency.Current)
	assert.Equal(t, []string{"00:" + fooURL, "01:" + fooURL}, ct.Runners())

	badResp := procPredictionHTTP(ct, fooURL, "badtok", map[string]any{"i": 1, "s": "badstr"})
	assert.Equal(t, http.StatusConflict, badResp.StatusCode)
	assert.Equal(t, []string{"00:" + fooURL, "01:" + fooURL}, ct.Runners())

	wg.Wait()
	// 4 out of 4 runner slots occupied with 0 pending
	hc = ct.HealthCheck()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, 4, hc.Concurrency.Max)
	assert.Equal(t, 0, hc.Concurrency.Current)
	assert.Equal(t, []string{"00:" + fooURL, "01:" + fooURL}, ct.Runners())

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestProcedureNonAsyncConcurrency(t *testing.T) {
	if *legacyCog {
		// Compat: procedure endpoint has diverged from legacy Cog
		t.SkipNow()
	}

	ct := NewCogProcedureTest(t)
	// Force max runners and max concurrency to 4
	ct.AppendEnvs("GOMAXPROCS=1")
	ct.AppendEnvs("COG_PROCEDURE_CONCURRENCY_PER_CPU=4")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)
	assert.Equal(t, 4, hc.Concurrency.Max)
	assert.Equal(t, 0, hc.Concurrency.Current)

	var wg sync.WaitGroup

	barURL := fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, "bar")
	predict := func(i int, s string) {
		defer wg.Done()
		resp1 := procPrediction(ct, barURL, "bartok", map[string]any{"i": i, "s": s})
		assert.Equal(t, server.PredictionSucceeded, resp1.Status)
		assert.Equal(t, fmt.Sprintf("i=%d, s=%s, token=bartok", i, s), resp1.Output)
		assert.Contains(t, resp1.Logs, "predicting bar\n")
	}

	// bar has non-async predict
	wg.Add(3)
	go predict(3, "foo")
	go predict(3, "bar")
	go predict(3, "baz")

	time.Sleep(200 * time.Millisecond) // Wait for runner startup
	// 3 out of 4 runner slots occupied with 3 pending
	hc = ct.HealthCheck()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, 4, hc.Concurrency.Max)
	assert.Equal(t, 3, hc.Concurrency.Current)
	assert.Equal(t, []string{"00:" + barURL, "01:" + barURL, "02:" + barURL}, ct.Runners())

	wg.Add(1)
	go predict(2, "foz")

	time.Sleep(200 * time.Millisecond) // Wait for runner startup
	// 4 out of 4 runner slots occupied with 4 pending
	// Max concurrency reached
	hc = ct.HealthCheck()
	assert.Equal(t, server.StatusBusy.String(), hc.Status)
	assert.Equal(t, 4, hc.Concurrency.Max)
	assert.Equal(t, 4, hc.Concurrency.Current)
	assert.Equal(t, []string{"00:" + barURL, "01:" + barURL, "02:" + barURL, "03:" + barURL}, ct.Runners())

	badResp := procPredictionHTTP(ct, barURL, "badtok", map[string]any{"i": 1, "s": "badstr"})
	assert.Equal(t, http.StatusConflict, badResp.StatusCode)
	assert.Equal(t, []string{"00:" + barURL, "01:" + barURL, "02:" + barURL, "03:" + barURL}, ct.Runners())

	wg.Wait()
	// 4 out of 4 runner slots occupied with 0 pending
	hc = ct.HealthCheck()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, 4, hc.Concurrency.Max)
	assert.Equal(t, 0, hc.Concurrency.Current)
	assert.Equal(t, []string{"00:" + barURL, "01:" + barURL, "02:" + barURL, "03:" + barURL}, ct.Runners())

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

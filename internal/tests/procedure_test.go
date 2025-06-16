package tests

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/replicate/cog-runtime/internal/server"
)

func TestProcedure(t *testing.T) {
	if *legacyCog {
		dir := path.Join(basePath, "..", "pipelines-runtime")
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			t.Logf("pipelines-runtime not found, skipping legacy cog test")
			t.SkipNow()
		}
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

	resp1 := prediction("foo", "bar", map[string]any{"s": "foobar"})
	assert.Equal(t, server.PredictionSucceeded, resp1.Status)
	assert.Equal(t, "s=foobar, token=bar", resp1.Output)
	assert.Contains(t, resp1.Logs, "predicting foo\n")

	resp2 := prediction("bar", "baz", map[string]any{"i": 123456})
	assert.Equal(t, server.PredictionSucceeded, resp2.Status)
	assert.Equal(t, "i=123456, token=baz", resp2.Output)
	assert.Contains(t, resp2.Logs, "predicting bar\n")

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

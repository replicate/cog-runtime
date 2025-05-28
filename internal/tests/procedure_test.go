package tests

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"testing"

	"github.com/replicate/go/must"
	"github.com/stretchr/testify/assert"

	"github.com/replicate/cog-runtime/internal/server"
)

func procedureRequest(procedure string, token string, inputs map[string]any) map[string]any {
	r := make(map[string]any)
	r["procedure_source_url"] = fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, procedure)
	r["token"] = token
	r["inputs_json"] = string(must.Get(json.Marshal(inputs)))
	return r
}

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
		if *legacyCog {
			// Compat: legacy pipelines-runtime uses a wrapper predictor with these 3 inputs
			// So the request looks like:
			/*
				{
				  "input": {
				    "procedure_source_url": "file:///<path>",
				    "token": "***",
				    "inputs_json": "{\"s\":\"\"}"
				  }
				}
			*/
			req := make(map[string]any)
			req["procedure_source_url"] = fmt.Sprintf("file://%s/python/tests/procedures/%s", basePath, procedure)
			req["token"] = token
			req["inputs_json"] = string(must.Get(json.Marshal(input)))
			return ct.Prediction(req)
		} else {
			// cog-runtime moves procedure_source_url and token to top level of PredictionRequest
			// So no more inputs_json unwrapping
			req := server.PredictionRequest{
				ProcedureSourceURL: url,
				Token:              token,
				Input:              input,
			}
			return ct.prediction(http.MethodPost, "/predictions", req)
		}
	}

	resp1 := prediction("foo", "bar", map[string]any{"s": "foobar"})
	assert.Equal(t, server.PredictionSucceeded, resp1.Status)
	assert.Equal(t, "s=foobar, token=bar", resp1.Output)
	assert.Equal(t, resp1.Logs, "predicting foo\n")

	resp2 := prediction("bar", "baz", map[string]any{"i": 123456})
	assert.Equal(t, server.PredictionSucceeded, resp2.Status)
	assert.Equal(t, "i=123456, token=baz", resp2.Output)
	assert.Equal(t, resp2.Logs, "predicting bar\n")

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

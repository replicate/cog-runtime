package tests

import (
	"encoding/json"
	"errors"
	"fmt"
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

	resp1 := ct.Prediction(procedureRequest("foo", "bar", map[string]any{"s": "foobar"}))
	assert.Equal(t, server.PredictionSucceeded, resp1.Status)
	assert.Equal(t, "s=foobar, token=bar", resp1.Output)
	assert.Equal(t, resp1.Logs, "predicting foo\n")

	resp2 := ct.Prediction(procedureRequest("bar", "baz", map[string]any{"i": 123456}))
	assert.Equal(t, server.PredictionSucceeded, resp2.Status)
	assert.Equal(t, "i=123456, token=baz", resp2.Output)
	assert.Equal(t, resp2.Logs, "predicting bar\n")

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

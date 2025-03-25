package tests

import (
	"testing"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func TestPredictionDataclassCoderSucceeded(t *testing.T) {
	if *legacyCog {
		// Compat: legacy Cog does not support custom coder
		t.SkipNow()
	}
	ct := NewCogTest(t, "dataclass")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{
		"account": map[string]any{
			"id":          0,
			"name":        "John",
			"address":     map[string]any{"street": "Smith", "zip": 12345},
			"credentials": map[string]any{"password": "foo", "pubkey": b64encode("bar")},
		},
	})

	output := map[string]any{
		"account": map[string]any{
			"id":          100.0,
			"name":        "JOHN",
			"address":     map[string]any{"street": "SMITH", "zip": 22345.0},
			"credentials": map[string]any{"password": "**********", "pubkey": b64encode("*bar*")},
		},
	}
	ct.AssertResponse(resp, server.PredictionSucceeded, output, "")

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionChatCoderSucceeded(t *testing.T) {
	if *legacyCog {
		// Compat: legacy Cog does not support custom coder
		t.SkipNow()
	}
	ct := NewCogTest(t, "chat")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{"msg": map[string]any{"role": "assistant", "content": "bar"}})
	output := map[string]any{"role": "assistant", "content": "*bar*"}
	ct.AssertResponse(resp, server.PredictionSucceeded, output, "")

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/replicate/cog-runtime/internal/server"
)

func TestPredictionOutputSucceeded(t *testing.T) {
	ct := NewCogTest(t, "output")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{"p": b64encode("bar")})
	logs := "reading input file\nwriting output file\n"
	var b64 string
	if *legacyCog {
		// Compat: different MIME type detection logic
		b64 = b64encodeLegacy("*bar*")
	} else {
		b64 = b64encode("*bar*")
	}
	output := map[string]any{
		"path": b64,
		"text": "*bar*",
	}
	ct.AssertResponse(resp, server.PredictionSucceeded, output, logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

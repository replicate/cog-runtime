package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/replicate/cog-runtime/internal/server"
)

func TestPredictionSecretSucceeded(t *testing.T) {
	ct := NewCogTest(t, "secret")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{"s": "bar"})
	logs := "reading secret\nwriting secret\n"
	ct.AssertResponse(resp, server.PredictionSucceeded, "**********", logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

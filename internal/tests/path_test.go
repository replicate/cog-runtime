package tests

import (
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func b64encode(s string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(s))
	return fmt.Sprintf("data:.txt;base64,%s", b64)
}

func TestPredictionPathSucceeded(t *testing.T) {
	ct := NewCogTest(t, "path")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{"p": b64encode("bar")})
	logs := "reading input file\nwriting output file\n"
	ct.AssertResponse(resp, server.PredictionSucceeded, b64encode("*bar*"), logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

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
	e := NewCogTest(t, "path")
	assert.NoError(t, e.Start())

	hc := e.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := e.Prediction(map[string]any{"p": b64encode("bar")})
	assert.Equal(t, server.PredictionSucceeded, resp.Status)
	assert.Equal(t, b64encode("*bar*"), resp.Output)
	assert.Equal(t, "reading input file\nwriting output file\n", resp.Logs)

	e.Shutdown()
	assert.NoError(t, e.Cleanup())
}

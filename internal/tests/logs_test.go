package tests

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/replicate/cog-runtime/internal/server"
)

func TestLogs(t *testing.T) {
	ct := NewCogTest(t, "logs")
	// Replicate Go logger logs to stdout in production mode
	ct.AppendEnvs("LOG_FILE=stderr")
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	assert.NoError(t, ct.StartWithPipes(stdout, stderr))

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)
	hcLogs := "STDOUT: starting setup\nSTDERR: starting setup\nSTDOUT: completed setup\nSTDERR: completed setup\n"
	assert.Equal(t, hcLogs, hc.Setup.Logs)

	resp := ct.Prediction(map[string]any{"s": "bar"})
	logs := "STDOUT: starting prediction\nSTDERR: starting prediction\nSTDOUT: completed prediction\nSTDERR: completed prediction\n"
	ct.AssertResponse(resp, server.PredictionSucceeded, "*bar*", logs)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())

	sout := "STDOUT: starting setup\nSTDOUT: completed setup\nSTDOUT: starting prediction\nSTDOUT: completed prediction\n"
	assert.Equal(t, sout, stdout.String())
	stderrLines := strings.Split(stderr.String(), "\n")
	assert.Contains(t, stderrLines, "STDERR: starting setup")
	assert.Contains(t, stderrLines, "STDERR: completed setup")
	assert.Contains(t, stderrLines, "STDERR: starting prediction")
	assert.Contains(t, stderrLines, "STDERR: completed prediction")
}

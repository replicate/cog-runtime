package tests

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/replicate/cog-runtime/internal/server"

	"github.com/stretchr/testify/assert"
)

func b64encode(s string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(s))
	return fmt.Sprintf("data:text/plain; charset=utf-8;base64,%s", b64)
}

func b64encodeLegacy(s string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(s))
	return fmt.Sprintf("data:text/plain;base64,%s", b64)
}

func TestPredictionPathSucceeded(t *testing.T) {
	ct := NewCogTest(t, "path")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{"p": b64encode("bar")})
	logs := "reading input file\nwriting output file\n"
	if *legacyCog {
		// Compat: different MIME type detection logic
		ct.AssertResponse(resp, server.PredictionSucceeded, b64encodeLegacy("*bar*"), logs)
	} else {
		ct.AssertResponse(resp, server.PredictionSucceeded, b64encode("*bar*"), logs)
	}

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionPathOutputFilePrefixSucceeded(t *testing.T) {
	ct := NewCogTest(t, "path")
	ct.StartWebhook()
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.PredictionWithUpload(map[string]any{"p": b64encode("bar")})
	logs := "reading input file\nwriting output file\n"
	assert.Equal(t, server.PredictionSucceeded, resp.Status)
	output := resp.Output.(string)
	assert.True(t, strings.HasPrefix(output, ct.UploadUrl()))
	assert.Equal(t, logs, resp.Logs)

	ul := ct.GetUploads()
	assert.Len(t, ul, 1)
	body := string(ul[0].Body)
	assert.Contains(t, body, "*bar*")

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionPathUploadUrlSucceeded(t *testing.T) {
	ct := NewCogTest(t, "path")
	ct.StartWebhook()
	ct.AppendArgs(fmt.Sprintf("--upload-url=http://localhost:%d/upload/", ct.webhookPort))
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPrediction(map[string]any{"p": b64encode("bar")})
	wr := ct.WaitForWebhookCompletion()
	ul := ct.GetUploads()

	assert.Len(t, ul, 1)
	logs := "reading input file\nwriting output file\n"
	filename, ok := strings.CutPrefix(ul[0].Path, "/upload/")
	assert.True(t, ok)
	url := fmt.Sprintf("%s%s", ct.UploadUrl(), filename)
	ct.AssertResponses(wr, server.PredictionSucceeded, url, logs)

	body := string(ul[0].Body)
	assert.Contains(t, body, "*bar*")

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

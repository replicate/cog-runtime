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
	assert.True(t, strings.HasPrefix(output, fmt.Sprintf("http://localhost:%d/upload/", ct.webhookPort)))
	assert.Equal(t, logs, resp.Logs)

	ul := ct.GetUploads()
	assert.Len(t, ul, 1)
	body := string(ul[0].Body)
	assert.Contains(t, body, "*bar*")
	assert.Contains(t, body, "Content-Disposition: form-data; name=\"file\"; filename=\"")
	if !*legacyCog {
		// Compat: different HTTP multipart handling
		assert.Contains(t, body, "Content-Type: application/octet-stream")
	}

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
	wr := ct.WaitForWebhookResponses()
	ul := ct.GetUploads()

	if *legacyCog {
		assert.Len(t, wr, 2)
		assert.Len(t, ul, 1)
		logs := ""
		// Compat: legacy Cog sends no "starting" event
		ct.AssertResponse(wr[0], server.PredictionProcessing, nil, logs)
		logs += "reading input file\nwriting output file\n"
		url := fmt.Sprintf("http://localhost:%d%s", ct.webhookPort, ul[0].Path)
		ct.AssertResponse(wr[1], server.PredictionSucceeded, url, logs)
	} else {
		assert.Len(t, wr, 3)
		assert.Len(t, ul, 1)
		logs := ""
		ct.AssertResponse(wr[0], server.PredictionStarting, nil, logs)
		logs += "reading input file\n"
		ct.AssertResponse(wr[1], server.PredictionProcessing, nil, logs)
		logs += "writing output file\n"
		url := fmt.Sprintf("http://localhost:%d%s", ct.webhookPort, ul[0].Path)
		ct.AssertResponse(wr[2], server.PredictionSucceeded, url, logs)
	}

	body := string(ul[0].Body)
	assert.Contains(t, body, "*bar*")
	if !*legacyCog {
		// Compat: different HTTP multipart handling
		assert.Contains(t, body, "Content-Disposition: form-data; name=\"file\"; filename=\"")
		assert.Contains(t, body, "Content-Type: application/octet-stream")
	}

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

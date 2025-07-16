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

func TestPredictionPathBase64Succeeded(t *testing.T) {
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

func TestPredictionPathURLSucceeded(t *testing.T) {
	ct := NewCogTest(t, "path")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	resp := ct.Prediction(map[string]any{"p": "https://raw.githubusercontent.com/replicate/cog-runtime/refs/heads/main/.python-version"})
	logs := "reading input file\nwriting output file\n"
	if *legacyCog {
		// Compat: different MIME type detection logic
		ct.AssertResponse(resp, server.PredictionSucceeded, b64encodeLegacy("*3.9\n*"), logs)
	} else {
		ct.AssertResponse(resp, server.PredictionSucceeded, b64encode("*3.9\n*"), logs)
	}

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

func TestPredictionNotPathSucceeded(t *testing.T) {
	ct := NewCogTest(t, "not_path")
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	// s: str should not be handled
	resp := ct.Prediction(map[string]any{"s": "https://replicate.com"})
	ct.AssertResponse(resp, server.PredictionSucceeded, "*https://replicate.com*", "")

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
	if *legacyCog {
		// Compat: legacy Cog sends multipart with output_file_prefix but actual mime-type with --upload-url?
		assert.True(t, strings.HasPrefix(ul[0].ContentType, "multipart/form-data"))
	} else {
		assert.Equal(t, "text/plain; charset=utf-8", ul[0].ContentType)
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
	if *legacyCog {
		// Compat: legacy Cog does not detect text encoding?
		assert.Equal(t, "text/plain", ul[0].ContentType)
	} else {
		assert.Equal(t, "text/plain; charset=utf-8", ul[0].ContentType)
	}

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

const TestDataPrefix = "https://raw.githubusercontent.com/gabriel-vasile/mimetype/refs/heads/master/testdata/"

func TestPredictionPathMimeTypes(t *testing.T) {
	if *legacyCog {
		//Compat: legacy Cog sends multipart with output_file_prefix and does not upload with --upload-url
		t.SkipNow()
	}
	ct := NewCogTest(t, "mime")
	ct.StartWebhook()
	ct.AppendArgs(fmt.Sprintf("--upload-url=http://localhost:%d/upload/", ct.webhookPort))
	assert.NoError(t, ct.Start())

	hc := ct.WaitForSetup()
	assert.Equal(t, server.StatusReady.String(), hc.Status)
	assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)

	ct.AsyncPredictionWithId("p1", map[string]any{"u": TestDataPrefix + "gif.gif"})
	ct.WaitForWebhookCompletion()

	ct.AsyncPredictionWithId("p2", map[string]any{"u": TestDataPrefix + "jar.jar"})
	ct.WaitForWebhookCompletion()

	ct.AsyncPredictionWithId("p3", map[string]any{"u": TestDataPrefix + "tar.tar"})
	ct.WaitForWebhookCompletion()

	ct.AsyncPredictionWithId("p4", map[string]any{"u": "https://www.gstatic.com/webp/gallery/1.sm.webp"})
	ct.WaitForWebhookCompletion()

	ul := ct.GetUploads()
	assert.Len(t, ul, 4)

	assert.Equal(t, "image/gif", ul[0].ContentType)
	mimeJar := "application/jar"
	if *legacyCog {
		mimeJar = "application/java-archive"
	}
	assert.Equal(t, mimeJar, ul[1].ContentType)
	assert.Equal(t, "application/x-tar", ul[2].ContentType)
	assert.Equal(t, "image/webp", ul[3].ContentType)

	ct.Shutdown()
	assert.NoError(t, ct.Cleanup())
}

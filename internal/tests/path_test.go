package tests

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
	"github.com/replicate/cog-runtime/internal/util"
)

func testDataContentServer(t *testing.T) *httptest.Server {
	fsys := os.DirFS("testdata")
	s := httptest.NewServer(http.FileServer(http.FS(fsys)))
	t.Cleanup(s.Close)
	return s
}

func b64encode(s string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(s))
	return fmt.Sprintf("data:text/plain; charset=utf-8;base64,%s", b64)
}

func b64encodeLegacy(s string) string {
	b64 := base64.StdEncoding.EncodeToString([]byte(s))
	return fmt.Sprintf("data:text/plain;base64,%s", b64)
}

func TestPredictionPathBase64Succeeded(t *testing.T) {
	t.Parallel()
	allowedOutputs := []string{
		b64encode("*bar*"),
		b64encodeLegacy("*bar*"),
	}
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "path",
		predictorClass:   "Predictor",
	})
	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	prediction := map[string]any{"p": b64encode("bar")}
	req := httpPredictionRequest(t, runtimeServer, server.PredictionRequest{Input: prediction})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var predictionResponse server.PredictionResponse
	err = json.Unmarshal(body, &predictionResponse)
	require.NoError(t, err)

	assert.Equal(t, server.PredictionSucceeded, predictionResponse.Status)
	assert.Contains(t, allowedOutputs, predictionResponse.Output)
	assert.Equal(t, "reading input file\nwriting output file\n", predictionResponse.Logs)
}

func TestPredictionPathURLSucceeded(t *testing.T) {
	t.Parallel()
	allowedOutputs := []string{
		b64encode("*3.9\n*"),
		b64encodeLegacy("*3.9\n*"),
	}
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "path",
		predictorClass:   "Predictor",
	})
	ts := testDataContentServer(t)
	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	prediction := map[string]any{"p": ts.URL + "/.python_version"}
	req := httpPredictionRequest(t, runtimeServer, server.PredictionRequest{Input: prediction})
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var predictionResponse server.PredictionResponse
	err = json.Unmarshal(body, &predictionResponse)

	assert.Equal(t, server.PredictionSucceeded, predictionResponse.Status)

	assert.Contains(t, allowedOutputs, predictionResponse.Output)
	assert.Equal(t, "reading input file\nwriting output file\n", predictionResponse.Logs)
}

func TestPredictionNotPathSucceeded(t *testing.T) {
	t.Parallel()
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        "",
		module:           "not_path",
		predictorClass:   "Predictor",
	})

	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	prediction := map[string]any{"s": "https://replicate.com"}
	req := httpPredictionRequest(t, runtimeServer, server.PredictionRequest{Input: prediction})

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var predictionResponse server.PredictionResponse
	err = json.Unmarshal(body, &predictionResponse)
	require.NoError(t, err)

	assert.Equal(t, server.PredictionSucceeded, predictionResponse.Status)
	assert.Equal(t, "*https://replicate.com*", predictionResponse.Output)
	assert.Equal(t, "", predictionResponse.Logs)
}

func TestPredictionPathOutputFilePrefixSucceeded(t *testing.T) {
	t.Parallel()
	allowedContentTypes := []string{"text/plain; charset=utf-8", "text/plain"}
	receiverServer := testHarnessReceiverServer(t)
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        receiverServer.URL + "/upload/",
		module:           "path",
		predictorClass:   "Predictor",
	})
	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	prediction := server.PredictionRequest{
		Input:               map[string]any{"p": b64encode("bar")},
		Webhook:             receiverServer.URL + "/webhook",
		WebhookEventsFilter: []server.WebhookEvent{server.WebhookCompleted},
	}
	req := httpPredictionRequest(t, runtimeServer, prediction)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)
	_, _ = io.Copy(io.Discard, resp.Body)

	// Wait for webhook completion
	var webhook webhookData
	select {
	case webhook = <-receiverServer.webhookReceiverChan:
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for webhook")
	}

	assert.Equal(t, server.PredictionSucceeded, webhook.Response.Status)
	assert.Equal(t, "reading input file\nwriting output file\n", webhook.Response.Logs)
	output, ok := webhook.Response.Output.(string)
	assert.True(t, ok)
	assert.True(t, strings.HasPrefix(output, receiverServer.URL+"/upload/"))

	filename, ok := strings.CutPrefix(output, receiverServer.URL+"/upload/")
	assert.True(t, ok)

	// Ensure we have reeived the upload before continuing.
	var uploadData uploadData
	select {
	case uploadData = <-receiverServer.uploadReceiverChan:
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for upload")
	}

	assert.Len(t, receiverServer.uploadRequests, 1)
	assert.Equal(t, "PUT", uploadData.Method)
	assert.Equal(t, "/upload/"+filename, uploadData.Path)
	assert.Contains(t, allowedContentTypes, uploadData.ContentType)
	assert.Equal(t, "*bar*", string(uploadData.Body))
}

func TestPredictionPathUploadUrlSucceeded(t *testing.T) {
	t.Parallel()
	allowedContentTypes := []string{"text/plain; charset=utf-8", "text/plain"}
	receiverServer := testHarnessReceiverServer(t)
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        receiverServer.URL + "/upload/",
		module:           "path",
		predictorClass:   "Predictor",
	})

	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	prediction := server.PredictionRequest{
		Input:               map[string]any{"p": b64encode("bar")},
		Webhook:             receiverServer.URL + "/webhook",
		WebhookEventsFilter: []server.WebhookEvent{server.WebhookCompleted},
	}
	req := httpPredictionRequest(t, runtimeServer, prediction)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	_, _ = io.Copy(io.Discard, resp.Body)

	// Wait for webhook completion
	var webhook webhookData
	select {
	case webhook = <-receiverServer.webhookReceiverChan:
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for webhook")
	}

	assert.Equal(t, server.PredictionSucceeded, webhook.Response.Status)
	assert.Equal(t, "reading input file\nwriting output file\n", webhook.Response.Logs)
	output, ok := webhook.Response.Output.(string)
	assert.True(t, ok)
	assert.True(t, strings.HasPrefix(output, receiverServer.URL+"/upload/"))

	filename, ok := strings.CutPrefix(output, receiverServer.URL+"/upload/")
	assert.True(t, ok)

	// Ensure we have received the upload before continuing
	var uploadData uploadData
	select {
	case uploadData = <-receiverServer.uploadReceiverChan:
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for upload")
	}

	assert.Len(t, receiverServer.uploadRequests, 1)
	assert.Equal(t, "PUT", uploadData.Method)
	assert.Equal(t, "/upload/"+filename, uploadData.Path)
	assert.Contains(t, allowedContentTypes, uploadData.ContentType)
	assert.Equal(t, "*bar*", string(uploadData.Body))
}

func TestPredictionPathUploadIterator(t *testing.T) {
	t.Parallel()
	receiverServer := testHarnessReceiverServer(t)
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        receiverServer.URL + "/upload/",
		module:           "path_out_iter",
		predictorClass:   "Predictor",
	})
	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	prediction := server.PredictionRequest{
		Input:   map[string]any{"n": 3},
		Webhook: receiverServer.URL + "/webhook",
	}
	req := httpPredictionRequest(t, runtimeServer, prediction)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	_, _ = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)

	// Process and validate the webhook data
	timer := time.After(10 * time.Second)
	for count := 0; count < 5; count++ {
		select {
		case webhook := <-receiverServer.webhookReceiverChan:
			switch count {
			case 0:
				assert.Equal(t, server.PredictionProcessing, webhook.Response.Status)
				assert.Nil(t, webhook.Response.Output)
			case 1, 2, 3:
				assert.Equal(t, server.PredictionProcessing, webhook.Response.Status)
				output, ok := webhook.Response.Output.([]any)
				require.True(t, ok)
				assert.Len(t, output, count)
			case 4:
				assert.Equal(t, server.PredictionSucceeded, webhook.Response.Status)
				output, ok := webhook.Response.Output.([]any)
				require.True(t, ok)
				assert.Len(t, output, 3)
			}
		case <-timer:
			t.Fatalf("timeout waiting for webhooks")
		}
	}
	assert.Len(t, receiverServer.webhookRequests, 5)

	// Process and validate the uploads
	timer = time.After(10 * time.Second)
	for count := 0; count < 3; count++ {
		select {
		case upload := <-receiverServer.uploadReceiverChan:
			assert.Equal(t, "out"+strconv.Itoa(count), string(upload.Body))
		case <-timer:
			t.Fatalf("timeout waiting for uploads")
		}
	}
	assert.Len(t, receiverServer.webhookRequests, 5)
}

func TestPredictionPathMimeTypes(t *testing.T) {
	t.Parallel()
	receiverServer := testHarnessReceiverServer(t)
	contentServer := testDataContentServer(t)
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        receiverServer.URL + "/upload/",
		module:           "mime",
		predictorClass:   "Predictor",
	})
	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	testDataPrefix := contentServer.URL + "/mimetype/"

	gifPredictionID, err := util.PredictionId()
	require.NoError(t, err)
	jarPredictionID, err := util.PredictionId()
	require.NoError(t, err)
	tarPredictionID, err := util.PredictionId()
	require.NoError(t, err)
	webpPredictionID, err := util.PredictionId()
	require.NoError(t, err)

	predictions := []struct {
		fileName            string
		predictionID        string
		allowedContentTypes []string
	}{
		{
			fileName:            "gif.gif",
			predictionID:        gifPredictionID,
			allowedContentTypes: []string{"image/gif"},
		},
		{
			fileName:            "jar.jar",
			predictionID:        jarPredictionID,
			allowedContentTypes: []string{"application/jar", "application/java-archive"},
		},
		{
			fileName:            "tar.tar",
			predictionID:        tarPredictionID,
			allowedContentTypes: []string{"application/x-tar"},
		},
		{
			fileName:            "1.sm.webp",
			predictionID:        webpPredictionID,
			allowedContentTypes: []string{"image/webp"},
		},
	}
	for _, tc := range predictions {
		// Each of these are treated as subtests, they will be run serially
		t.Run(tc.fileName, func(t *testing.T) {
			prediction := server.PredictionRequest{
				Input:               map[string]any{"u": testDataPrefix + tc.fileName},
				Id:                  tc.predictionID,
				Webhook:             receiverServer.URL + "/webhook",
				WebhookEventsFilter: []server.WebhookEvent{server.WebhookCompleted},
			}
			t.Logf("prediction file: %s", tc.fileName)
			req := httpPredictionRequestWithId(t, runtimeServer, prediction)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusAccepted, resp.StatusCode)

			_, _ = io.Copy(io.Discard, resp.Body)

			// Wait for webhook completion
			select {
			case webhook := <-receiverServer.webhookReceiverChan:
				assert.Equal(t, server.PredictionSucceeded, webhook.Response.Status)
			case <-time.After(10 * time.Second):
				t.Fatalf("timeout waiting for webhook")
			}

			// Validate the upload
			select {
			case upload := <-receiverServer.uploadReceiverChan:
				assert.Contains(t, tc.allowedContentTypes, upload.ContentType)
				assert.Equal(t, "PUT", upload.Method)
			case <-time.After(10 * time.Second):
				t.Fatalf("timeout waiting for upload")
			}
		})
	}

	// Ensure we didn't receive any superfluous uploads
	assert.Len(t, receiverServer.uploadRequests, len(predictions))
}

func TestPredictionPathMultiMimeTypes(t *testing.T) {
	receiverServer := testHarnessReceiverServer(t)
	contentServer := testDataContentServer(t)
	runtimeServer := setupCogRuntime(t, cogRuntimeServerConfig{
		procedureMode:    false,
		explicitShutdown: true,
		uploadURL:        receiverServer.URL + "/upload/",
		module:           "mimes",
		predictorClass:   "Predictor",
	})
	waitForSetupComplete(t, runtimeServer, server.StatusReady, server.SetupSucceeded)

	files := []struct {
		fileName            string
		allowedContentTypes []string
	}{
		{
			fileName:            "gif.gif",
			allowedContentTypes: []string{"image/gif"},
		},
		{
			fileName:            "jar.jar",
			allowedContentTypes: []string{"application/jar", "application/java-archive"},
		},
		{
			fileName:            "tar.tar",
			allowedContentTypes: []string{"application/x-tar"},
		},
		{
			fileName:            "1.sm.webp",
			allowedContentTypes: []string{"image/webp"},
		},
	}

	prediction := server.PredictionRequest{
		Input: map[string]any{"us": []string{
			contentServer.URL + "/mimetype/" + files[0].fileName,
			contentServer.URL + "/mimetype/" + files[1].fileName,
			contentServer.URL + "/mimetype/" + files[2].fileName,
			contentServer.URL + "/mimetype/" + files[3].fileName,
		}},
		Webhook:             receiverServer.URL + "/webhook",
		WebhookEventsFilter: []server.WebhookEvent{server.WebhookCompleted},
	}

	req := httpPredictionRequest(t, runtimeServer, prediction)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	_, _ = io.Copy(io.Discard, resp.Body)

	// Wait for webhook completion
	select {
	case webhook := <-receiverServer.webhookReceiverChan:
		assert.Equal(t, server.PredictionSucceeded, webhook.Response.Status)
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for webhook")
	}

	// Validate the uploads
	for _, file := range files {
		select {
		case upload := <-receiverServer.uploadReceiverChan:
			assert.Contains(t, file.allowedContentTypes, upload.ContentType)
		case <-time.After(10 * time.Second):
			t.Fatalf("timeout waiting for upload")
		}
	}
	// Ensure we didn't receive any superfluous uploads
	assert.Len(t, receiverServer.uploadRequests, len(files))
}

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/replicate/cog-runtime/internal/server"
)

// This file implements the basis for the test harness. It is used to test the
// runtime server.

type webhookData struct {
	Method string
	Path   string
	Body   []byte
}

type uploadData struct {
	Method      string
	Path        string
	ContentType string
	Body        []byte
}

type testHarnessReceiver struct {
	*httptest.Server

	mu              sync.Mutex
	webhookRequests []webhookData
	uploadRequests  []uploadData

	webhookReceived chan bool
	uploadReceived  chan bool
}

func (tr *testHarnessReceiver) webhookHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		tr.webhookRequests = append(tr.webhookRequests, webhookData{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   body,
		})
		tr.webhookReceived <- true
	}
}

func (tr *testHarnessReceiver) uploadHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		tr.uploadRequests = append(tr.uploadRequests, uploadData{
			Method:      r.Method,
			Path:        r.URL.Path,
			ContentType: r.Header.Get("Content-Type"),
			Body:        body,
		})
		tr.uploadReceived <- true
	}
}

func testHarnessReceiverServer(t *testing.T) *testHarnessReceiver {
	t.Helper()
	tr := &testHarnessReceiver{}
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", tr.webhookHandler(t))
	mux.HandleFunc("/upload", tr.uploadHandler(t))
	tr.webhookReceived = make(chan bool, 1)
	tr.uploadReceived = make(chan bool, 1)
	tr.Server = httptest.NewServer(mux)
	return tr
}

func setupCogRuntimeServer(t *testing.T, procedureMode bool, legacyCog bool, explicitShutdown bool, uploadURL string, module string, predictorClass string) *httptest.Server {
	t.Helper()
	tempDir := t.TempDir()
	t.Logf("Working directory: %s", tempDir)
	t.Cleanup(func() {
		os.RemoveAll(tempDir)
	})
	// FIXME: This is for compatibility with the `cog_test` test harness while we migrate to in-process testing. This allows us
	// to specify the python venvs and binary in the same way as for minimizing the blast radius of changes.
	_, b, _, _ := runtime.Caller(0)
	basePath := path.Dir(path.Dir(path.Dir(b)))

	var pathEnv string

	// SetupEnvs for downstream use
	switch {
	case legacyCog && procedureMode:
		pathEnv = path.Join(basePath, ".venv-procedure", "bin")
	case legacyCog:
		pathEnv = path.Join(basePath, ".venv-legacy", "bin")
	default:
		pathEnv = path.Join(basePath, ".venv", "bin")
	}

	pythonPathEnv := path.Join(basePath, "python")

	// NOTE(morgan): this is a special case, we need the IPCUrl which is homed on the server before we create the handler. Create a nil
	// handler server and then set the handler after.
	s := httptest.NewServer(nil)

	serverCfg := server.Config{
		UseProcedureMode:      procedureMode,
		AwaitExplicitShutdown: explicitShutdown,
		UploadUrl:             uploadURL,
		WorkingDirectory:      tempDir,
		IPCUrl:                s.URL + "/_ipc",
		EnvSet: map[string]string{
			"PATH":       fmt.Sprintf("%s:%s", pathEnv, os.Getenv("PATH")),
			"PYTHONPATH": pythonPathEnv,
		},
	}
	concurrencyMax := 1
	if strings.HasPrefix(module, "async_") {
		concurrencyMax = 2
	}
	writeCogConfig(t, tempDir, predictorClass, concurrencyMax)
	linkPythonModule(t, basePath, tempDir, module)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	// NOTE(morgan): We now have the IPCUrl, so we can create the handler.
	// FIXME: This should be done over unix sockets instead of HTTP, it resolves
	// the chicken and egg problem of needing the IPCUrl to create the handler.
	handler, err := server.NewHandler(serverCfg, cancel)
	require.NoError(t, err)
	mux := server.NewServeMux(handler, serverCfg.UseProcedureMode)
	s.Config.Handler = mux

	// FIXME: This is a hack to cover shutdown logic that is expected. This
	// is more compatbility for the migration away from `cog_test`
	go func() {
		<-ctx.Done()
		s.Close()
	}()
	return s
}

type cogConfig struct {
	Predict     string `json:"predict"`
	Concurrency struct {
		Max int `json:"max"`
	} `json:"concurrency,omitempty"`
}

// writeCogConfig creates a cog.yaml file that contains json-ified version of the config.
// As JSON is a strict subset of YAML, this allows us to stdlib instead of needing external
// yaml-specific dependencies for a very basic cog.yaml
func writeCogConfig(t *testing.T, tempDir string, predictorClass string, concurrencyMax int) {
	t.Helper()
	conf := cogConfig{
		Predict: "predict.py:" + predictorClass,
	}
	if concurrencyMax > 0 {
		conf.Concurrency = struct {
			Max int `json:"max"`
		}{Max: concurrencyMax}
	}
	cogConfigFilePath := path.Join(tempDir, "cog.yaml")
	cogConfigFile, err := os.OpenFile(cogConfigFilePath, os.O_CREATE|os.O_WRONLY, 0644)
	require.NoError(t, err)
	err = json.NewEncoder(cogConfigFile).Encode(conf)
	require.NoError(t, err)
}

// linkPythonModule links the python module into the temp directory.
// FIXME: this is a hack to provide compatibility with the `cog_test` test harness while we migrate to in-process testing.
func linkPythonModule(t *testing.T, basePath string, tempDir string, module string) {
	t.Helper()
	runnersPath := path.Join(basePath, "python", "tests", "runners")
	err := os.Symlink(path.Join(runnersPath, fmt.Sprintf("%s.py", module)), path.Join(tempDir, "predict.py"))
	require.NoError(t, err)
}

func healthCheck(t *testing.T, testServer *httptest.Server) server.HealthCheck {
	t.Helper()
	url := testServer.URL + "/health-check"
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var hc server.HealthCheck
	err = json.Unmarshal(body, &hc)
	require.NoError(t, err)
	return hc
}

func waitForSetupComplete(t *testing.T, testServer *httptest.Server) server.HealthCheck {
	t.Helper()

	timer := time.NewTicker(10 * time.Millisecond)
	defer timer.Stop()

	for range timer.C {
		hc := healthCheck(t, testServer)
		if hc.Status != server.StatusStarting.String() {
			assert.Equal(t, server.StatusReady.String(), hc.Status)
			assert.Equal(t, server.SetupSucceeded, hc.Setup.Status)
			return hc
		}
	}
	return server.HealthCheck{}
}

func httpPredictionRequest(t *testing.T, runtimeServer *httptest.Server, receiverServer *testHarnessReceiver, prediction server.PredictionRequest) *http.Request {
	t.Helper()
	url := runtimeServer.URL + "/predictions"
	body, err := json.Marshal(prediction)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if receiverServer != nil && prediction.Webhook != "" {
		req.Header.Set("Prefer", "respond-async")
	}
	return req
}
